// Package signin 签到域：日签 + 当月累计里程碑奖励（按 VIP 倍率放大）。
package signin

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/netfishx/gabon-go/internal/apierr"
	"github.com/netfishx/gabon-go/internal/db"
	"github.com/netfishx/gabon-go/internal/tz"
	"github.com/netfishx/gabon-go/internal/wallet"
)

// Service 签到域服务。
type Service struct {
	pool    *pgxpool.Pool
	q       *db.Queries
	wallets *wallet.Service
}

// NewService 构造签到域服务。
func NewService(pool *pgxpool.Pool, wallets *wallet.Service) *Service {
	return &Service{pool: pool, q: db.New(pool), wallets: wallets}
}

// pgDate 便捷构造 pgtype.Date。
func pgDate(t time.Time) pgtype.Date {
	return pgtype.Date{Time: t, Valid: true}
}

// SignIn 每日打卡：唯一约束防重签；日签与里程碑各自 floor 放大、各自入账（拆两类流水）。
// 达标发奖与签到落库同一事务原子完成。
func (s *Service) SignIn(ctx context.Context, customerID int64) error {
	now := time.Now().In(tz.Shanghai)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, tz.Shanghai)
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, tz.Shanghai)

	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)

		bp, err := q.GetCustomerRewardMultiplierBp(ctx, customerID)
		if err != nil {
			return fmt.Errorf("read multiplier: %w", err)
		}

		// 日签奖励（缺配置/停用则不发）
		dailyReward, err := rewardAmount(bp, func() (int64, error) {
			return q.GetDailySignInReward(ctx)
		})
		if err != nil {
			return err
		}
		signIn, err := q.InsertSignIn(ctx, db.InsertSignInParams{
			CustomerID: customerID, SignDate: pgDate(today), RewardAmount: dailyReward,
		})
		if db.UniqueViolationConstraint(err) == "sign_ins_daily_key" {
			return apierr.New(http.StatusConflict, apierr.CodeSignInAlreadyToday, "already signed in today")
		}
		if err != nil {
			return fmt.Errorf("insert sign-in: %w", err)
		}
		if dailyReward > 0 {
			if err := s.wallets.CreditTx(ctx, tx, wallet.CreditParams{
				CustomerID: customerID, Type: db.TransactionTypeSignInReward,
				Amount: dailyReward, RefID: &signIn.ID,
			}); err != nil {
				return err
			}
		}

		// 里程碑：当月累计天数命中配置档位则额外发放（自然月天数 ≤31，安全落入 int32）
		dayCount, err := q.CountSignInsInMonth(ctx, db.CountSignInsInMonthParams{
			CustomerID: customerID, AnyDay: pgDate(today),
		})
		if err != nil {
			return fmt.Errorf("count month sign-ins: %w", err)
		}
		return s.grantMilestone(ctx, q, tx, customerID, monthStart, dayCount, bp)
	})
}

// grantMilestone 当月累计天数命中里程碑档位时额外发放（唯一约束幂等）。
// dayCount 为当月签到天数（自然月 ≤31），窄化为 int32 匹配 threshold 列。
func (s *Service) grantMilestone(ctx context.Context, q *db.Queries, tx pgx.Tx, customerID int64, monthStart time.Time, dayCount int64, bp int32) error {
	threshold := int32(dayCount) //nolint:gosec // 自然月签到天数 ≤31，无溢出风险
	base, err := q.GetMilestoneReward(ctx, threshold)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil // 该天数不是里程碑档位
	}
	if err != nil {
		return fmt.Errorf("read milestone reward: %w", err)
	}
	amount := base * int64(bp) / 10000 // floor
	if amount <= 0 {
		return nil
	}
	award, err := q.InsertMilestoneAward(ctx, db.InsertMilestoneAwardParams{
		CustomerID: customerID, Month: pgDate(monthStart), Threshold: threshold, RewardAmount: amount,
	})
	if db.UniqueViolationConstraint(err) == "milestone_awards_key" {
		return nil // 同月同档已发，幂等
	}
	if err != nil {
		return fmt.Errorf("insert milestone award: %w", err)
	}
	return s.wallets.CreditTx(ctx, tx, wallet.CreditParams{
		CustomerID: customerID, Type: db.TransactionTypeMilestoneReward,
		Amount: amount, RefID: &award.ID,
	})
}

// rewardAmount 读配置基础值并按倍率放大（floor）；缺行/停用返回 0（不发放）。
func rewardAmount(bp int32, get func() (int64, error)) (int64, error) {
	base, err := get()
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("read reward config: %w", err)
	}
	return base * int64(bp) / 10000, nil
}

// Status 本月签到状态：已签天数、今日是否已签、下一里程碑进度由 handler 组装。
type Status struct {
	SignedDays  int64
	TodaySigned bool
}

// Status 查询本月签到状态。
func (s *Service) Status(ctx context.Context, customerID int64) (Status, error) {
	now := time.Now().In(tz.Shanghai)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, tz.Shanghai)
	days, err := s.q.CountSignInsInMonth(ctx, db.CountSignInsInMonthParams{
		CustomerID: customerID, AnyDay: pgDate(today),
	})
	if err != nil {
		return Status{}, fmt.Errorf("count month sign-ins: %w", err)
	}
	signed, err := s.q.TodaySigned(ctx, db.TodaySignedParams{CustomerID: customerID, SignDate: pgDate(today)})
	if err != nil {
		return Status{}, fmt.Errorf("today signed: %w", err)
	}
	return Status{SignedDays: days, TodaySigned: signed}, nil
}
