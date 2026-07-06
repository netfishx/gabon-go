package video

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5"

	"github.com/netfishx/gabon-go/internal/apierr"
	"github.com/netfishx/gabon-go/internal/db"
)

func notFound() *apierr.Error {
	return apierr.New(http.StatusNotFound, apierr.CodeVideoNotFound, "video not found")
}

func notReviewable() *apierr.Error {
	return apierr.New(http.StatusConflict, apierr.CodeVideoNotReviewable, "video is not pending review")
}

// ListPendingReview 待审核视频（审核先进先出，id 升序游标）。
func (s *Service) ListPendingReview(ctx context.Context, cursor int64, limit int32) (items []db.Video, next int64, err error) {
	items, err = s.q.ListPendingReviewVideos(ctx, db.ListPendingReviewVideosParams{
		Cursor: cursor, RowLimit: limit + 1,
	})
	if err != nil {
		return nil, 0, fmt.Errorf("list pending review: %w", err)
	}
	if len(items) > int(limit) {
		items = items[:limit]
		next = items[len(items)-1].ID
	}
	return items, next, nil
}

// Approve 审核通过：视频翻 published、作者 video_count+1 与 OnApproved 钩子同一事务。
// video_count 是有效用户判定"有作品"的输入。
func (s *Service) Approve(ctx context.Context, adminID int64, publicID string) error {
	v, err := s.getByPublicID(ctx, publicID)
	if err != nil {
		return err
	}
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		rows, err := q.ApproveVideo(ctx, db.ApproveVideoParams{ID: v.ID, ReviewedBy: &adminID})
		if err != nil {
			return err
		}
		if rows == 0 {
			return notReviewable()
		}
		if err := q.IncrementVideoCount(ctx, v.CustomerID); err != nil {
			return err
		}
		if s.OnApproved != nil {
			return s.OnApproved(ctx, tx, v.CustomerID)
		}
		return nil
	})
}

// Reject 驳回：必填原因。
func (s *Service) Reject(ctx context.Context, adminID int64, publicID, reason string) error {
	if reason == "" {
		return apierr.InvalidArgument("reason is required")
	}
	v, err := s.getByPublicID(ctx, publicID)
	if err != nil {
		return err
	}
	rows, err := s.q.RejectVideo(ctx, db.RejectVideoParams{ID: v.ID, ReviewedBy: &adminID, ReviewNotes: &reason})
	if err != nil {
		return fmt.Errorf("reject video: %w", err)
	}
	if rows == 0 {
		return notReviewable()
	}
	return nil
}

func (s *Service) getByPublicID(ctx context.Context, publicID string) (*db.Video, error) {
	v, err := s.q.GetVideoByPublicID(ctx, publicID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, notFound()
	}
	if err != nil {
		return nil, fmt.Errorf("get video by public id: %w", err)
	}
	return &v, nil
}
