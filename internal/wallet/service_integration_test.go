package wallet

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/netfishx/gabon-go/internal/db"
	"github.com/netfishx/gabon-go/internal/testdb"
)

// 资金不变量的主战场：真库真并发，服务缝直测（issue #5/#6/#8 验收）。
// 包级共享一个容器（TestMain），每个测试注册独立客户隔离数据。

var testPool *pgxpool.Pool

func TestMain(m *testing.M) {
	ctx := context.Background()
	pool, cleanup, err := testdb.Start(ctx)
	if err != nil {
		log.Printf("wallet integration setup: %v", err)
		os.Exit(1)
	}
	testPool = pool
	code := m.Run()
	cleanup()
	os.Exit(code)
}

var nameSeq atomic.Int64

// setup 直插客户与零余额钱包 fixture。
// 不走 customer 域注册：customer 依赖 wallet 发奖，反向引用会在测试编译时成环。
func setup(t *testing.T) (*Service, int64) {
	t.Helper()
	n := nameSeq.Add(1)
	username := fmt.Sprintf("w%d_%d", os.Getpid()%10000, n)
	var id int64
	if err := testPool.QueryRow(context.Background(),
		`INSERT INTO customers (public_id, username, password_hash, invite_code)
		 VALUES ($1, $2, 'not-a-real-hash', $3) RETURNING id`,
		fmt.Sprintf("wfix%08d", n), username, fmt.Sprintf("WF%06d", n),
	).Scan(&id); err != nil {
		t.Fatalf("stage fixture customer: %v", err)
	}
	if _, err := testPool.Exec(context.Background(),
		`INSERT INTO wallets (customer_id) VALUES ($1)`, id); err != nil {
		t.Fatalf("stage fixture wallet: %v", err)
	}
	return NewService(testPool), id
}

func balances(t *testing.T, customerID int64) (available, frozen int64) {
	t.Helper()
	if err := testPool.QueryRow(
		context.Background(),
		`SELECT available, frozen FROM wallets WHERE customer_id = $1`, customerID,
	).Scan(&available, &frozen); err != nil {
		t.Fatalf("query balances: %v", err)
	}
	return available, frozen
}

func ledgerRows(t *testing.T, customerID int64) []struct {
	Amount, BalanceAfter int64
} {
	t.Helper()
	rows, err := testPool.Query(context.Background(),
		`SELECT amount, balance_after FROM transactions WHERE customer_id = $1 ORDER BY id`, customerID)
	if err != nil {
		t.Fatalf("query ledger: %v", err)
	}
	defer rows.Close()
	var out []struct{ Amount, BalanceAfter int64 }
	for rows.Next() {
		var r struct{ Amount, BalanceAfter int64 }
		if err := rows.Scan(&r.Amount, &r.BalanceAfter); err != nil {
			t.Fatalf("scan ledger: %v", err)
		}
		out = append(out, r)
	}
	return out
}

func TestCreditWritesLedgerAtomically(t *testing.T) {
	svc, cid := setup(t)
	ctx := context.Background()

	if err := svc.Credit(ctx, CreditParams{
		CustomerID: cid, Type: db.TransactionTypeSignInReward, Amount: 500,
	}); err != nil {
		t.Fatalf("credit: %v", err)
	}
	if err := svc.Credit(ctx, CreditParams{
		CustomerID: cid, Type: db.TransactionTypeWatchReward, Amount: 300,
	}); err != nil {
		t.Fatalf("second credit: %v", err)
	}

	available, frozen := balances(t, cid)
	if available != 800 || frozen != 0 {
		t.Errorf("balances = (%d, %d), want (800, 0)", available, frozen)
	}
	rows := ledgerRows(t, cid)
	if len(rows) != 2 {
		t.Fatalf("ledger rows = %d, want 2", len(rows))
	}
	if rows[0].Amount != 500 || rows[0].BalanceAfter != 500 {
		t.Errorf("row0 = %+v, want amount 500 balance_after 500", rows[0])
	}
	if rows[1].Amount != 300 || rows[1].BalanceAfter != 800 {
		t.Errorf("row1 = %+v, want amount 300 balance_after 800", rows[1])
	}
}

func TestCreditIdempotentByRef(t *testing.T) {
	svc, cid := setup(t)
	ctx := context.Background()
	ref := int64(4242)

	if err := svc.Credit(ctx, CreditParams{
		CustomerID: cid, Type: db.TransactionTypeInviteValidReward, Amount: 100, RefID: &ref,
	}); err != nil {
		t.Fatalf("first credit: %v", err)
	}
	err := svc.Credit(ctx, CreditParams{
		CustomerID: cid, Type: db.TransactionTypeInviteValidReward, Amount: 100, RefID: &ref,
	})
	if !errors.Is(err, ErrAlreadyGranted) {
		t.Fatalf("second credit err = %v, want ErrAlreadyGranted", err)
	}

	available, _ := balances(t, cid)
	if available != 100 {
		t.Errorf("available = %d, want 100 (granted exactly once)", available)
	}
	if rows := ledgerRows(t, cid); len(rows) != 1 {
		t.Errorf("ledger rows = %d, want 1", len(rows))
	}
}

func TestCreditConcurrentSameRef(t *testing.T) {
	svc, cid := setup(t)
	ctx := context.Background()
	ref := int64(7777)

	const workers = 10
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- svc.Credit(ctx, CreditParams{
				CustomerID: cid, Type: db.TransactionTypePeriodicTaskReward, Amount: 50, RefID: &ref,
			})
		}()
	}
	wg.Wait()
	close(errs)

	var granted, already int
	for err := range errs {
		switch {
		case err == nil:
			granted++
		case errors.Is(err, ErrAlreadyGranted):
			already++
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if granted != 1 || already != workers-1 {
		t.Errorf("granted = %d, already = %d, want 1 and %d", granted, already, workers-1)
	}
	available, _ := balances(t, cid)
	if available != 50 {
		t.Errorf("available = %d, want 50 (constraint backstop held)", available)
	}
	if rows := ledgerRows(t, cid); len(rows) != 1 {
		t.Errorf("ledger rows = %d, want 1", len(rows))
	}
}

func TestCreditRejectsNonPositiveAmount(t *testing.T) {
	svc, cid := setup(t)
	for _, amount := range []int64{0, -10} {
		if err := svc.Credit(context.Background(), CreditParams{
			CustomerID: cid, Type: db.TransactionTypeSignInReward, Amount: amount,
		}); err == nil {
			t.Errorf("credit %d succeeded, want error", amount)
		}
	}
}
