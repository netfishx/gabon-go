package payment

import (
	"context"
	"errors"
	"net/url"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/netfishx/gabon-go/internal/db"
	"github.com/netfishx/gabon-go/internal/testdb"
	"github.com/netfishx/gabon-go/internal/wallet"
)

type timeoutProvider struct {
	queryResult *QueryResult
	queryErr    error
}

func (timeoutProvider) Code() string               { return "timeout-stub" }
func (timeoutProvider) SupportedMethods() []string { return []string{"timeout-stub"} }
func (timeoutProvider) Withdraw(context.Context, WithdrawCommand) (*WithdrawResult, error) {
	return &WithdrawResult{}, nil
}
func (timeoutProvider) ParseCallback(*CallbackRequest) (*CallbackResult, error) {
	return &CallbackResult{}, nil
}
func (timeoutProvider) Pay(_ context.Context, cmd PayCommand) (*PayResult, error) {
	return &PayResult{
		ProviderOrderNo: "STUB-" + cmd.Order.OrderNo,
		ProviderStatus:  "pending",
	}, nil
}
func (p timeoutProvider) Query(context.Context, OrderView) (*QueryResult, error) {
	return p.queryResult, p.queryErr
}

type raceTimeoutProvider struct{ timeoutProvider }

func (raceTimeoutProvider) ParseCallback(req *CallbackRequest) (*CallbackResult, error) {
	amount, err := strconv.ParseInt(req.Form.Get("amount"), 10, 64)
	if err != nil {
		return nil, err
	}
	return &CallbackResult{
		Valid:           true,
		OrderNo:         req.Form.Get("order_no"),
		ProviderOrderNo: req.Form.Get("provider_order_no"),
		FiatAmount:      amount,
		Outcome:         OutcomeSuccess,
		ProviderStatus:  "success",
		AckSuccess:      Ack{Body: []byte("success")},
		AckFailure:      Ack{Body: []byte("fail")},
	}, nil
}

