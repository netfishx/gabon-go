// Package ad 广告域：看广告（随机在投 + 原子扣库存 + 明细）与广告商/广告运营管理。
package ad

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/netfishx/gabon-go/internal/db"
)

// Service 广告域服务。
type Service struct {
	pool *pgxpool.Pool
	q    *db.Queries
}

// NewService 构造广告域服务。
func NewService(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool, q: db.New(pool)}
}

// Served 看广告结果：Ad 为 nil 表示当前无在投广告。
type Served struct {
	Ad *db.PickServingAdRow
}

// Watch 看广告：随机取在投 → 条件 UPDATE 原子扣库存 → ad_watches 落明细，三步同一事务。
// 库存被并发抢空（0 行）则重取；无在投广告返回 Served{Ad: nil}。展示即扣（复刻旧版）。
func (s *Service) Watch(ctx context.Context, customerID int64) (Served, error) {
	const maxRetries = 3
	for range maxRetries {
		var served Served
		err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
			q := s.q.WithTx(tx)
			ad, err := q.PickServingAd(ctx)
			if errors.Is(err, pgx.ErrNoRows) {
				return nil // 无在投广告
			}
			if err != nil {
				return fmt.Errorf("pick serving ad: %w", err)
			}
			rows, err := q.DecrementAdStock(ctx, ad.ID)
			if err != nil {
				return fmt.Errorf("decrement stock: %w", err)
			}
			if rows == 0 {
				return errStockRace // 该广告刚被抢空，跳出事务重取
			}
			if err := q.InsertAdWatch(ctx, db.InsertAdWatchParams{CustomerID: customerID, AdID: ad.ID}); err != nil {
				return fmt.Errorf("insert ad watch: %w", err)
			}
			served.Ad = &ad
			return nil
		})
		if errors.Is(err, errStockRace) {
			continue
		}
		if err != nil {
			return Served{}, err
		}
		return served, nil
	}
	return Served{}, nil // 连续抢空，视为暂无在投（极端并发下的降级）
}

// errStockRace 内部信号：选中广告在扣库存前被并发抢空，需重取。
var errStockRace = errors.New("ad stock race")
