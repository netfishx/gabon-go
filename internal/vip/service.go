// Package vip VIP 域：钻石购买等级升级（只升不降、可跳级、永久无到期）。
package vip

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/netfishx/gabon-go/internal/apierr"
	"github.com/netfishx/gabon-go/internal/db"
	"github.com/netfishx/gabon-go/internal/wallet"
)

// Service VIP 域服务。
type Service struct {
	pool    *pgxpool.Pool
	q       *db.Queries
	wallets *wallet.Service
}

// NewService 构造 VIP 域服务。
func NewService(pool *pgxpool.Pool, wallets *wallet.Service) *Service {
	return &Service{pool: pool, q: db.New(pool), wallets: wallets}
}

// Purchase 购买目标 VIP 档：全价扣钻 + 只升级 CAS + 落购买记录 + 流水，同一事务原子完成。
// 降级/平级/并发重复由 CAS 拦截（0 行返冲突）；余额不足由 DebitTx 拦截并整体回滚。
func (s *Service) Purchase(ctx context.Context, customerID int64, toLevel int32) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		// 价格在事务内读，避免 TOCTOU：成交价与扣款额取同一快照
		cfg, err := q.GetVipLevelConfig(ctx, toLevel)
		if errors.Is(err, pgx.ErrNoRows) {
			return apierr.InvalidArgument("unknown vip level")
		}
		if err != nil {
			return fmt.Errorf("get vip config: %w", err)
		}
		fromLevel, err := q.UpgradeVipLevel(ctx, db.UpgradeVipLevelParams{ID: customerID, ToLevel: toLevel})
		if errors.Is(err, pgx.ErrNoRows) {
			return apierr.New(http.StatusConflict, apierr.CodeVipNotUpgrade, "target level is not an upgrade")
		}
		if err != nil {
			return fmt.Errorf("upgrade vip level: %w", err)
		}
		purchase, err := q.InsertVipPurchase(ctx, db.InsertVipPurchaseParams{
			CustomerID: customerID, FromLevel: fromLevel, ToLevel: toLevel, Price: cfg.Price,
		})
		if err != nil {
			return fmt.Errorf("insert vip purchase: %w", err)
		}
		if cfg.Price == 0 {
			return nil // 免费/促销档（price=0）无需扣钻——CAS 已确保是升级
		}
		return s.wallets.DebitTx(ctx, tx, wallet.DebitParams{
			CustomerID: customerID, Type: db.TransactionTypeVipPurchase,
			Amount: cfg.Price, RefID: &purchase.ID,
		})
	})
}

// Levels 返回全部 VIP 档位配置。
func (s *Service) Levels(ctx context.Context) ([]db.VipLevelConfig, error) {
	rows, err := s.q.ListVipLevelConfigs(ctx)
	if err != nil {
		return nil, fmt.Errorf("list vip configs: %w", err)
	}
	return rows, nil
}
