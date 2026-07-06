package task

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5"

	"github.com/netfishx/gabon-go/internal/apierr"
	"github.com/netfishx/gabon-go/internal/db"
)

func taskNotFound() *apierr.Error {
	return apierr.New(http.StatusNotFound, apierr.CodeClaimTaskNotFound, "task not found")
}

// ---- 周期任务定义 CRUD ----

// CreatePeriodicTask 新增周期任务定义。
func (s *Service) CreatePeriodicTask(ctx context.Context, p db.CreatePeriodicTaskParams) (db.PeriodicTask, error) {
	t, err := s.q.CreatePeriodicTask(ctx, p)
	if err != nil {
		return db.PeriodicTask{}, fmt.Errorf("create periodic task: %w", err)
	}
	return t, nil
}

// ListPeriodicTasksAdmin 后台周期任务定义列表。
func (s *Service) ListPeriodicTasksAdmin(ctx context.Context) ([]db.PeriodicTask, error) {
	rows, err := s.q.ListPeriodicTasksAdmin(ctx)
	if err != nil {
		return nil, fmt.Errorf("list periodic tasks: %w", err)
	}
	return rows, nil
}

// UpdatePeriodicTask 部分更新周期任务定义。
func (s *Service) UpdatePeriodicTask(ctx context.Context, p db.UpdatePeriodicTaskParams) (db.PeriodicTask, error) {
	t, err := s.q.UpdatePeriodicTask(ctx, p)
	if errors.Is(err, pgx.ErrNoRows) {
		return db.PeriodicTask{}, taskNotFound()
	}
	if err != nil {
		return db.PeriodicTask{}, fmt.Errorf("update periodic task: %w", err)
	}
	return t, nil
}

// ---- 限时任务定义 CRUD ----

// CreateClaimTask 新增限时任务定义。
func (s *Service) CreateClaimTask(ctx context.Context, p db.CreateClaimTaskParams) (db.ClaimTask, error) {
	t, err := s.q.CreateClaimTask(ctx, p)
	if err != nil {
		return db.ClaimTask{}, fmt.Errorf("create claim task: %w", err)
	}
	return t, nil
}

// ListClaimTasksAdmin 后台限时任务定义列表。
func (s *Service) ListClaimTasksAdmin(ctx context.Context) ([]db.ClaimTask, error) {
	rows, err := s.q.ListClaimTasksAdmin(ctx)
	if err != nil {
		return nil, fmt.Errorf("list claim tasks: %w", err)
	}
	return rows, nil
}

// UpdateClaimTask 部分更新限时任务定义；改 ends_at 时同事务把未终态在途记录的 expires_at 回写（运营语义）。
func (s *Service) UpdateClaimTask(ctx context.Context, p db.UpdateClaimTaskParams) (db.ClaimTask, error) {
	var updated db.ClaimTask
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		t, err := q.UpdateClaimTask(ctx, p)
		if errors.Is(err, pgx.ErrNoRows) {
			return taskNotFound()
		}
		if err != nil {
			return fmt.Errorf("update claim task: %w", err)
		}
		if p.EndsAt.Valid {
			if err := q.RewriteInflightExpiry(ctx, db.RewriteInflightExpiryParams{
				ExpiresAt: p.EndsAt, TaskID: p.ID,
			}); err != nil {
				return fmt.Errorf("rewrite inflight expiry: %w", err)
			}
		}
		updated = t
		return nil
	})
	return updated, err
}

// SetClaimTaskEnabled 上下架切换。
func (s *Service) SetClaimTaskEnabled(ctx context.Context, taskID int64, enabled bool) error {
	rows, err := s.q.SetClaimTaskEnabled(ctx, db.SetClaimTaskEnabledParams{ID: taskID, Enabled: enabled})
	if err != nil {
		return fmt.Errorf("set claim task enabled: %w", err)
	}
	if rows == 0 {
		return taskNotFound()
	}
	return nil
}

// SoftDeleteClaimTask 软删定义并同事务作废全部未终态在途记录（已发奖终态不动）。
func (s *Service) SoftDeleteClaimTask(ctx context.Context, taskID int64) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		rows, err := q.SoftDeleteClaimTask(ctx, taskID)
		if err != nil {
			return fmt.Errorf("soft delete claim task: %w", err)
		}
		if rows == 0 {
			return taskNotFound()
		}
		return q.VoidInflightClaims(ctx, taskID)
	})
}
