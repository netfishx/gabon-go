package task

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/netfishx/gabon-go/internal/apierr"
	"github.com/netfishx/gabon-go/internal/db"
	"github.com/netfishx/gabon-go/internal/tz"
	"github.com/netfishx/gabon-go/internal/wallet"
)

const maxProofImages = 9

func claimNotFound() *apierr.Error {
	return apierr.New(http.StatusNotFound, apierr.CodeClaimTaskNotFound, "claim task not found")
}

// Claim 领取限时任务：窗口内 + VIP 门槛 + 一人一次（唯一约束）；
// reward_base 与 expires_at 领取时快照（后续定义变更不影响本次，除非运营回写）。
func (s *Service) Claim(ctx context.Context, customerID, vipLevel int, taskID int64) error {
	task, err := s.q.GetClaimTask(ctx, taskID)
	if errors.Is(err, pgx.ErrNoRows) {
		return claimNotFound()
	}
	if err != nil {
		return fmt.Errorf("get claim task: %w", err)
	}
	if !task.Enabled {
		return apierr.New(http.StatusConflict, apierr.CodeClaimTaskOffline, "task is offline")
	}
	now := time.Now().In(tz.Shanghai)
	if task.StartsAt.Valid && now.Before(task.StartsAt.Time) ||
		task.EndsAt.Valid && !now.Before(task.EndsAt.Time) {
		return apierr.New(http.StatusConflict, apierr.CodeClaimTaskWindowClosed, "task is not within its claim window")
	}
	if int(task.MinVipLevel) > vipLevel {
		return apierr.New(http.StatusForbidden, apierr.CodeClaimTaskVipRequired, "vip level not high enough")
	}
	_, err = s.q.InsertTaskClaim(ctx, db.InsertTaskClaimParams{
		CustomerID: int64(customerID), TaskID: taskID,
		RewardBase: task.Reward, ExpiresAt: task.EndsAt,
	})
	if db.UniqueViolationConstraint(err) == "task_claims_customer_task_key" {
		return apierr.New(http.StatusConflict, apierr.CodeClaimTaskAlreadyClaimed, "already claimed")
	}
	if err != nil {
		return fmt.Errorf("insert claim: %w", err)
	}
	return nil
}

// Submit 提交证明：校验后归属校验交由 api 层（需对象存储）；此处只落库与状态流转。
// claimed/rejected 可提交（驳回重提覆盖凭证回 submitted）；下架/过期即时拦截。
func (s *Service) Submit(ctx context.Context, customerID, claimID int64, proofText *string, images []string) error {
	if len(images) < 1 || len(images) > maxProofImages {
		return apierr.InvalidArgument("proof must have 1-9 images")
	}
	claim, err := s.q.GetTaskClaim(ctx, claimID)
	if errors.Is(err, pgx.ErrNoRows) {
		return claimNotFound()
	}
	if err != nil {
		return fmt.Errorf("get claim: %w", err)
	}
	if claim.CustomerID != customerID {
		return claimNotFound() // 非本人：不泄露存在性
	}
	if claim.ExpiresAt.Valid && !time.Now().In(tz.Shanghai).Before(claim.ExpiresAt.Time) {
		return apierr.New(http.StatusConflict, apierr.CodeClaimTaskWindowClosed, "claim has expired")
	}
	rows, err := s.q.SubmitTaskClaim(ctx, db.SubmitTaskClaimParams{
		ID: claimID, CustomerID: customerID, ProofText: proofText, ProofImages: images,
	})
	if err != nil {
		return fmt.Errorf("submit claim: %w", err)
	}
	if rows == 0 {
		return apierr.New(http.StatusConflict, apierr.CodeClaimTaskNotSubmittable, "claim is not in a submittable state")
	}
	return nil
}

// Approve 审核通过即发奖（一步）：submitted→rewarded 单次条件 UPDATE + 同事务入账。
// 倍率取审核时刻 VIP 档；幂等：条件 UPDATE 0 行即已终态（返回冲突）。
func (s *Service) Approve(ctx context.Context, adminID, claimID int64) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		claim, err := q.GetTaskClaim(ctx, claimID)
		if errors.Is(err, pgx.ErrNoRows) {
			return claimNotFound()
		}
		if err != nil {
			return fmt.Errorf("get claim: %w", err)
		}
		bp, err := q.GetCustomerRewardMultiplierBp(ctx, claim.CustomerID)
		if err != nil {
			return fmt.Errorf("read multiplier: %w", err)
		}
		reward := claim.RewardBase * int64(bp) / 10000 // floor
		customerID, err := q.ApproveTaskClaim(ctx, db.ApproveTaskClaimParams{
			ID: claimID, ReviewedBy: &adminID, RewardGranted: &reward,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return apierr.New(http.StatusConflict, apierr.CodeClaimTaskNotReviewable, "claim is not pending review")
		}
		if err != nil {
			return fmt.Errorf("approve claim: %w", err)
		}
		if reward <= 0 {
			return nil
		}
		return s.wallets.CreditTx(ctx, tx, wallet.CreditParams{
			CustomerID: customerID,
			Type:       db.TransactionTypeClaimTaskReward,
			Amount:     reward,
			RefID:      &claimID,
		})
	})
}

// Reject 驳回：理由必填；submitted→rejected。
func (s *Service) Reject(ctx context.Context, adminID, claimID int64, remark string) error {
	if remark == "" {
		return apierr.InvalidArgument("reject remark is required")
	}
	rows, err := s.q.RejectTaskClaim(ctx, db.RejectTaskClaimParams{
		ID: claimID, ReviewedBy: &adminID, ReviewRemark: &remark,
	})
	if err != nil {
		return fmt.Errorf("reject claim: %w", err)
	}
	if rows == 0 {
		return apierr.New(http.StatusConflict, apierr.CodeClaimTaskNotReviewable, "claim is not pending review")
	}
	return nil
}

// ListPendingClaims 待审核队列（id 升序游标分页）。
func (s *Service) ListPendingClaims(ctx context.Context, cursor int64, limit int32) (items []db.ListPendingClaimsRow, next int64, err error) {
	items, err = s.q.ListPendingClaims(ctx, db.ListPendingClaimsParams{Cursor: cursor, RowLimit: limit + 1})
	if err != nil {
		return nil, 0, fmt.Errorf("list pending claims: %w", err)
	}
	if len(items) > int(limit) {
		items = items[:limit]
		next = items[len(items)-1].ID
	}
	return items, next, nil
}
