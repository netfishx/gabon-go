package customer

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/netfishx/gabon-go/internal/db"
	"github.com/netfishx/gabon-go/internal/wallet"
)

// MarkValidIfQualifiedTx 在调用方事务内做有效用户判定：
// 三条件（有作品、有成功邀请、有联系方式）由一条原子条件 UPDATE 完成，
// valid_at 的 CAS 保证只翻转一次、永不回退；翻转成功即在同一事务内给邀请人发奖。
// 返回本次是否翻转。
// 触发点：注册（对邀请人）、资料修改写联系方式（对本人）、视频审核通过（对作者，装配层回调注入）。
func (s *Service) MarkValidIfQualifiedTx(ctx context.Context, tx pgx.Tx, customerID int64) (bool, error) {
	inviterID, err := s.q.WithTx(tx).MarkCustomerValidIfQualified(ctx, customerID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil // 条件未凑齐或已有效：本次未翻转
	}
	if err != nil {
		return false, fmt.Errorf("mark valid if qualified: %w", err)
	}
	if inviterID != nil {
		if err := s.grantInviteRewardTx(ctx, tx, *inviterID, customerID); err != nil {
			return false, err
		}
	}
	return true, nil
}

// grantInviteRewardTx 翻转事务内给邀请人发放邀请有效奖励。
// 金额读活动奖励配置（缺行/停用/为 0 → 不发放，不复刻旧版代码 123 兜底）；
// 受邀请人 VIP 档 invite_reward_cap 约束：有效邀请数（含本次，事务内可见）超过上限即跳过。
// 发奖幂等第一道闸是 valid_at 的 CAS；流水 (type, ref=被邀请人) 唯一约束是最后防线，
// 撞上即数据异常，错误原样上抛令整个触发事务回滚。
func (s *Service) grantInviteRewardTx(ctx context.Context, tx pgx.Tx, inviterID, inviteeID int64) error {
	q := s.q.WithTx(tx)

	reward, err := q.GetInviteValidReward(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read invite reward config: %w", err)
	}
	if reward <= 0 {
		return nil
	}

	inviter, err := q.GetCustomerByID(ctx, inviterID)
	if err != nil {
		return fmt.Errorf("load inviter %d: %w", inviterID, err)
	}
	rewardCap, err := q.GetVipInviteRewardCap(ctx, inviter.VipLevel)
	if err != nil {
		return fmt.Errorf("read vip %d invite cap: %w", inviter.VipLevel, err)
	}
	validCount, err := q.CountValidInvitees(ctx, &inviterID)
	if err != nil {
		return fmt.Errorf("count valid invitees: %w", err)
	}
	if validCount > rewardCap {
		return nil // 超出邀请奖励上限：实际至多发 cap 笔
	}

	return s.wallets.CreditTx(ctx, tx, wallet.CreditParams{
		CustomerID: inviterID,
		Type:       db.TransactionTypeInviteValidReward,
		Amount:     reward,
		RefID:      &inviteeID,
	})
}

// CountValidInvitees 有效邀请数：该客户名下已翻转为有效用户的被邀请人数量。
// 注意与 customers.invite_count（总邀请数，注册即算）语义区分。
func (s *Service) CountValidInvitees(ctx context.Context, inviterID int64) (int64, error) {
	n, err := s.q.CountValidInvitees(ctx, &inviterID)
	if err != nil {
		return 0, fmt.Errorf("count valid invitees: %w", err)
	}
	return n, nil
}
