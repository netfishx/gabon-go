package wallet

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5"

	"github.com/netfishx/gabon-go/internal/apierr"
	"github.com/netfishx/gabon-go/internal/db"
)

func insufficient() *apierr.Error {
	return apierr.New(http.StatusConflict, apierr.CodeWalletInsufficientBalance, "insufficient balance")
}

// DebitParams 扣减参数。RefID 可空（如 VIP 购买指向购买记录）。
type DebitParams struct {
	CustomerID int64
	Type       db.TransactionType
	Amount     int64 // 必须 > 0
	RefID      *int64
}

// Debit 在自管事务内扣减可用余额。
func (s *Service) Debit(ctx context.Context, p DebitParams) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		return s.DebitTx(ctx, tx, p)
	})
}

// DebitTx 原子条件扣减（WHERE available >= amount，数据库层杜绝超扣），同事务落负金额流水。
func (s *Service) DebitTx(ctx context.Context, tx pgx.Tx, p DebitParams) error {
	if p.Amount <= 0 {
		return fmt.Errorf("wallet: debit amount must be positive, got %d", p.Amount)
	}
	q := s.q.WithTx(tx)

	w, err := q.DebitWallet(ctx, db.DebitWalletParams{
		CustomerID: p.CustomerID, Available: p.Amount,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return insufficient()
	}
	if err != nil {
		return fmt.Errorf("debit wallet: %w", err)
	}
	if _, err := q.InsertTransaction(ctx, db.InsertTransactionParams{
		CustomerID:   p.CustomerID,
		Type:         p.Type,
		Amount:       -p.Amount,
		BalanceAfter: w.Available + w.Frozen,
		RefID:        p.RefID,
	}); err != nil {
		return fmt.Errorf("insert transaction: %w", err)
	}
	return nil
}

// Freeze 可用→冻结的内部转移：总额不变，不写流水（ADR-0006）。
func (s *Service) Freeze(ctx context.Context, customerID, amount int64) error {
	return transfer(amount, func() (int64, error) {
		return s.q.FreezeWallet(ctx, db.FreezeWalletParams{CustomerID: customerID, Available: amount})
	})
}

// Unfreeze 冻结→可用的内部转移：总额不变，不写流水。
func (s *Service) Unfreeze(ctx context.Context, customerID, amount int64) error {
	return transfer(amount, func() (int64, error) {
		return s.q.UnfreezeWallet(ctx, db.UnfreezeWalletParams{CustomerID: customerID, Available: amount})
	})
}

func transfer(amount int64, op func() (int64, error)) error {
	if amount <= 0 {
		return fmt.Errorf("wallet: transfer amount must be positive, got %d", amount)
	}
	rows, err := op()
	if err != nil {
		return fmt.Errorf("wallet transfer: %w", err)
	}
	if rows == 0 {
		return insufficient()
	}
	return nil
}

// SettleParams 冻结结算参数（提现打款成功时的真正扣出）。
type SettleParams struct {
	CustomerID int64
	Type       db.TransactionType
	Amount     int64 // 必须 > 0
	RefID      *int64
}

// SettleFrozen 在自管事务内结算冻结余额：总额减少，同事务落负金额流水。
func (s *Service) SettleFrozen(ctx context.Context, p SettleParams) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		return s.SettleFrozenTx(ctx, tx, p)
	})
}

// SettleFrozenTx 供消费方（提现审核流）在自身事务内调用。
func (s *Service) SettleFrozenTx(ctx context.Context, tx pgx.Tx, p SettleParams) error {
	if p.Amount <= 0 {
		return fmt.Errorf("wallet: settle amount must be positive, got %d", p.Amount)
	}
	q := s.q.WithTx(tx)

	w, err := q.SettleFrozenWallet(ctx, db.SettleFrozenWalletParams{
		CustomerID: p.CustomerID, Frozen: p.Amount,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return insufficient()
	}
	if err != nil {
		return fmt.Errorf("settle frozen: %w", err)
	}
	if _, err := q.InsertTransaction(ctx, db.InsertTransactionParams{
		CustomerID:   p.CustomerID,
		Type:         p.Type,
		Amount:       -p.Amount,
		BalanceAfter: w.Available + w.Frozen,
		RefID:        p.RefID,
	}); err != nil {
		return fmt.Errorf("insert transaction: %w", err)
	}
	return nil
}
