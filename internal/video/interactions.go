package video

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/netfishx/gabon-go/internal/apierr"
	"github.com/netfishx/gabon-go/internal/db"
	"github.com/netfishx/gabon-go/internal/tz"
)

const maxCommentLen = 500

// 热度权重（CONTEXT.md 热度分：只增不减）
const (
	hotClick   = 1
	hotValid   = 1
	hotLike    = 2
	hotComment = 5
)

// publishedByPublicID 互动入口守卫：仅已发布且未删除的视频可互动。
func (s *Service) publishedByPublicID(ctx context.Context, publicID string) (*db.Video, error) {
	v, err := s.getByPublicID(ctx, publicID)
	if err != nil {
		return nil, err
	}
	if v.Status != db.VideoStatusPublished {
		return nil, notFound()
	}
	return v, nil
}

// Like 点赞：软删复用行——仅首次 INSERT 计热度（+2），复活只恢复计数，重复点赞幂等。
// counted 仅首次 INSERT 为 true：任务进度与热度同口径（终身一行只计一次）。
func (s *Service) Like(ctx context.Context, customerID int64, publicID string) (counted bool, err error) {
	v, err := s.publishedByPublicID(ctx, publicID)
	if err != nil {
		return false, err
	}
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		inserted, err := q.InsertLike(ctx, db.InsertLikeParams{CustomerID: customerID, VideoID: v.ID})
		if err != nil {
			return err
		}
		if inserted == 1 {
			counted = true
			return q.BumpVideoCounters(ctx, bump(v.ID, db.BumpVideoCountersParams{LikeDelta: 1, HotDelta: hotLike}))
		}
		revived, err := q.ReviveLike(ctx, db.ReviveLikeParams{CustomerID: customerID, VideoID: v.ID})
		if err != nil {
			return err
		}
		if revived == 1 {
			return q.BumpVideoCounters(ctx, bump(v.ID, db.BumpVideoCountersParams{LikeDelta: 1}))
		}
		return nil // 已是点赞状态，幂等
	})
	if err != nil {
		return false, err
	}
	return counted, nil
}

// Unlike 取消点赞：软删，计数 -1，热度不减。
func (s *Service) Unlike(ctx context.Context, customerID int64, publicID string) error {
	v, err := s.publishedByPublicID(ctx, publicID)
	if err != nil {
		return err
	}
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		removed, err := q.SoftDeleteLike(ctx, db.SoftDeleteLikeParams{CustomerID: customerID, VideoID: v.ID})
		if err != nil {
			return err
		}
		if removed == 1 {
			return q.BumpVideoCounters(ctx, bump(v.ID, db.BumpVideoCountersParams{LikeDelta: -1}))
		}
		return nil
	})
}

// Comment 评论：每人每视频每日一条（Asia/Shanghai 记日，唯一约束含软删行——删后当日不可再评）。
func (s *Service) Comment(ctx context.Context, customerID int64, publicID, content string) (*db.Comment, error) {
	if content == "" || len(content) > maxCommentLen {
		return nil, apierr.InvalidArgument("content must be 1-500 bytes")
	}
	v, err := s.publishedByPublicID(ctx, publicID)
	if err != nil {
		return nil, err
	}
	var created db.Comment
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		c, err := q.InsertComment(ctx, db.InsertCommentParams{
			VideoID:     v.ID,
			CustomerID:  customerID,
			Content:     content,
			CommentDate: pgtype.Date{Time: tz.Today(), Valid: true},
		})
		if err != nil {
			return err
		}
		if err := q.BumpVideoCounters(ctx, bump(v.ID, db.BumpVideoCountersParams{CommentDelta: 1, HotDelta: hotComment})); err != nil {
			return err
		}
		created = c
		return nil
	})
	if db.UniqueViolationConstraint(err) == "comments_daily_key" {
		return nil, apierr.New(http.StatusConflict, apierr.CodeVideoCommentDailyLimit, "one comment per video per day")
	}
	if err != nil {
		return nil, fmt.Errorf("comment: %w", err)
	}
	return &created, nil
}

// DeleteComment 删除本人评论：计数 -1，热度不变；当日仍不可再评（约束含软删行）。
func (s *Service) DeleteComment(ctx context.Context, customerID, commentID int64) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		videoID, err := q.SoftDeleteComment(ctx, db.SoftDeleteCommentParams{ID: commentID, CustomerID: customerID})
		if errors.Is(err, pgx.ErrNoRows) {
			return apierr.New(http.StatusNotFound, apierr.CodeVideoCommentNotFound, "comment not found")
		}
		if err != nil {
			return err
		}
		return q.BumpVideoCounters(ctx, bump(videoID, db.BumpVideoCountersParams{CommentDelta: -1}))
	})
}

// Comments 评论列表（公开，仅存活评论）。
func (s *Service) Comments(ctx context.Context, publicID string, cursor int64, limit int32) (rows []db.ListVideoCommentsRow, next int64, err error) {
	v, err := s.publishedByPublicID(ctx, publicID)
	if err != nil {
		return nil, 0, err
	}
	rows, err = s.q.ListVideoComments(ctx, db.ListVideoCommentsParams{
		VideoID: v.ID, Cursor: cursor, RowLimit: limit + 1,
	})
	if err != nil {
		return nil, 0, fmt.Errorf("list comments: %w", err)
	}
	if len(rows) > int(limit) {
		rows = rows[:limit]
		next = rows[len(rows)-1].ID
	}
	return rows, next, nil
}

// Play 播放点击：不去重，每次 +1；返回事件 id 供达标后上报有效播放（也是 M5 观看奖励的流水 ref）。
func (s *Service) Play(ctx context.Context, customerID int64, publicID string) (playID int64, err error) {
	v, err := s.publishedByPublicID(ctx, publicID)
	if err != nil {
		return 0, err
	}
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		p, err := q.InsertPlay(ctx, db.InsertPlayParams{CustomerID: customerID, VideoID: v.ID})
		if err != nil {
			return err
		}
		playID = p.ID
		return q.BumpVideoCounters(ctx, bump(v.ID, db.BumpVideoCountersParams{ClickDelta: 1, HotDelta: hotClick}))
	})
	if err != nil {
		return 0, fmt.Errorf("record play: %w", err)
	}
	return playID, nil
}

// MarkValid 有效播放：同一事件 id 只计一次（条件 UPDATE），重复上报与他人事件均幂等静默。
// marked 仅首次标记为 true，并回传 videoID（任务进度事件源：仅首次推进，防刷主体为视频）。
func (s *Service) MarkValid(ctx context.Context, customerID, playID int64) (videoID int64, marked bool, err error) {
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		vid, err := q.MarkPlayValid(ctx, db.MarkPlayValidParams{ID: playID, CustomerID: customerID})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil // 已标记 / 不存在 / 非本人：幂等静默，不泄露存在性
		}
		if err != nil {
			return err
		}
		videoID, marked = vid, true
		return q.BumpVideoCounters(ctx, bump(vid, db.BumpVideoCountersParams{ValidDelta: 1, HotDelta: hotValid}))
	})
	if err != nil {
		return 0, false, fmt.Errorf("mark valid play: %w", err)
	}
	return videoID, marked, nil
}

func bump(videoID int64, p db.BumpVideoCountersParams) db.BumpVideoCountersParams {
	p.ID = videoID
	return p
}
