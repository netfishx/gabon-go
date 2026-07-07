package payment

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/netfishx/gabon-go/internal/db"
	"github.com/netfishx/gabon-go/internal/testdb"
)

// P3 回归：快速异步回调抢先把订单落终态（此时建单尚未回填 provider_order_no）后，
// 迟到的 SetRechargeProviderInfo 必须：
//   - 保留回调已落的 provider_status（不被过期 pending 盖回）；
//   - 仍补写缺失的 provider_order_no（否则 trace/fallback 少一个渠道单号）。
func TestSetProviderInfoBackfillsButKeepsTerminalStatus(t *testing.T) {
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

	q := db.New(pool)
	// 建单（尚未调 Pay，故 provider_order_no 为 NULL）——模拟回调抢在 persistPayResult 之前。
	providerCode := MockProviderCode
	id, err := q.InsertRechargeOrder(ctx, db.InsertRechargeOrderParams{
		CustomerID: customerID, Amount: 5000, FiatAmount: 5000, Provider: &providerCode,
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true},
	})
	if err != nil {
		t.Fatalf("insert order: %v", err)
	}
	order, err := q.FinalizeRechargeOrderNo(ctx, id)
	if err != nil {
		t.Fatalf("finalize order_no: %v", err)
	}

	// 快速回调抢先落终态：succeeded + provider_status='success'（回调按 order_no 定位，不设单号）。
	successStatus := "success"
	if _, err := q.MarkRechargeSucceeded(ctx, db.MarkRechargeSucceededParams{
		ID: id, ProviderStatus: &successStatus,
	}); err != nil {
		t.Fatalf("mark succeeded: %v", err)
	}

	// 迟到的建单回填：带真实单号 + 过期 pending 状态。
	pon, stale := "MOCK-late", "pending"
	if err := q.SetRechargeProviderInfo(ctx, db.SetRechargeProviderInfoParams{
		ID: id, ProviderOrderNo: &pon, ProviderStatus: &stale,
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
	if got.ProviderOrderNo == nil || *got.ProviderOrderNo != "MOCK-late" {
		t.Fatalf("provider_order_no = %v, want MOCK-late (must still backfill when missing)", got.ProviderOrderNo)
	}
}
