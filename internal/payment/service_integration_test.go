package payment

import (
	"context"
	"testing"

	"github.com/netfishx/gabon-go/internal/db"
	"github.com/netfishx/gabon-go/internal/testdb"
	"github.com/netfishx/gabon-go/internal/wallet"
)

// P3 回归：快速异步回调抢先把订单落终态后，建单流程迟到的 provider 信息回填
// （SetRechargeProviderInfo）不得把过期的 pending provider_status 盖回终态订单。
func TestSetProviderInfoSkipsTerminalOrder(t *testing.T) {
	ctx := context.Background()
	pool, cleanup, err := testdb.Start(ctx)
	if err != nil {
		t.Fatalf("start testdb: %v", err)
	}
	t.Cleanup(cleanup)

	var customerID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO customers (public_id, username, password_hash, invite_code)
		 VALUES ('payp3000001', 'pay_p3', 'not-a-real-hash', 'PYP30001') RETURNING id`,
	).Scan(&customerID); err != nil {
		t.Fatalf("stage customer: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO wallets (customer_id) VALUES ($1)`, customerID); err != nil {
		t.Fatalf("stage wallet: %v", err)
	}

	registry, err := NewRegistry(NewMockProvider())
	if err != nil {
		t.Fatalf("registry: %v", err)
	}
	svc := NewService(pool, wallet.NewService(pool), registry, "")
	order, _, err := svc.CreateRechargeOrder(ctx, customerID, 5000, "mock")
	if err != nil {
		t.Fatalf("create recharge order: %v", err)
	}

	q := db.New(pool)
	// 回调抢先落终态：succeeded + provider_status='success'。
	successStatus := "success"
	if _, err := q.MarkRechargeSucceeded(ctx, db.MarkRechargeSucceededParams{
		ID: order.ID, ProviderStatus: &successStatus,
	}); err != nil {
		t.Fatalf("mark succeeded: %v", err)
	}

	// 建单流程迟到的回填带过期 pending 状态——守卫应使其对终态订单变 no-op。
	stale, pon := "pending", "MOCK-late"
	if err := q.SetRechargeProviderInfo(ctx, db.SetRechargeProviderInfoParams{
		ID: order.ID, ProviderOrderNo: &pon, ProviderStatus: &stale,
	}); err != nil {
		t.Fatalf("set provider info: %v", err)
	}

	got, err := q.GetRechargeOrderByOrderNo(ctx, order.OrderNo)
	if err != nil {
		t.Fatalf("get order: %v", err)
	}
	if got.Status != db.RechargeOrderStatusSucceeded {
		t.Fatalf("status = %s, want succeeded", got.Status)
	}
	if got.ProviderStatus == nil || *got.ProviderStatus != "success" {
		t.Fatalf("provider_status = %v, want success (terminal status must not be overwritten)", got.ProviderStatus)
	}
}
