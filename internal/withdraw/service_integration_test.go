package withdraw

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/netfishx/gabon-go/internal/apierr"
	"github.com/netfishx/gabon-go/internal/testdb"
	"github.com/netfishx/gabon-go/internal/wallet"
)

// 已在 handler 取得快照后卡被删除时，事务内复核必须在冻结前拒绝建单。
func TestCreateOrderRejectsCardDeletedAfterSnapshot(t *testing.T) {
	ctx := context.Background()
	pool, cleanup, err := testdb.Start(ctx)
	if err != nil {
		t.Fatalf("start testdb: %v", err)
	}
	t.Cleanup(cleanup)

	var customerID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO customers (public_id, username, password_hash, invite_code)
		 VALUES ('wdlock000001', 'withdraw_lock', 'not-a-real-hash', 'WDLOCK01') RETURNING id`,
	).Scan(&customerID); err != nil {
		t.Fatalf("stage customer: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO wallets (customer_id, available) VALUES ($1, 500)`, customerID); err != nil {
		t.Fatalf("stage wallet: %v", err)
	}

	var cardID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO bank_cards (customer_id, card_no, holder_name, bank_name)
		 VALUES ($1, '6222020202021010', '张三', '中国工商银行') RETURNING id`, customerID,
	).Scan(&cardID); err != nil {
		t.Fatalf("stage bank card: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE bank_cards SET deleted_at=now() WHERE id=$1`, cardID); err != nil {
		t.Fatalf("delete bank card after snapshot: %v", err)
	}

	svc := NewService(pool, wallet.NewService(pool))
	_, err = svc.CreateOrder(ctx, customerID, CreateParams{
		Amount:     100,
		BankCardID: cardID,
		Payee: PayeeSnapshot{
			Account: "6222020202021010",
			Name:    "张三",
			Bank:    "中国工商银行",
		},
	})
	var apiErr *apierr.Error
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusNotFound || apiErr.Code != apierr.CodeBankCardNotFound {
		t.Fatalf("CreateOrder error = %v, want 404 BANK_CARD_NOT_FOUND", err)
	}

	var available, frozen int64
	if err := pool.QueryRow(ctx,
		`SELECT available, frozen FROM wallets WHERE customer_id=$1`, customerID,
	).Scan(&available, &frozen); err != nil {
		t.Fatalf("query wallet: %v", err)
	}
	if available != 500 || frozen != 0 {
		t.Errorf("wallet = (%d, %d), want (500, 0)", available, frozen)
	}
	var orders int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM withdrawal_orders WHERE customer_id=$1`, customerID,
	).Scan(&orders); err != nil {
		t.Fatalf("count withdrawal orders: %v", err)
	}
	if orders != 0 {
		t.Errorf("withdrawal orders = %d, want 0", orders)
	}
}
