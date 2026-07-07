package payment

import (
	"context"
	"net/url"
	"strconv"
	"sync"
	"testing"

	"github.com/netfishx/gabon-go/internal/testdb"
	"github.com/netfishx/gabon-go/internal/wallet"
)

// 并发重复回调（PRD #63 第二缝）：同一订单的 N 路并发成功回调，
// MarkRechargeSucceeded 的条件 UPDATE + CreditTx 幂等约束保证恰好一次到账、
// 恰好一笔 recharge 流水，钱包对账恒等式成立。
func TestConcurrentCallbackCreditsExactlyOnce(t *testing.T) {
	ctx := context.Background()
	pool, cleanup, err := testdb.Start(ctx)
	if err != nil {
		t.Fatalf("start testdb: %v", err)
	}
	t.Cleanup(cleanup)

	var customerID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO customers (public_id, username, password_hash, invite_code)
		 VALUES ('payrace00001', 'pay_racer', 'not-a-real-hash', 'PY000001') RETURNING id`,
	).Scan(&customerID); err != nil {
		t.Fatalf("stage customer: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO wallets (customer_id) VALUES ($1)`, customerID); err != nil {
		t.Fatalf("stage wallet: %v", err)
	}

	wallets := wallet.NewService(pool)
	registry, err := NewRegistry(NewMockProvider())
	if err != nil {
		t.Fatalf("registry: %v", err)
	}
	svc := NewService(pool, wallets, registry, "")

	const fiat = 25000
	order, _, err := svc.CreateRechargeOrder(ctx, customerID, fiat, "mock")
	if err != nil {
		t.Fatalf("create recharge order: %v", err)
	}

	form := url.Values{
		"order_no":          {order.OrderNo},
		"provider_order_no": {"MOCK-" + order.OrderNo},
		"status":            {"success"},
		"amount":            {strconv.FormatInt(fiat, 10)},
	}

	const workers = 8
	var wg sync.WaitGroup
	start := make(chan struct{})
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if _, err := svc.HandlePayCallback(ctx, MockProviderCode, &CallbackRequest{Form: form}); err != nil {
				t.Errorf("HandlePayCallback: %v", err)
			}
		}()
	}
	close(start)
	wg.Wait()

	var available, frozen int64
	if err := pool.QueryRow(ctx,
		`SELECT available, frozen FROM wallets WHERE customer_id=$1`, customerID).Scan(&available, &frozen); err != nil {
		t.Fatalf("query wallet: %v", err)
	}
	if available != fiat {
		t.Fatalf("available = %d, want %d (credit exactly once)", available, fiat)
	}

	var txCount int
	var ledger int64
	if err := pool.QueryRow(ctx,
		`SELECT count(*), COALESCE(SUM(amount),0) FROM transactions WHERE customer_id=$1 AND type='recharge'`,
		customerID).Scan(&txCount, &ledger); err != nil {
		t.Fatalf("query recharge tx: %v", err)
	}
	if txCount != 1 {
		t.Fatalf("recharge tx count = %d, want 1", txCount)
	}
	if ledger != available+frozen {
		t.Fatalf("audit identity broken: ledger=%d wallet_total=%d", ledger, available+frozen)
	}
}
