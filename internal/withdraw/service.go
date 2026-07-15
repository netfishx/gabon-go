// Package withdraw 提现建单与客户订单查询。
package withdraw

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/netfishx/gabon-go/internal/db"
	"github.com/netfishx/gabon-go/internal/wallet"
)

// PayeeSnapshot 是提现建单时固化的收款人信息。
type PayeeSnapshot struct {
	Account  string
	Name     string
	Bank     string
	BankCode *string
	Province *string
	City     *string
}

// CreateParams 提现建单参数。
type CreateParams struct {
	Amount     int64
	BankCardID int64
	Payee      PayeeSnapshot
}

// Service 提现域服务；银行卡与密码检查由 handler 编排后传入快照。
type Service struct {
	pool    *pgxpool.Pool
	q       *db.Queries
	wallets *wallet.Service
}

// NewService 构造提现域服务。
func NewService(pool *pgxpool.Pool, wallets *wallet.Service) *Service {
	return &Service{pool: pool, q: db.New(pool), wallets: wallets}
}

// CreateOrder 同一事务内冻结钻石并建单；任一步失败均整体回滚。
func (s *Service) CreateOrder(ctx context.Context, customerID int64, p CreateParams) (db.WithdrawalOrder, error) {
	var order db.WithdrawalOrder
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if err := s.wallets.FreezeTx(ctx, tx, customerID, p.Amount); err != nil {
			return err
		}
		q := s.q.WithTx(tx)
		bankCardID := p.BankCardID
		bank := p.Payee.Bank
		id, err := q.InsertWithdrawalOrder(ctx, db.InsertWithdrawalOrderParams{
			CustomerID:    customerID,
			Amount:        p.Amount,
			FiatAmount:    p.Amount,
			BankCardID:    &bankCardID,
			PayeeAccount:  p.Payee.Account,
			PayeeName:     p.Payee.Name,
			PayeeBank:     &bank,
			PayeeBankCode: p.Payee.BankCode,
			PayeeProvince: p.Payee.Province,
			PayeeCity:     p.Payee.City,
		})
		if err != nil {
			return fmt.Errorf("insert withdrawal order: %w", err)
		}
		order, err = q.FinalizeWithdrawalOrderNo(ctx, id)
		if err != nil {
			return fmt.Errorf("finalize withdrawal order_no: %w", err)
		}
		return nil
	})
	if err != nil {
		return db.WithdrawalOrder{}, err
	}
	return order, nil
}

// List 按订单 ID 降序游标分页，next=0 表示没有更多。
func (s *Service) List(ctx context.Context, customerID, cursor int64, limit int32) (items []db.WithdrawalOrder, next int64, err error) {
	items, err = s.q.ListWithdrawalOrders(ctx, db.ListWithdrawalOrdersParams{
		CustomerID: customerID, Cursor: cursor, RowLimit: limit + 1,
	})
	if err != nil {
		return nil, 0, fmt.Errorf("list withdrawal orders: %w", err)
	}
	if len(items) > int(limit) {
		items = items[:limit]
		next = items[len(items)-1].ID
	}
	return items, next, nil
}
