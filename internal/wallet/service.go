// Package wallet 钱包域：唯一资金入口（核心被依赖域）。
// 余额变更一律经由本包原语——原子条件 UPDATE + 同事务纯账本流水（ADR-0006）。
package wallet

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/netfishx/gabon-go/internal/db"
)

// Service 钱包域服务。
type Service struct {
	pool *pgxpool.Pool
	q    *db.Queries
}

// NewService 构造钱包域服务。
func NewService(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool, q: db.New(pool)}
}

// ListTransactions 按流水号降序游标分页。cursor=0 表示第一页；
// 返回的 next=0 表示没有更多。
func (s *Service) ListTransactions(ctx context.Context, customerID, cursor int64, limit int32) (items []db.Transaction, next int64, err error) {
	items, err = s.q.ListTransactions(ctx, db.ListTransactionsParams{
		CustomerID: customerID, Cursor: cursor, RowLimit: limit,
	})
	if err != nil {
		return nil, 0, fmt.Errorf("list transactions: %w", err)
	}
	if int32(len(items)) == limit && len(items) > 0 {
		next = items[len(items)-1].ID
	}
	return items, next, nil
}

// Get 查询客户钱包（注册即建行，缺行即数据异常，直接上抛）。
func (s *Service) Get(ctx context.Context, customerID int64) (*db.Wallet, error) {
	w, err := s.q.GetWallet(ctx, customerID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("wallet missing for customer %d: %w", customerID, err)
	}
	if err != nil {
		return nil, fmt.Errorf("get wallet: %w", err)
	}
	return &w, nil
}
