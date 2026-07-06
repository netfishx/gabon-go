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

// SignIn 每日打卡（签到日 = 当前 Asia/Shanghai 日期）。
func (s *Service) SignIn(ctx context.Context, customerID int64) error {
	now := time.Now().In(tz.Shanghai)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, tz.Shanghai)
	return s.signInAt(ctx, customerID, today)
}

// signInAt 在指定签到日打卡：唯一约束防重签；日签与里程碑各自 floor 放大、各自入账（拆两类流水）。
// 签到落库 + 达标发奖同一事务原子完成；开头锁客户行串行化同一客户的签到（跨午夜相邻日安全）。
// day 参数化便于确定性测试相邻日；生产入口 SignIn 传当日。
func (s *Service) signInAt(ctx context.Context, customerID int64, today time.Time) error {
	monthStart := time.Date(today.Year(), today.Month(), 1, 0, 0, 0, 0, tz.Shanghai)

	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)

		// 锁客户行串行化同一客户的签到事务：保证相邻日跨午夜并发时月累计计数视图一致
		bp, err := q.LockCustomerForSignIn(ctx, customerID)
		if err != nil {
			return fmt.Errorf("lock customer: %w", err)
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
	// 同月同档只发一次由唯一约束保证；客户行锁已串行化本客户签到，正常流不可能撞约束，
	// 若真撞（数据异常）则原样上抛令事务整体回滚，不吞。
	award, err := q.InsertMilestoneAward(ctx, db.InsertMilestoneAwardParams{
		CustomerID: customerID, Month: pgDate(monthStart), Threshold: threshold, RewardAmount: amount,
	})
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

// Status 本月签到状态：已签天数、今日是否已签、下一里程碑档位（nil = 无更高档位）。
type Status struct {
	SignedDays    int64
	TodaySigned   bool
	NextMilestone *int32 // 大于当前已签天数的最近里程碑档位
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
	st := Status{SignedDays: days, TodaySigned: signed}
	next, err := s.q.NextMilestoneThreshold(ctx, int32(days)) //nolint:gosec // 自然月签到天数 ≤31
	if errors.Is(err, pgx.ErrNoRows) {
		return st, nil // 无更高里程碑档位
	}
	if err != nil {
		return Status{}, fmt.Errorf("next milestone: %w", err)
	}
	st.NextMilestone = &next
	return st, nil
}
