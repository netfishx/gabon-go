package video

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/netfishx/gabon-go/internal/db"
	"github.com/netfishx/gabon-go/internal/shortcode"
)

// Feed 默认流：seed 为空则新发一个（下拉刷新换序），翻页沿用同一 seed 保证稳定不重不漏。
func (s *Service) Feed(ctx context.Context, seed, cursor string, limit int32) (rows []db.ListFeedVideosRow, outSeed, next string, err error) {
	if seed == "" {
		seed, err = shortcode.New(shortcode.Base58, 16)
		if err != nil {
			return nil, "", "", err
		}
	}
	rows, err = s.q.ListFeedVideos(ctx, db.ListFeedVideosParams{
		Seed: seed, Cursor: cursor, RowLimit: limit + 1,
	})
	if err != nil {
		return nil, "", "", fmt.Errorf("list feed: %w", err)
	}
	if len(rows) > int(limit) {
		rows = rows[:limit]
		next = rows[len(rows)-1].FeedRank
	}
	return rows, seed, next, nil
}

// Featured 精选流：热度分降序 keyset 游标。
func (s *Service) Featured(ctx context.Context, cursorScore, cursorID int64, limit int32) (rows []db.ListFeaturedVideosRow, nextScore, nextID int64, err error) {
	rows, err = s.q.ListFeaturedVideos(ctx, db.ListFeaturedVideosParams{
		CursorScore: cursorScore, CursorID: cursorID, RowLimit: limit + 1,
	})
	if err != nil {
		return nil, 0, 0, fmt.Errorf("list featured: %w", err)
	}
	if len(rows) > int(limit) {
		rows = rows[:limit]
		last := rows[len(rows)-1]
		nextScore, nextID = last.Video.HotScore, last.Video.ID
	}
	return rows, nextScore, nextID, nil
}

// DetailPublished 公开详情：仅已发布且未删除。
func (s *Service) DetailPublished(ctx context.Context, publicID string) (*db.GetPublishedVideoByPublicIDRow, error) {
	row, err := s.q.GetPublishedVideoByPublicID(ctx, publicID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, notFound()
	}
	if err != nil {
		return nil, fmt.Errorf("get published video: %w", err)
	}
	return &row, nil
}

// PublishedByCustomer 他人主页作品：仅已发布。
func (s *Service) PublishedByCustomer(ctx context.Context, customerID, cursor int64, limit int32) (rows []db.ListCustomerPublishedVideosRow, next int64, err error) {
	rows, err = s.q.ListCustomerPublishedVideos(ctx, db.ListCustomerPublishedVideosParams{
		CustomerID: customerID, Cursor: cursor, RowLimit: limit + 1,
	})
	if err != nil {
		return nil, 0, fmt.Errorf("list customer videos: %w", err)
	}
	if len(rows) > int(limit) {
		rows = rows[:limit]
		next = rows[len(rows)-1].Video.ID
	}
	return rows, next, nil
}

// Mine 我的作品：含全部状态（未过审/被驳回可见）。
func (s *Service) Mine(ctx context.Context, customerID, cursor int64, limit int32) (items []db.Video, next int64, err error) {
	items, err = s.q.ListMyVideos(ctx, db.ListMyVideosParams{
		CustomerID: customerID, Cursor: cursor, RowLimit: limit + 1,
	})
	if err != nil {
		return nil, 0, fmt.Errorf("list my videos: %w", err)
	}
	if len(items) > int(limit) {
		items = items[:limit]
		next = items[len(items)-1].ID
	}
	return items, next, nil
}

// Delete 软删本人视频；他人视频与已删视频一律 404（不泄露存在性）。
func (s *Service) Delete(ctx context.Context, customerID int64, publicID string) error {
	v, err := s.getByPublicID(ctx, publicID)
	if err != nil {
		return err
	}
	rows, err := s.q.SoftDeleteVideo(ctx, db.SoftDeleteVideoParams{ID: v.ID, CustomerID: customerID})
	if err != nil {
		return fmt.Errorf("soft delete video: %w", err)
	}
	if rows == 0 {
		return notFound()
	}
	return nil
}
