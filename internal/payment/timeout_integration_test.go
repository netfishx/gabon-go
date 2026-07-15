package payment

import (
	"context"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/netfishx/gabon-go/internal/db"
	"github.com/netfishx/gabon-go/internal/testdb"
	"github.com/netfishx/gabon-go/internal/wallet"
)

type timeoutProvider struct {
	queryResult *QueryResult
	queryErr    error
	queryCalls  *atomic.Int32
	queryFn     func(context.Context, OrderView) (*QueryResult, error)
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

func (p timeoutProvider) Query(ctx context.Context, order OrderView) (*QueryResult, error) {
	if p.queryCalls != nil {
		p.queryCalls.Add(1)
	}
	if p.queryFn != nil {
		return p.queryFn(ctx, order)
	}
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
	if err := pool.QueryRow(
		ctx,
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
	if _, err := pool.Exec(
		ctx,
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
	if err := pool.QueryRow(
		ctx,
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
	if err := pool.QueryRow(
		ctx,
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
	if _, err := pool.Exec(
		ctx,
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
	if err := pool.QueryRow(
		ctx,
		`SELECT count(*) FROM transactions WHERE customer_id = $1 AND type = 'recharge' AND ref_id = $2`,
		customerID, order.ID,
	).Scan(&txCount); err != nil {
		t.Fatalf("query recharge transaction: %v", err)
	}
	if txCount != 1 {
		t.Fatalf("recharge transaction count = %d, want 1", txCount)
	}
	var eventCount int
	if err := pool.QueryRow(
		ctx,
		`SELECT count(*) FROM payment_events
		 WHERE order_no = $1 AND direction = 'query' AND payload->>'action' = 'settled'`,
		order.OrderNo,
	).Scan(&eventCount); err != nil {
		t.Fatalf("query payment event: %v", err)
	}
	if eventCount != 1 {
		t.Fatalf("settled query event count = %d, want 1", eventCount)
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
	if err := pool.QueryRow(
		ctx,
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
	if _, err := pool.Exec(
		ctx,
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
	var eventCount int
	if err := pool.QueryRow(
		ctx,
		`SELECT count(*) FROM payment_events
		 WHERE order_no = $1 AND direction = 'query' AND payload->>'action' = 'failed'`,
		order.OrderNo,
	).Scan(&eventCount); err != nil {
		t.Fatalf("query payment event: %v", err)
	}
	if eventCount != 1 {
		t.Fatalf("failed query event count = %d, want 1", eventCount)
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
	if err := pool.QueryRow(
		ctx,
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
	if err := pool.QueryRow(
		ctx,
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
	if _, err := pool.Exec(
		ctx,
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
	if err := pool.QueryRow(
		ctx,
		`SELECT count(*) FROM payment_events
		 WHERE order_no = $1 AND direction = 'query' AND payload->>'action' = 'error'`,
		order.OrderNo,
	).Scan(&eventCount); err != nil {
		t.Fatalf("query payment event: %v", err)
	}
	if eventCount != 1 {
		t.Fatalf("error query event count = %d, want 1", eventCount)
	}

	recoveredProvider := timeoutProvider{queryResult: &QueryResult{
		Outcome:        OutcomePending,
		ProviderStatus: "pending",
	}}
	recoveredRegistry, err := NewRegistry(recoveredProvider)
	if err != nil {
		t.Fatalf("recovered registry: %v", err)
	}
	recoveredSvc := NewService(pool, wallet.NewService(pool), recoveredRegistry, "", 10*time.Minute)
	cancelled, err = recoveredSvc.CancelExpiredRecharges(ctx)
	if err != nil {
		t.Fatalf("CancelExpiredRecharges after recovery: %v", err)
	}
	if cancelled != 1 {
		t.Fatalf("cancelled after recovery = %d, want 1", cancelled)
	}
	got, err = db.New(pool).GetRechargeOrderByOrderNo(ctx, order.OrderNo)
	if err != nil {
		t.Fatalf("get recharge order after recovery: %v", err)
	}
	if got.Status != db.RechargeOrderStatusCancelled {
		t.Fatalf("status after recovery = %s, want cancelled", got.Status)
	}
}

func TestCancelExpiredRechargesMarksAndExcludesAmountMismatchThenSettlesCorrectCallback(t *testing.T) {
	ctx := context.Background()
	pool, cleanup, err := testdb.Start(ctx)
	if err != nil {
		t.Fatalf("start testdb: %v", err)
	}
	defer cleanup()

	var customerID int64
	if err := pool.QueryRow(
		ctx,
		`INSERT INTO customers (public_id, username, password_hash, invite_code)
		 VALUES ('timeout00006', 'timeout_mismatch', 'not-a-real-hash', 'TIME0006') RETURNING id`,
	).Scan(&customerID); err != nil {
		t.Fatalf("stage customer: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO wallets (customer_id) VALUES ($1)`, customerID); err != nil {
		t.Fatalf("stage wallet: %v", err)
	}
	var queryCalls atomic.Int32
	provider := raceTimeoutProvider{timeoutProvider{
		queryResult: &QueryResult{
			Outcome: OutcomeSuccess, ProviderStatus: "success", FiatAmount: 1,
		},
		queryCalls: &queryCalls,
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
	if _, err := pool.Exec(
		ctx,
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
	if got.FailureCode == nil || *got.FailureCode != "amount_mismatch" {
		t.Fatalf("failure_code = %v, want amount_mismatch", got.FailureCode)
	}
	if got.FailureReason == nil || !strings.Contains(*got.FailureReason, "query_amount=1") || !strings.Contains(*got.FailureReason, "order_amount=10000") {
		t.Fatalf("failure_reason = %v, want both query and order amounts", got.FailureReason)
	}
	w, err := wallet.NewService(pool).Get(ctx, customerID)
	if err != nil {
		t.Fatalf("get wallet: %v", err)
	}
	if w.Available != 0 || w.Frozen != 0 {
		t.Fatalf("wallet = available %d frozen %d, want both 0", w.Available, w.Frozen)
	}
	var eventCount int
	if err := pool.QueryRow(
		ctx,
		`SELECT count(*) FROM payment_events
		 WHERE order_no = $1 AND direction = 'query' AND payload->>'action' = 'amount_mismatch'`,
		order.OrderNo,
	).Scan(&eventCount); err != nil {
		t.Fatalf("query amount mismatch event: %v", err)
	}
	if eventCount != 1 {
		t.Fatalf("amount mismatch event count = %d, want 1", eventCount)
	}
	if got := queryCalls.Load(); got != 1 {
		t.Fatalf("query calls after first sweep = %d, want 1", got)
	}

	if _, err := svc.CancelExpiredRecharges(ctx); err != nil {
		t.Fatalf("CancelExpiredRecharges second pass: %v", err)
	}
	if got := queryCalls.Load(); got != 1 {
		t.Fatalf("query calls after second sweep = %d, want 1", got)
	}
	got, err = db.New(pool).GetRechargeOrderByOrderNo(ctx, order.OrderNo)
	if err != nil {
		t.Fatalf("get recharge order after second sweep: %v", err)
	}
	if got.Status != db.RechargeOrderStatusPendingPayment {
		t.Fatalf("status after second sweep = %s, want pending_payment", got.Status)
	}

	form := url.Values{
		"order_no":          {order.OrderNo},
		"provider_order_no": {"STUB-" + order.OrderNo},
		"amount":            {"10000"},
	}
	if _, err := svc.HandlePayCallback(ctx, provider.Code(), &CallbackRequest{Form: form}); err != nil {
		t.Fatalf("HandlePayCallback: %v", err)
	}
	got, err = db.New(pool).GetRechargeOrderByOrderNo(ctx, order.OrderNo)
	if err != nil {
		t.Fatalf("get recharge order after callback: %v", err)
	}
	if got.Status != db.RechargeOrderStatusSucceeded {
		t.Fatalf("status after callback = %s, want succeeded", got.Status)
	}
	if got.FailureCode != nil || got.FailureReason != nil {
		t.Fatalf("failure marker after callback = (%v, %v), want both nil", got.FailureCode, got.FailureReason)
	}
	w, err = wallet.NewService(pool).Get(ctx, customerID)
	if err != nil {
		t.Fatalf("get wallet after callback: %v", err)
	}
	if w.Available != 10000 || w.Frozen != 0 {
		t.Fatalf("wallet after callback = available %d frozen %d, want 10000 and 0", w.Available, w.Frozen)
	}
	var txCount int
	if err := pool.QueryRow(
		ctx,
		`SELECT count(*) FROM transactions WHERE type = 'recharge' AND ref_id = $1`, order.ID,
	).Scan(&txCount); err != nil {
		t.Fatalf("query recharge transaction: %v", err)
	}
	if txCount != 1 {
		t.Fatalf("recharge transaction count = %d, want 1", txCount)
	}
}

func TestCancelExpiredRechargesDoesNotClaimAmountMismatchWhenMarkerCASLoses(t *testing.T) {
	ctx := context.Background()
	pool, cleanup, err := testdb.Start(ctx)
	if err != nil {
		t.Fatalf("start testdb: %v", err)
	}
	t.Cleanup(cleanup)

	var customerID int64
	if err := pool.QueryRow(
		ctx,
		`INSERT INTO customers (public_id, username, password_hash, invite_code)
		 VALUES ('timeout00009', 'timeout_mismatch_race', 'not-a-real-hash', 'TIME0009') RETURNING id`,
	).Scan(&customerID); err != nil {
		t.Fatalf("stage customer: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO wallets (customer_id) VALUES ($1)`, customerID); err != nil {
		t.Fatalf("stage wallet: %v", err)
	}

	var orderID int64
	provider := timeoutProvider{queryFn: func(ctx context.Context, _ OrderView) (*QueryResult, error) {
		if _, err := pool.Exec(
			ctx,
			`UPDATE recharge_orders
			 SET status = 'cancelled', completed_at = now(), updated_at = now()
			 WHERE id = $1 AND status = 'pending_payment'`,
			orderID,
		); err != nil {
			return nil, err
		}
		return &QueryResult{Outcome: OutcomeSuccess, ProviderStatus: "success", FiatAmount: 1}, nil
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
	orderID = order.ID
	if _, err := pool.Exec(
		ctx,
		`UPDATE recharge_orders SET expires_at = now() - interval '1 minute' WHERE id = $1`, order.ID,
	); err != nil {
		t.Fatalf("expire recharge order: %v", err)
	}

	if _, err := svc.CancelExpiredRecharges(ctx); err != nil {
		t.Fatalf("CancelExpiredRecharges: %v", err)
	}
	var mismatchEvents, skippedEvents int
	if err := pool.QueryRow(
		ctx,
		`SELECT count(*) FILTER (WHERE payload->>'action' = 'amount_mismatch'),
		        count(*) FILTER (WHERE payload->>'action' = 'skipped')
		 FROM payment_events WHERE order_no = $1 AND direction = 'query'`,
		order.OrderNo,
	).Scan(&mismatchEvents, &skippedEvents); err != nil {
		t.Fatalf("query sweep events: %v", err)
	}
	if mismatchEvents != 0 || skippedEvents != 1 {
		t.Fatalf("query events = amount_mismatch %d skipped %d, want 0 and 1", mismatchEvents, skippedEvents)
	}
}

func TestRechargeCallbackMarksAmountMismatchAndExcludesSweepThenCorrectCallbackSettles(t *testing.T) {
	ctx := context.Background()
	pool, cleanup, err := testdb.Start(ctx)
	if err != nil {
		t.Fatalf("start testdb: %v", err)
	}
	t.Cleanup(cleanup)

	var customerID int64
	if err := pool.QueryRow(
		ctx,
		`INSERT INTO customers (public_id, username, password_hash, invite_code)
		 VALUES ('timeout00010', 'timeout_callback_mismatch', 'not-a-real-hash', 'TIME0010') RETURNING id`,
	).Scan(&customerID); err != nil {
		t.Fatalf("stage customer: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO wallets (customer_id) VALUES ($1)`, customerID); err != nil {
		t.Fatalf("stage wallet: %v", err)
	}

	var queryCalls atomic.Int32
	provider := raceTimeoutProvider{timeoutProvider{
		queryResult: &QueryResult{Outcome: OutcomePending, ProviderStatus: "pending"},
		queryCalls:  &queryCalls,
	}}
	registry, err := NewRegistry(provider)
	if err != nil {
		t.Fatalf("registry: %v", err)
	}
	svc := NewService(pool, wallet.NewService(pool), registry, "", 10*time.Minute)
	const amount = int64(14000)
	order, _, err := svc.CreateRechargeOrder(ctx, customerID, amount, "timeout-stub")
	if err != nil {
		t.Fatalf("create recharge order: %v", err)
	}

	mismatchForm := url.Values{
		"order_no":          {order.OrderNo},
		"provider_order_no": {"STUB-" + order.OrderNo},
		"amount":            {"1"},
	}
	ack, err := svc.HandlePayCallback(ctx, provider.Code(), &CallbackRequest{Form: mismatchForm})
	if err != nil {
		t.Fatalf("HandlePayCallback amount mismatch: %v", err)
	}
	if string(ack.Body) != "fail" {
		t.Fatalf("amount mismatch ack = %q, want fail", ack.Body)
	}
	got, err := db.New(pool).GetRechargeOrderByOrderNo(ctx, order.OrderNo)
	if err != nil {
		t.Fatalf("get recharge order after mismatch callback: %v", err)
	}
	if got.Status != db.RechargeOrderStatusPendingPayment {
		t.Fatalf("status after mismatch callback = %s, want pending_payment", got.Status)
	}
	if got.FailureCode == nil || *got.FailureCode != "amount_mismatch" {
		t.Fatalf("failure_code = %v, want amount_mismatch", got.FailureCode)
	}
	if got.FailureReason == nil || !strings.Contains(*got.FailureReason, "callback_amount=1") || !strings.Contains(*got.FailureReason, "order_amount=14000") {
		t.Fatalf("failure_reason = %v, want callback and order amounts", got.FailureReason)
	}
	if _, err := pool.Exec(
		ctx,
		`UPDATE recharge_orders SET expires_at = now() - interval '25 hours' WHERE id = $1`, order.ID,
	); err != nil {
		t.Fatalf("expire recharge order: %v", err)
	}
	if _, err := svc.CancelExpiredRecharges(ctx); err != nil {
		t.Fatalf("CancelExpiredRecharges: %v", err)
	}
	if got := queryCalls.Load(); got != 0 {
		t.Fatalf("query calls after mismatch callback = %d, want 0", got)
	}

	correctForm := url.Values{
		"order_no":          {order.OrderNo},
		"provider_order_no": {"STUB-" + order.OrderNo},
		"amount":            {strconv.FormatInt(amount, 10)},
	}
	ack, err = svc.HandlePayCallback(ctx, provider.Code(), &CallbackRequest{Form: correctForm})
	if err != nil {
		t.Fatalf("HandlePayCallback corrected amount: %v", err)
	}
	if string(ack.Body) != "success" {
		t.Fatalf("corrected amount ack = %q, want success", ack.Body)
	}
	got, err = db.New(pool).GetRechargeOrderByOrderNo(ctx, order.OrderNo)
	if err != nil {
		t.Fatalf("get recharge order after corrected callback: %v", err)
	}
	if got.Status != db.RechargeOrderStatusSucceeded {
		t.Fatalf("status after corrected callback = %s, want succeeded", got.Status)
	}
	if got.FailureCode != nil || got.FailureReason != nil {
		t.Fatalf("failure marker after corrected callback = (%v, %v), want both nil", got.FailureCode, got.FailureReason)
	}
	w, err := wallet.NewService(pool).Get(ctx, customerID)
	if err != nil {
		t.Fatalf("get wallet: %v", err)
	}
	if w.Available != amount || w.Frozen != 0 {
		t.Fatalf("wallet = available %d frozen %d, want %d and 0", w.Available, w.Frozen, amount)
	}
	var txCount int
	if err := pool.QueryRow(
		ctx,
		`SELECT count(*) FROM transactions WHERE type = 'recharge' AND ref_id = $1`, order.ID,
	).Scan(&txCount); err != nil {
		t.Fatalf("query recharge transaction: %v", err)
	}
	if txCount != 1 {
		t.Fatalf("recharge transaction count = %d, want 1", txCount)
	}
}

func TestCancelExpiredRechargesGraceCancelsQueryErrorAndLateCallbackStaysCancelled(t *testing.T) {
	ctx := context.Background()
	pool, cleanup, err := testdb.Start(ctx)
	if err != nil {
		t.Fatalf("start testdb: %v", err)
	}
	t.Cleanup(cleanup)

	var customerID int64
	if err := pool.QueryRow(
		ctx,
		`INSERT INTO customers (public_id, username, password_hash, invite_code)
		 VALUES ('timeout00008', 'timeout_grace_error', 'not-a-real-hash', 'TIME0008') RETURNING id`,
	).Scan(&customerID); err != nil {
		t.Fatalf("stage customer: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO wallets (customer_id) VALUES ($1)`, customerID); err != nil {
		t.Fatalf("stage wallet: %v", err)
	}

	provider := raceTimeoutProvider{timeoutProvider{queryErr: errors.New("provider unavailable")}}
	registry, err := NewRegistry(provider)
	if err != nil {
		t.Fatalf("registry: %v", err)
	}
	svc := NewService(pool, wallet.NewService(pool), registry, "", 10*time.Minute)
	order, _, err := svc.CreateRechargeOrder(ctx, customerID, 12000, "timeout-stub")
	if err != nil {
		t.Fatalf("create recharge order: %v", err)
	}
	if _, err := pool.Exec(
		ctx,
		`UPDATE recharge_orders SET expires_at = now() - interval '25 hours' WHERE id = $1`, order.ID,
	); err != nil {
		t.Fatalf("expire recharge order beyond grace: %v", err)
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
	var eventCount int
	if err := pool.QueryRow(
		ctx,
		`SELECT count(*) FROM payment_events
		 WHERE order_no = $1 AND direction = 'query'
		   AND payload->>'action' = 'grace_cancelled'
		   AND payload->>'reason' = 'query_error'
		   AND payload->>'error' = 'provider unavailable'`,
		order.OrderNo,
	).Scan(&eventCount); err != nil {
		t.Fatalf("query grace cancellation event: %v", err)
	}
	if eventCount != 1 {
		t.Fatalf("grace cancellation event count = %d, want 1", eventCount)
	}

	form := url.Values{
		"order_no":          {order.OrderNo},
		"provider_order_no": {"STUB-" + order.OrderNo},
		"amount":            {"12000"},
	}
	if _, err := svc.HandlePayCallback(ctx, provider.Code(), &CallbackRequest{Form: form}); err != nil {
		t.Fatalf("HandlePayCallback: %v", err)
	}
	got, err = db.New(pool).GetRechargeOrderByOrderNo(ctx, order.OrderNo)
	if err != nil {
		t.Fatalf("get recharge order after callback: %v", err)
	}
	if got.Status != db.RechargeOrderStatusCancelled {
		t.Fatalf("status after callback = %s, want cancelled", got.Status)
	}
	w, err := wallet.NewService(pool).Get(ctx, customerID)
	if err != nil {
		t.Fatalf("get wallet: %v", err)
	}
	if w.Available != 0 || w.Frozen != 0 {
		t.Fatalf("wallet = available %d frozen %d, want both 0", w.Available, w.Frozen)
	}
	var txCount int
	if err := pool.QueryRow(
		ctx,
		`SELECT count(*) FROM transactions WHERE type = 'recharge' AND ref_id = $1`, order.ID,
	).Scan(&txCount); err != nil {
		t.Fatalf("query recharge transaction: %v", err)
	}
	if txCount != 0 {
		t.Fatalf("recharge transaction count = %d, want 0", txCount)
	}
}

func TestCancelExpiredRechargesHandlesUnregisteredProviderGraceWindow(t *testing.T) {
	tests := []struct {
		name           string
		expiresAgo     string
		wantStatus     db.RechargeOrderStatus
		wantCancelled  int
		wantEventCount int
	}{
		{
			name:           "inside grace remains pending",
			expiresAgo:     "1 minute",
			wantStatus:     db.RechargeOrderStatusPendingPayment,
			wantCancelled:  0,
			wantEventCount: 0,
		},
		{
			name:           "beyond grace is cancelled",
			expiresAgo:     "25 hours",
			wantStatus:     db.RechargeOrderStatusCancelled,
			wantCancelled:  1,
			wantEventCount: 1,
		},
	}

	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			pool, cleanup, err := testdb.Start(ctx)
			if err != nil {
				t.Fatalf("start testdb: %v", err)
			}
			t.Cleanup(cleanup)

			var customerID int64
			if err := pool.QueryRow(
				ctx,
				`INSERT INTO customers (public_id, username, password_hash, invite_code)
				 VALUES ($1, $2, 'not-a-real-hash', $3) RETURNING id`,
				"timeout-unregistered-"+strconv.Itoa(i),
				"timeout_unregistered_"+strconv.Itoa(i),
				"TIMEUN"+strconv.Itoa(i),
			).Scan(&customerID); err != nil {
				t.Fatalf("stage customer: %v", err)
			}
			if _, err := pool.Exec(ctx, `INSERT INTO wallets (customer_id) VALUES ($1)`, customerID); err != nil {
				t.Fatalf("stage wallet: %v", err)
			}

			provider := timeoutProvider{queryResult: &QueryResult{Outcome: OutcomePending}}
			creatorRegistry, err := NewRegistry(provider)
			if err != nil {
				t.Fatalf("creator registry: %v", err)
			}
			creator := NewService(pool, wallet.NewService(pool), creatorRegistry, "", 10*time.Minute)
			order, _, err := creator.CreateRechargeOrder(ctx, customerID, 13000, "timeout-stub")
			if err != nil {
				t.Fatalf("create recharge order: %v", err)
			}
			if _, err := pool.Exec(
				ctx,
				`UPDATE recharge_orders SET expires_at = now() - $1::interval WHERE id = $2`, tt.expiresAgo, order.ID,
			); err != nil {
				t.Fatalf("expire recharge order: %v", err)
			}

			emptyRegistry, err := NewRegistry()
			if err != nil {
				t.Fatalf("empty registry: %v", err)
			}
			sweeper := NewService(pool, wallet.NewService(pool), emptyRegistry, "", 10*time.Minute)
			cancelled, err := sweeper.CancelExpiredRecharges(ctx)
			if err != nil {
				t.Fatalf("CancelExpiredRecharges: %v", err)
			}
			if cancelled != tt.wantCancelled {
				t.Fatalf("cancelled = %d, want %d", cancelled, tt.wantCancelled)
			}
			got, err := db.New(pool).GetRechargeOrderByOrderNo(ctx, order.OrderNo)
			if err != nil {
				t.Fatalf("get recharge order: %v", err)
			}
			if got.Status != tt.wantStatus {
				t.Fatalf("status = %s, want %s", got.Status, tt.wantStatus)
			}
			w, err := wallet.NewService(pool).Get(ctx, customerID)
			if err != nil {
				t.Fatalf("get wallet: %v", err)
			}
			if w.Available != 0 || w.Frozen != 0 {
				t.Fatalf("wallet = available %d frozen %d, want both 0", w.Available, w.Frozen)
			}
			var eventCount int
			if err := pool.QueryRow(
				ctx,
				`SELECT count(*) FROM payment_events
				 WHERE order_no = $1 AND direction = 'query'
				   AND payload->>'action' = 'grace_cancelled'
				   AND payload->>'reason' = 'unregistered_provider'`,
				order.OrderNo,
			).Scan(&eventCount); err != nil {
				t.Fatalf("query grace cancellation event: %v", err)
			}
			if eventCount != tt.wantEventCount {
				t.Fatalf("grace cancellation event count = %d, want %d", eventCount, tt.wantEventCount)
			}
		})
	}
}

func TestCancelExpiredRechargesHandlesMissingProviderGraceWindow(t *testing.T) {
	tests := []struct {
		name           string
		expiresAgo     string
		wantStatus     db.RechargeOrderStatus
		wantCancelled  int
		wantEventCount int
	}{
		{
			name:           "inside grace remains pending",
			expiresAgo:     "1 minute",
			wantStatus:     db.RechargeOrderStatusPendingPayment,
			wantCancelled:  0,
			wantEventCount: 0,
		},
		{
			name:           "beyond grace is cancelled",
			expiresAgo:     "25 hours",
			wantStatus:     db.RechargeOrderStatusCancelled,
			wantCancelled:  1,
			wantEventCount: 1,
		},
	}

	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			pool, cleanup, err := testdb.Start(ctx)
			if err != nil {
				t.Fatalf("start testdb: %v", err)
			}
			t.Cleanup(cleanup)

			var customerID int64
			if err := pool.QueryRow(
				ctx,
				`INSERT INTO customers (public_id, username, password_hash, invite_code)
				 VALUES ($1, $2, 'not-a-real-hash', $3) RETURNING id`,
				"timeout-missing-"+strconv.Itoa(i),
				"timeout_missing_"+strconv.Itoa(i),
				"TIMEMS"+strconv.Itoa(i),
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
			order, _, err := svc.CreateRechargeOrder(ctx, customerID, 15000, "timeout-stub")
			if err != nil {
				t.Fatalf("create recharge order: %v", err)
			}
			if _, err := pool.Exec(
				ctx,
				`UPDATE recharge_orders
				 SET provider = NULL, expires_at = now() - $1::interval
				 WHERE id = $2`,
				tt.expiresAgo,
				order.ID,
			); err != nil {
				t.Fatalf("remove provider and expire recharge order: %v", err)
			}

			cancelled, err := svc.CancelExpiredRecharges(ctx)
			if err != nil {
				t.Fatalf("CancelExpiredRecharges: %v", err)
			}
			if cancelled != tt.wantCancelled {
				t.Fatalf("cancelled = %d, want %d", cancelled, tt.wantCancelled)
			}
			got, err := db.New(pool).GetRechargeOrderByOrderNo(ctx, order.OrderNo)
			if err != nil {
				t.Fatalf("get recharge order: %v", err)
			}
			if got.Status != tt.wantStatus {
				t.Fatalf("status = %s, want %s", got.Status, tt.wantStatus)
			}
			w, err := wallet.NewService(pool).Get(ctx, customerID)
			if err != nil {
				t.Fatalf("get wallet: %v", err)
			}
			if w.Available != 0 || w.Frozen != 0 {
				t.Fatalf("wallet = available %d frozen %d, want both 0", w.Available, w.Frozen)
			}
			var eventCount int
			if err := pool.QueryRow(
				ctx,
				`SELECT count(*) FROM payment_events
				 WHERE order_no = $1 AND direction = 'query'
				   AND payload->>'action' = 'grace_cancelled'
				   AND payload->>'reason' = 'missing_provider'`,
				order.OrderNo,
			).Scan(&eventCount); err != nil {
				t.Fatalf("query grace cancellation event: %v", err)
			}
			if eventCount != tt.wantEventCount {
				t.Fatalf("grace cancellation event count = %d, want %d", eventCount, tt.wantEventCount)
			}
		})
	}
}

func TestRechargeSweepIndexMigration(t *testing.T) {
	ctx := context.Background()
	pool, cleanup, err := testdb.Start(ctx)
	if err != nil {
		t.Fatalf("start testdb: %v", err)
	}
	t.Cleanup(cleanup)

	var indexDef string
	if err := pool.QueryRow(
		ctx,
		`SELECT indexdef FROM pg_indexes
		 WHERE schemaname = current_schema()
		   AND indexname = 'recharge_orders_pending_expires_idx'`,
	).Scan(&indexDef); err != nil {
		t.Fatalf("query recharge sweep index: %v", err)
	}
	for _, part := range []string{"expires_at", "pending_payment", "failure_code IS NULL"} {
		if !strings.Contains(indexDef, part) {
			t.Fatalf("sweep index definition %q does not contain %q", indexDef, part)
		}
	}
	var oldIndexCount int
	if err := pool.QueryRow(
		ctx,
		`SELECT count(*) FROM pg_indexes
		 WHERE schemaname = current_schema()
		   AND indexname = 'recharge_orders_status_idx'`,
	).Scan(&oldIndexCount); err != nil {
		t.Fatalf("query old recharge index: %v", err)
	}
	if oldIndexCount != 0 {
		t.Fatalf("recharge_orders_status_idx count = %d, want 0", oldIndexCount)
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
	if err := pool.QueryRow(
		ctx,
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
		if _, err := pool.Exec(
			ctx,
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
		if err := pool.QueryRow(
			ctx,
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