func TestCancelExpiredRechargesCancelsPendingWithoutChangingBalance(t *testing.T) {
	ctx := context.Background()
	pool, cleanup, err := testdb.Start(ctx)
	if err != nil {
		t.Fatalf("start testdb: %v", err)
	}
	t.Cleanup(cleanup)

	var customerID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO customers (public_id, username, password_hash, invite_code)
		 VALUES ('timeout00001', 'timeout_customer', 'not-a-real-hash', 'TIME0001') RETURNING id`,
	).Scan(&customerID); err != nil {
		t.Fatalf("stage customer: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO wallets (customer_id) VALUES ($1)`, customerID); err != nil {
		t.Fatalf("stage wallet: %v", err)
	}

	provider := timeoutProvider{queryResult: &QueryResult{
		Outcome:        OutcomePending,
		ProviderStatus: "pending",
		RawRequest:     []byte(`request=raw`),
		RawResponse:    []byte(`{"status":"pending"}`),
	}}
	registry, err := NewRegistry(provider)
	if err != nil {
		t.Fatalf("registry: %v", err)
	}
	svc := NewService(pool, wallet.NewService(pool), registry, "", 10*time.Minute)

	order, _, err := svc.CreateRechargeOrder(ctx, customerID, 5000, "timeout-stub")
	if err != nil {
		t.Fatalf("create recharge order: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE recharge_orders SET expires_at = now() - interval '1 minute' WHERE id = $1`, order.ID,
	); err != nil {
		t.Fatalf("expire recharge order: %v", err)
	}

	cancelled, err := svc.CancelExpiredRecharges(ctx)
	if err != nil {
		t.Fatalf("CancelExpiredRecharges: %v", err)
	}
	if cancelled != 1 {
		t.Fatalf("cancelled = %d, want 1", cancelled)
	}

	got, err := db.New(pool).GetRechargeOrderByOrderNo(ctx, order.OrderNo)
	if err != nil {
		t.Fatalf("get recharge order: %v", err)
	}
	if got.Status != db.RechargeOrderStatusCancelled {
		t.Fatalf("status = %s, want cancelled", got.Status)
	}
	w, err := wallet.NewService(pool).Get(ctx, customerID)
	if err != nil {
		t.Fatalf("get wallet: %v", err)
	}
	if w.Available != 0 || w.Frozen != 0 {
		t.Fatalf("wallet = available %d frozen %d, want both 0", w.Available, w.Frozen)
	}

	var eventCount int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM payment_events
		 WHERE order_no = $1 AND direction = 'query' AND payload->>'action' = 'cancelled'`,
		order.OrderNo,
	).Scan(&eventCount); err != nil {
		t.Fatalf("query payment event: %v", err)
	}
	if eventCount != 1 {
		t.Fatalf("cancelled query event count = %d, want 1", eventCount)
	}

}

func TestCancelExpiredRechargesSettlesPaidOrder(t *testing.T) {
	ctx := context.Background()
	pool, cleanup, err := testdb.Start(ctx)
	if err != nil {
		t.Fatalf("start testdb: %v", err)
	}
	defer cleanup()

	var customerID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO customers (public_id, username, password_hash, invite_code)
		 VALUES ('timeout00002', 'timeout_paid', 'not-a-real-hash', 'TIME0002') RETURNING id`,
	).Scan(&customerID); err != nil {
		t.Fatalf("stage customer: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO wallets (customer_id) VALUES ($1)`, customerID); err != nil {
		t.Fatalf("stage wallet: %v", err)
	}

	const amount = int64(6000)
	provider := timeoutProvider{queryResult: &QueryResult{
		Outcome:        OutcomeSuccess,
		ProviderStatus: "success",
		FiatAmount:     amount,
	}}
	registry, err := NewRegistry(provider)
	if err != nil {
		t.Fatalf("registry: %v", err)
	}
	svc := NewService(pool, wallet.NewService(pool), registry, "", 10*time.Minute)
	order, _, err := svc.CreateRechargeOrder(ctx, customerID, amount, "timeout-stub")
	if err != nil {
		t.Fatalf("create recharge order: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE recharge_orders SET expires_at = now() - interval '1 minute' WHERE id = $1`, order.ID,
	); err != nil {
		t.Fatalf("expire recharge order: %v", err)
	}

	cancelled, err := svc.CancelExpiredRecharges(ctx)
	if err != nil {
		t.Fatalf("CancelExpiredRecharges: %v", err)
	}
	if cancelled != 0 {
		t.Fatalf("cancelled = %d, want 0", cancelled)
	}
	got, err := db.New(pool).GetRechargeOrderByOrderNo(ctx, order.OrderNo)
	if err != nil {
		t.Fatalf("get recharge order: %v", err)
	}
	if got.Status != db.RechargeOrderStatusSucceeded {
		t.Fatalf("status = %s, want succeeded", got.Status)
	}
	w, err := wallet.NewService(pool).Get(ctx, customerID)
	if err != nil {
		t.Fatalf("get wallet: %v", err)
	}
	if w.Available != amount {
		t.Fatalf("available = %d, want %d", w.Available, amount)
	}
	var txCount int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM transactions WHERE customer_id = $1 AND type = 'recharge' AND ref_id = $2`,
		customerID, order.ID,
	).Scan(&txCount); err != nil {
		t.Fatalf("query recharge transaction: %v", err)
	}
	if txCount != 1 {
		t.Fatalf("recharge transaction count = %d, want 1", txCount)
	}
}

func TestCancelExpiredRechargesMarksProviderFailure(t *testing.T) {
	ctx := context.Background()
	pool, cleanup, err := testdb.Start(ctx)
	if err != nil {
		t.Fatalf("start testdb: %v", err)
	}
	defer cleanup()

	var customerID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO customers (public_id, username, password_hash, invite_code)
		 VALUES ('timeout00003', 'timeout_failed', 'not-a-real-hash', 'TIME0003') RETURNING id`,
	).Scan(&customerID); err != nil {
		t.Fatalf("stage customer: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO wallets (customer_id) VALUES ($1)`, customerID); err != nil {
		t.Fatalf("stage wallet: %v", err)
	}

	provider := timeoutProvider{queryResult: &QueryResult{Outcome: OutcomeFailed, ProviderStatus: "failed"}}
	registry, err := NewRegistry(provider)
	if err != nil {
		t.Fatalf("registry: %v", err)
	}
	svc := NewService(pool, wallet.NewService(pool), registry, "", 10*time.Minute)
	order, _, err := svc.CreateRechargeOrder(ctx, customerID, 7000, "timeout-stub")
	if err != nil {
		t.Fatalf("create recharge order: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE recharge_orders SET expires_at = now() - interval '1 minute' WHERE id = $1`, order.ID,
	); err != nil {
		t.Fatalf("expire recharge order: %v", err)
	}

	cancelled, err := svc.CancelExpiredRecharges(ctx)
	if err != nil {
		t.Fatalf("CancelExpiredRecharges: %v", err)
	}
	if cancelled != 0 {
		t.Fatalf("cancelled = %d, want 0", cancelled)
	}
	got, err := db.New(pool).GetRechargeOrderByOrderNo(ctx, order.OrderNo)
	if err != nil {
		t.Fatalf("get recharge order: %v", err)
	}
	if got.Status != db.RechargeOrderStatusFailed {
		t.Fatalf("status = %s, want failed", got.Status)
	}
	w, err := wallet.NewService(pool).Get(ctx, customerID)
	if err != nil {
		t.Fatalf("get wallet: %v", err)
	}
	if w.Available != 0 || w.Frozen != 0 {
		t.Fatalf("wallet = available %d frozen %d, want both 0", w.Available, w.Frozen)
	}
}

func TestCancelExpiredRechargesLeavesFutureOrderPending(t *testing.T) {
	ctx := context.Background()
	pool, cleanup, err := testdb.Start(ctx)
	if err != nil {
		t.Fatalf("start testdb: %v", err)
	}
	defer cleanup()

	var customerID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO customers (public_id, username, password_hash, invite_code)
		 VALUES ('timeout00004', 'timeout_future', 'not-a-real-hash', 'TIME0004') RETURNING id`,
	).Scan(&customerID); err != nil {
		t.Fatalf("stage customer: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO wallets (customer_id) VALUES ($1)`, customerID); err != nil {
		t.Fatalf("stage wallet: %v", err)
	}
	provider := timeoutProvider{queryResult: &QueryResult{Outcome: OutcomePending}}
	registry, err := NewRegistry(provider)
	if err != nil {
		t.Fatalf("registry: %v", err)
	}
	svc := NewService(pool, wallet.NewService(pool), registry, "", 10*time.Minute)
	order, _, err := svc.CreateRechargeOrder(ctx, customerID, 8000, "timeout-stub")
	if err != nil {
		t.Fatalf("create recharge order: %v", err)
	}

	cancelled, err := svc.CancelExpiredRecharges(ctx)
	if err != nil {
		t.Fatalf("CancelExpiredRecharges: %v", err)
	}
	if cancelled != 0 {
		t.Fatalf("cancelled = %d, want 0", cancelled)
	}
	got, err := db.New(pool).GetRechargeOrderByOrderNo(ctx, order.OrderNo)
	if err != nil {
		t.Fatalf("get recharge order: %v", err)
	}
	if got.Status != db.RechargeOrderStatusPendingPayment {
		t.Fatalf("status = %s, want pending_payment", got.Status)
	}
}

func TestCancelExpiredRechargesRecordsQueryErrorAndRetriesLater(t *testing.T) {
	ctx := context.Background()
	pool, cleanup, err := testdb.Start(ctx)
	if err != nil {
		t.Fatalf("start testdb: %v", err)
	}
	defer cleanup()

	var customerID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO customers (public_id, username, password_hash, invite_code)
		 VALUES ('timeout00005', 'timeout_error', 'not-a-real-hash', 'TIME0005') RETURNING id`,
	).Scan(&customerID); err != nil {
		t.Fatalf("stage customer: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO wallets (customer_id) VALUES ($1)`, customerID); err != nil {
		t.Fatalf("stage wallet: %v", err)
	}
	provider := timeoutProvider{queryErr: errors.New("provider unavailable")}
	registry, err := NewRegistry(provider)
	if err != nil {
		t.Fatalf("registry: %v", err)
	}
	svc := NewService(pool, wallet.NewService(pool), registry, "", 10*time.Minute)
	order, _, err := svc.CreateRechargeOrder(ctx, customerID, 9000, "timeout-stub")
	if err != nil {
		t.Fatalf("create recharge order: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE recharge_orders SET expires_at = now() - interval '1 minute' WHERE id = $1`, order.ID,
	); err != nil {
		t.Fatalf("expire recharge order: %v", err)
	}

	cancelled, err := svc.CancelExpiredRecharges(ctx)
	if err != nil {
		t.Fatalf("CancelExpiredRecharges: %v", err)
	}
	if cancelled != 0 {
		t.Fatalf("cancelled = %d, want 0", cancelled)
	}
	got, err := db.New(pool).GetRechargeOrderByOrderNo(ctx, order.OrderNo)
	if err != nil {
		t.Fatalf("get recharge order: %v", err)
	}
	if got.Status != db.RechargeOrderStatusPendingPayment {
		t.Fatalf("status = %s, want pending_payment", got.Status)
	}
	var eventCount int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM payment_events
		 WHERE order_no = $1 AND direction = 'query' AND payload->>'action' = 'error'`,
		order.OrderNo,
	).Scan(&eventCount); err != nil {
		t.Fatalf("query payment event: %v", err)
	}
	if eventCount != 1 {
		t.Fatalf("error query event count = %d, want 1", eventCount)
	}
}

func TestCancelExpiredRechargesSkipsAmountMismatch(t *testing.T) {
	ctx := context.Background()
	pool, cleanup, err := testdb.Start(ctx)
	if err != nil {
		t.Fatalf("start testdb: %v", err)
	}
	defer cleanup()

	var customerID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO customers (public_id, username, password_hash, invite_code)
		 VALUES ('timeout00006', 'timeout_mismatch', 'not-a-real-hash', 'TIME0006') RETURNING id`,
	).Scan(&customerID); err != nil {
		t.Fatalf("stage customer: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO wallets (customer_id) VALUES ($1)`, customerID); err != nil {
		t.Fatalf("stage wallet: %v", err)
	}
	provider := timeoutProvider{queryResult: &QueryResult{
		Outcome: OutcomeSuccess, ProviderStatus: "success", FiatAmount: 1,
	}}
	registry, err := NewRegistry(provider)
	if err != nil {
		t.Fatalf("registry: %v", err)
	}
	svc := NewService(pool, wallet.NewService(pool), registry, "", 10*time.Minute)
	order, _, err := svc.CreateRechargeOrder(ctx, customerID, 10000, "timeout-stub")
	if err != nil {
		t.Fatalf("create recharge order: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE recharge_orders SET expires_at = now() - interval '1 minute' WHERE id = $1`, order.ID,
	); err != nil {
		t.Fatalf("expire recharge order: %v", err)
	}

	if _, err := svc.CancelExpiredRecharges(ctx); err != nil {
		t.Fatalf("CancelExpiredRecharges: %v", err)
	}
	got, err := db.New(pool).GetRechargeOrderByOrderNo(ctx, order.OrderNo)
	if err != nil {
		t.Fatalf("get recharge order: %v", err)
	}
	if got.Status != db.RechargeOrderStatusPendingPayment {
		t.Fatalf("status = %s, want pending_payment", got.Status)
	}
	w, err := wallet.NewService(pool).Get(ctx, customerID)
	if err != nil {
		t.Fatalf("get wallet: %v", err)
	}
	if w.Available != 0 || w.Frozen != 0 {
		t.Fatalf("wallet = available %d frozen %d, want both 0", w.Available, w.Frozen)
	}
}

func TestRechargeCallbackAndTimeoutSweepReachOneTerminalState(t *testing.T) {
	ctx := context.Background()
	pool, cleanup, err := testdb.Start(ctx)
	if err != nil {
		t.Fatalf("start testdb: %v", err)
	}
	defer cleanup()

	var customerID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO customers (public_id, username, password_hash, invite_code)
		 VALUES ('timeout00007', 'timeout_race', 'not-a-real-hash', 'TIME0007') RETURNING id`,
	).Scan(&customerID); err != nil {
		t.Fatalf("stage customer: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO wallets (customer_id) VALUES ($1)`, customerID); err != nil {
		t.Fatalf("stage wallet: %v", err)
	}
	provider := raceTimeoutProvider{timeoutProvider{queryResult: &QueryResult{
		Outcome: OutcomePending, ProviderStatus: "pending",
	}}}
	registry, err := NewRegistry(provider)
	if err != nil {
		t.Fatalf("registry: %v", err)
	}
	svc := NewService(pool, wallet.NewService(pool), registry, "", 10*time.Minute)

	const (
		iterations = 20
		amount     = int64(11000)
	)
	succeeded := int64(0)
	for range iterations {
		order, _, err := svc.CreateRechargeOrder(ctx, customerID, amount, "timeout-stub")
		if err != nil {
			t.Fatalf("create recharge order: %v", err)
		}
		if _, err := pool.Exec(ctx,
			`UPDATE recharge_orders SET expires_at = now() - interval '1 minute' WHERE id = $1`, order.ID,
		); err != nil {
			t.Fatalf("expire recharge order: %v", err)
		}

		form := url.Values{
			"order_no":          {order.OrderNo},
			"provider_order_no": {"STUB-" + order.OrderNo},
			"amount":            {strconv.FormatInt(amount, 10)},
		}
		start := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			if _, err := svc.HandlePayCallback(ctx, provider.Code(), &CallbackRequest{Form: form}); err != nil {
				t.Errorf("HandlePayCallback: %v", err)
			}
		}()
		go func() {
			defer wg.Done()
			<-start
			if _, err := svc.CancelExpiredRecharges(ctx); err != nil {
				t.Errorf("CancelExpiredRecharges: %v", err)
			}
		}()
		close(start)
		wg.Wait()

		got, err := db.New(pool).GetRechargeOrderByOrderNo(ctx, order.OrderNo)
		if err != nil {
			t.Fatalf("get recharge order: %v", err)
		}
		var txCount int
		if err := pool.QueryRow(ctx,
			`SELECT count(*) FROM transactions WHERE type = 'recharge' AND ref_id = $1`, order.ID,
		).Scan(&txCount); err != nil {
			t.Fatalf("query recharge transaction: %v", err)
		}
		switch got.Status {
		case db.RechargeOrderStatusSucceeded:
			succeeded++
			if txCount != 1 {
				t.Fatalf("succeeded order %s transaction count = %d, want 1", order.OrderNo, txCount)
			}
		case db.RechargeOrderStatusCancelled:
			if txCount != 0 {
				t.Fatalf("cancelled order %s transaction count = %d, want 0", order.OrderNo, txCount)
			}
		default:
			t.Fatalf("order %s status = %s, want succeeded or cancelled", order.OrderNo, got.Status)
		}
	}

	w, err := wallet.NewService(pool).Get(ctx, customerID)
	if err != nil {
		t.Fatalf("get wallet: %v", err)
	}
	if want := succeeded * amount; w.Available != want {
		t.Fatalf("available = %d, want %d for %d succeeded orders", w.Available, want, succeeded)
	}
}
