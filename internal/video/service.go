// Package video 视频域：上传、转码状态机、审核联动、Feed 与互动。
package video

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/netfishx/gabon-go/internal/apierr"
	"github.com/netfishx/gabon-go/internal/db"
	"github.com/netfishx/gabon-go/internal/shortcode"
	"github.com/netfishx/gabon-go/internal/storage"
)

const (
	maxTags       = 3
	presignExpiry = 15 * time.Minute
	// 原片路径约定：videos/{customerID}/{random}.mp4——归属内嵌，confirm 时校验前缀
	rawPathPattern = "videos/%d/%s.mp4"
)

// Service 视频域服务。
type Service struct {
	pool  *pgxpool.Pool
	q     *db.Queries
	store *storage.Store
}

// NewService 构造视频域服务。
func NewService(pool *pgxpool.Pool, store *storage.Store) *Service {
	return &Service{pool: pool, q: db.New(pool), store: store}
}

// CreateUpload 生成原片预签名 PUT 地址与存储路径。
func (s *Service) CreateUpload(ctx context.Context, customerID int64) (storagePath, uploadURL string, err error) {
	name, err := shortcode.New(shortcode.Base58, 16)
	if err != nil {
		return "", "", err
	}
	storagePath = fmt.Sprintf(rawPathPattern, customerID, name)
	uploadURL, err = s.store.PresignPut(ctx, storagePath, presignExpiry)
	if err != nil {
		return "", "", err
	}
	return storagePath, uploadURL, nil
}

// Confirm 确认上传：校验路径归属与对象存在，同一事务建视频行（待转码）与转码任务（queued）。
func (s *Service) Confirm(ctx context.Context, customerID int64, storagePath, title string, tags []string) (*db.Video, error) {
	if !strings.HasPrefix(storagePath, fmt.Sprintf("videos/%d/", customerID)) {
		return nil, apierr.New(http.StatusForbidden, apierr.CodeVideoPathForbidden, "storage path does not belong to you")
	}
	if title == "" {
		return nil, apierr.InvalidArgument("title is required")
	}
	if len(tags) > maxTags {
		return nil, apierr.InvalidArgument("at most 3 tags")
	}
	exists, err := s.store.Exists(ctx, storagePath)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, apierr.New(http.StatusBadRequest, apierr.CodeVideoObjectMissing, "uploaded object not found")
	}

	publicID, err := shortcode.New(shortcode.Base58, 12)
	if err != nil {
		return nil, err
	}
	if tags == nil {
		tags = []string{}
	}

	var created db.Video
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		v, err := q.CreateVideo(ctx, db.CreateVideoParams{
			PublicID:    publicID,
			CustomerID:  customerID,
			Title:       title,
			Tags:        tags,
			StoragePath: storagePath,
		})
		if err != nil {
			return err
		}
		if _, err := q.CreateTranscodeJob(ctx, v.ID); err != nil {
			return err
		}
		created = v
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("confirm video: %w", err)
	}
	return &created, nil
}
