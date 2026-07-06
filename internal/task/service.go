// Package task 任务域：周期任务进度推进与达标发奖、限时领取任务（骨架中可被其他域依赖的两域之一）。
package task

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/netfishx/gabon-go/internal/db"
	"github.com/netfishx/gabon-go/internal/tz"
	"github.com/netfishx/gabon-go/internal/wallet"
)

// Service 任务域服务。
type Service struct {
	pool    *pgxpool.Pool
	q       *db.Queries
	wallets *wallet.Service
}

// NewService 构造任务域服务。
func NewService(pool *pgxpool.Pool, wallets *wallet.Service) *Service {
	return &Service{pool: pool, q: db.New(pool), wallets: wallets}
}

// Advance 推进某客户在该类别下全部启用任务的当期进度。
// 独立事务自管：调用方（客户面编排层）须在主事件事务提交后调用，
// 失败由调用方记日志、不回传主链路（PRD #45 架构）。
// refID：watch_video 传有效播放事件 id（防刷判定用），其余类别忽略。
func (s *Service) Advance(ctx context.Context, customerID int64, category db.TaskCategory, refID int64) error {
	tasks, err := s.q.ListEnabledPeriodicTasksByCategory(ctx, category)
	if err != nil {
		return fmt.Errorf("list tasks by category: %w", err)
	}
	now := time.Now().In(tz.Shanghai)
	for _, t := range tasks {
		if err := s.advanceOne(ctx, customerID, t, refID, now); err != nil {
			return fmt.Errorf("advance task %d: %w", t.ID, err)
		}
	}
	return nil
}

// advanceOne 单任务推进：进度 UPSERT + 达标翻转 + 发奖同一事务原子完成。
// 幂等三层：进度行唯一键 / reward_granted_at 条件翻转 / 流水 (type, ref) 唯一约束。
func (s *Service) advanceOne(ctx context.Context, customerID int64, t db.PeriodicTask, refID int64, now time.Time) error {
	key, start := periodOf(t.Period, now)

	// watch 防刷：每客户×视频×周期仅首次有效播放推进（id 最小者胜，并发下恰好一次）
	if t.Category == db.TaskCategoryWatchVideo {
		first, err := s.q.IsFirstValidPlayInPeriod(ctx, db.IsFirstValidPlayInPeriodParams{
			CustomerID:  customerID,
			PlayID:      refID,
			PeriodStart: pgtype.Timestamptz{Time: start, Valid: true},
		})
		if err != nil {
			return fmt.Errorf("watch dedup check: %w", err)
		}
		if !first {
			return nil
		}
	}

	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		row, err := q.UpsertTaskProgress(ctx, db.UpsertTaskProgressParams{
			CustomerID: customerID,
			TaskID:     t.ID,
			PeriodKey:  key,
			Target:     t.Target,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil // 本期已达标：进度封顶，不再累加
		}
		if err != nil {
			return fmt.Errorf("upsert progress: %w", err)
		}
		if row.Progress < row.Target {
			return nil
		}

		bp, err := q.GetCustomerRewardMultiplierBp(ctx, customerID)
		if err != nil {
			return fmt.Errorf("read multiplier: %w", err)
		}
		amount := t.Reward * int64(bp) / 10000 // floor：整数除法即向下取整
		granted, err := q.GrantTaskRewardIfDue(ctx, db.GrantTaskRewardIfDueParams{
			ID: row.ID, RewardAmount: &amount,
		})
		if err != nil {
			return fmt.Errorf("grant flip: %w", err)
		}
		if granted == 0 || amount <= 0 {
			return nil
		}
		return s.wallets.CreditTx(ctx, tx, wallet.CreditParams{
			CustomerID: customerID,
			Type:       db.TransactionTypePeriodicTaskReward,
			Amount:     amount,
			RefID:      &row.ID,
		})
	})
}

// ProgressItem 周期任务列表项：定义 × 当期进度合成。
type ProgressItem struct {
	Task     db.PeriodicTask
	Progress int32
	Granted  bool
}

// ListWithProgress 全部启用任务（display_order 序）及查看者当期进度（无进度行 = 0）。
func (s *Service) ListWithProgress(ctx context.Context, customerID int64) ([]ProgressItem, error) {
	tasks, err := s.q.ListEnabledPeriodicTasks(ctx)
	if err != nil {
		return nil, fmt.Errorf("list tasks: %w", err)
	}
	now := time.Now().In(tz.Shanghai)
	keySet := map[string]bool{}
	currentKey := map[int64]string{}
	for _, t := range tasks {
		key, _ := periodOf(t.Period, now)
		keySet[key] = true
		currentKey[t.ID] = key
	}
	keys := make([]string, 0, len(keySet))
	for k := range keySet {
		keys = append(keys, k)
	}
	rows, err := s.q.ListTaskProgressForKeys(ctx, db.ListTaskProgressForKeysParams{
		CustomerID: customerID, PeriodKeys: keys,
	})
	if err != nil {
		return nil, fmt.Errorf("list progress: %w", err)
	}
	type pk struct {
		taskID int64
		key    string
	}
	byTask := map[pk]db.PeriodicTaskProgress{}
	for _, r := range rows {
		byTask[pk{r.TaskID, r.PeriodKey}] = r
	}
	out := make([]ProgressItem, 0, len(tasks))
	for _, t := range tasks {
		item := ProgressItem{Task: t}
		if r, ok := byTask[pk{t.ID, currentKey[t.ID]}]; ok {
			item.Progress = r.Progress
			item.Granted = r.RewardGrantedAt.Valid
		}
		out = append(out, item)
	}
	return out, nil
}
