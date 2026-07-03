package wallet

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/netfishx/gabon-go/internal/apierr"
	"github.com/netfishx/gabon-go/internal/db"
)

func credit(t *testing.T, svc *Service, cid, amount int64) {
	t.Helper()
	if err := svc.Credit(context.Background(), CreditParams{
		CustomerID: cid, Type: db.TransactionTypeSignInReward, Amount: amount,
	}); err != nil {
		t.Fatalf("seed credit: %v", err)
	}
}

func TestDebitConcurrentNeverOverdraws(t *testing.T) {
	svc, cid := setup(t)
	ctx := context.Background()
	credit(t, svc, cid, 1000) // 恰好够 10 次 100

	const workers = 25 // 多于余额可承受次数
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- svc.Debit(ctx, DebitParams{
				CustomerID: cid, Type: db.TransactionTypeVipPurchase, Amount: 100,
			})
		}()
	}
	wg.Wait()
	close(errs)

	var ok, insufficient int
	for err := range errs {
		var apiErr *apierr.Error
		switch {
		case err == nil:
			ok++
		case errors.As(err, &apiErr) && apiErr.Code == apierr.CodeWalletInsufficientBalance:
			insufficient++
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if ok != 10 || insufficient != workers-10 {
		t.Errorf("ok = %d, insufficient = %d, want 10 and %d", ok, insufficient, workers-10)
	}
	available, frozen := balances(t, cid)
	if available != 0 || frozen != 0 {
		t.Errorf("balances = (%d, %d), want (0, 0)", available, frozen)
	}
	// 账本：1 笔入账 + 恰好 10 笔扣减
	if rows := ledgerRows(t, cid); len(rows) != 11 {
		t.Errorf("ledger rows = %d, want 11", len(rows))
	}
}

func TestDebitInsufficientRejected(t *testing.T) {
	svc, cid := setup(t)
	credit(t, svc, cid, 50)

	err := svc.Debit(context.Background(), DebitParams{
		CustomerID: cid, Type: db.TransactionTypeVipPurchase, Amount: 51,
	})
	var apiErr *apierr.Error
	if !errors.As(err, &apiErr) || apiErr.Code != apierr.CodeWalletInsufficientBalance {
		t.Fatalf("err = %v, want WALLET_INSUFFICIENT_BALANCE", err)
	}
	if available, _ := balances(t, cid); available != 50 {
		t.Errorf("available = %d, want 50 (untouched)", available)
	}
	if rows := ledgerRows(t, cid); len(rows) != 1 {
		t.Errorf("ledger rows = %d, want 1 (no debit row)", len(rows))
	}
}

func TestFreezeUnfreezeKeepsTotalAndSilent(t *testing.T) {
	svc, cid := setup(t)
	ctx := context.Background()
	credit(t, svc, cid, 500)

	if err := svc.Freeze(ctx, cid, 200); err != nil {
		t.Fatalf("freeze: %v", err)
	}
	available, frozen := balances(t, cid)
	if available != 300 || frozen != 200 {
		t.Fatalf("after freeze = (%d, %d), want (300, 200)", available, frozen)
	}

	// 超额冻结 / 超额解冻被拒
	if err := svc.Freeze(ctx, cid, 301); !isInsufficient(err) {
		t.Errorf("over-freeze err = %v, want insufficient", err)
	}
	if err := svc.Unfreeze(ctx, cid, 201); !isInsufficient(err) {
		t.Errorf("over-unfreeze err = %v, want insufficient", err)
	}

	if err := svc.Unfreeze(ctx, cid, 200); err != nil {
		t.Fatalf("unfreeze: %v", err)
	}
	available, frozen = balances(t, cid)
	if available != 500 || frozen != 0 {
		t.Errorf("after unfreeze = (%d, %d), want (500, 0)", available, frozen)
	}

	// 冻结/解冻全程不产生流水
	if rows := ledgerRows(t, cid); len(rows) != 1 {
		t.Errorf("ledger rows = %d, want 1 (freeze/unfreeze silent)", len(rows))
	}
}

func TestSettleFrozenWritesLedger(t *testing.T) {
	svc, cid := setup(t)
	ctx := context.Background()
	credit(t, svc, cid, 500)
	if err := svc.Freeze(ctx, cid, 200); err != nil {
		t.Fatalf("freeze: %v", err)
	}

	ref := int64(9001)
	if err := svc.SettleFrozen(ctx, SettleParams{
		CustomerID: cid, Type: db.TransactionTypeWithdrawal, Amount: 200, RefID: &ref,
	}); err != nil {
		t.Fatalf("settle: %v", err)
	}

	available, frozen := balances(t, cid)
	if available != 300 || frozen != 0 {
		t.Errorf("after settle = (%d, %d), want (300, 0)", available, frozen)
	}
	rows := ledgerRows(t, cid)
	if len(rows) != 2 {
		t.Fatalf("ledger rows = %d, want 2", len(rows))
	}
	last := rows[len(rows)-1]
	if last.Amount != -200 || last.BalanceAfter != 300 {
		t.Errorf("settle row = %+v, want amount -200 balance_after 300", last)
	}

	// 超额结算被拒
	if err := svc.SettleFrozen(ctx, SettleParams{
		CustomerID: cid, Type: db.TransactionTypeWithdrawal, Amount: 1,
	}); !isInsufficient(err) {
		t.Errorf("over-settle err = %v, want insufficient", err)
	}
}

func isInsufficient(err error) bool {
	var apiErr *apierr.Error
	return errors.As(err, &apiErr) && apiErr.Code == apierr.CodeWalletInsufficientBalance
}
