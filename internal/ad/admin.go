package ad

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5"

	"github.com/netfishx/gabon-go/internal/apierr"
	"github.com/netfishx/gabon-go/internal/db"
)

func adNotFound() *apierr.Error {
	return apierr.New(http.StatusNotFound, apierr.CodeAdNotFound, "ad or advertiser not found")
}

// ---- 广告商 ----

// CreateAdvertiser 新增广告商。
func (s *Service) CreateAdvertiser(ctx context.Context, name string, contact *string) (db.Advertiser, error) {
	a, err := s.q.CreateAdvertiser(ctx, db.CreateAdvertiserParams{Name: name, Contact: contact})
	if err != nil {
		return db.Advertiser{}, fmt.Errorf("create advertiser: %w", err)
	}
	return a, nil
}

// ListAdvertisers 后台广告商列表。
func (s *Service) ListAdvertisers(ctx context.Context) ([]db.Advertiser, error) {
	rows, err := s.q.ListAdvertisers(ctx)
	if err != nil {
		return nil, fmt.Errorf("list advertisers: %w", err)
	}
	return rows, nil
}

// UpdateAdvertiser 部分更新广告商。
func (s *Service) UpdateAdvertiser(ctx context.Context, p db.UpdateAdvertiserParams) (db.Advertiser, error) {
	a, err := s.q.UpdateAdvertiser(ctx, p)
	if errors.Is(err, pgx.ErrNoRows) {
		return db.Advertiser{}, adNotFound()
	}
	if err != nil {
		return db.Advertiser{}, fmt.Errorf("update advertiser: %w", err)
	}
	return a, nil
}

// SoftDeleteAdvertiser 软删广告商并同事务下架名下广告（服务 JOIN 已按 deleted_at 过滤，级联保列表一致）。
func (s *Service) SoftDeleteAdvertiser(ctx context.Context, advertiserID int64) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		rows, err := q.SoftDeleteAdvertiser(ctx, advertiserID)
		if err != nil {
			return fmt.Errorf("soft delete advertiser: %w", err)
		}
		if rows == 0 {
			return adNotFound()
		}
		return q.CascadeOfflineAds(ctx, advertiserID)
	})
}

// SetAdvertiserStatus 上下架广告商；下架时同事务单向写级联下架名下广告（重开不反向恢复）。
func (s *Service) SetAdvertiserStatus(ctx context.Context, advertiserID int64, active bool) error {
	status := db.AdStatusActive
	if !active {
		status = db.AdStatusOffline
	}
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		rows, err := q.SetAdvertiserStatus(ctx, db.SetAdvertiserStatusParams{ID: advertiserID, Status: status})
		if err != nil {
			return fmt.Errorf("set advertiser status: %w", err)
		}
		if rows == 0 {
			return adNotFound()
		}
		if !active {
			return q.CascadeOfflineAds(ctx, advertiserID)
		}
		return nil
	})
}

// ---- 广告 ----

// CreateAd 新增广告；广告商不存在（FK 违例）映射为参数错误而非 500。
func (s *Service) CreateAd(ctx context.Context, p db.CreateAdParams) (db.Ad, error) {
	a, err := s.q.CreateAd(ctx, p)
	if db.IsForeignKeyViolation(err) {
		return db.Ad{}, apierr.InvalidArgument("advertiser does not exist")
	}
	if err != nil {
		return db.Ad{}, fmt.Errorf("create ad: %w", err)
	}
	return a, nil
}

// ListAds 后台广告列表。
func (s *Service) ListAds(ctx context.Context) ([]db.Ad, error) {
	rows, err := s.q.ListAds(ctx)
	if err != nil {
		return nil, fmt.Errorf("list ads: %w", err)
	}
	return rows, nil
}

// UpdateAd 部分更新广告。
func (s *Service) UpdateAd(ctx context.Context, p db.UpdateAdParams) (db.Ad, error) {
	a, err := s.q.UpdateAd(ctx, p)
	if errors.Is(err, pgx.ErrNoRows) {
		return db.Ad{}, adNotFound()
	}
	if err != nil {
		return db.Ad{}, fmt.Errorf("update ad: %w", err)
	}
	return a, nil
}

// SetAdStatus 广告上下架。
func (s *Service) SetAdStatus(ctx context.Context, adID int64, active bool) error {
	status := db.AdStatusActive
	if !active {
		status = db.AdStatusOffline
	}
	rows, err := s.q.SetAdStatus(ctx, db.SetAdStatusParams{ID: adID, Status: status})
	if err != nil {
		return fmt.Errorf("set ad status: %w", err)
	}
	if rows == 0 {
		return adNotFound()
	}
	return nil
}

// SoftDeleteAd 软删广告。
func (s *Service) SoftDeleteAd(ctx context.Context, adID int64) error {
	rows, err := s.q.SoftDeleteAd(ctx, adID)
	if err != nil {
		return fmt.Errorf("soft delete ad: %w", err)
	}
	if rows == 0 {
		return adNotFound()
	}
	return nil
}
