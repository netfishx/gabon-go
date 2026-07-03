package app_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/netfishx/gabon-go/internal/db"
	"github.com/netfishx/gabon-go/internal/wallet"
)

// 流水明细分页（issue #7）：用钱包原语造数据（不手插表），验证游标语义不重不漏。

func TestWalletTransactionsPagination(t *testing.T) {
	username := uniqueUsername(t)
	registerCustomer(t, username, "")
	token := loginCustomer(t, username, "secret123")

	var customerID int64
	if err := testPool.QueryRow(context.Background(),
		`SELECT id FROM customers WHERE username = $1`, username).Scan(&customerID); err != nil {
		t.Fatalf("query customer id: %v", err)
	}

	// 造 5 笔流水：3 入账 + 冻结（无流水）+ 结算 + 扣减
	svc := wallet.NewService(testPool)
	ctx := context.Background()
	for i, amount := range []int64{100, 200, 300} {
		ref := int64(90000 + i)
		if err := svc.Credit(ctx, wallet.CreditParams{
			CustomerID: customerID, Type: db.TransactionTypeSignInReward, Amount: amount, RefID: &ref,
		}); err != nil {
			t.Fatalf("seed credit: %v", err)
		}
	}
	if err := svc.Freeze(ctx, customerID, 150); err != nil {
		t.Fatalf("freeze: %v", err)
	}
	if err := svc.SettleFrozen(ctx, wallet.SettleParams{
		CustomerID: customerID, Type: db.TransactionTypeWithdrawal, Amount: 150,
	}); err != nil {
		t.Fatalf("settle: %v", err)
	}
	if err := svc.Debit(ctx, wallet.DebitParams{
		CustomerID: customerID, Type: db.TransactionTypeVipPurchase, Amount: 50,
	}); err != nil {
		t.Fatalf("debit: %v", err)
	}

	// 第一页 limit=2：流水号降序
	resp, body := getJSON(t, "/api/v1/wallet/transactions?limit=2", token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("page1: status = %d, body = %v", resp.StatusCode, body)
	}
	items, _ := body["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("page1 items = %d, want 2", len(items))
	}
	first := items[0].(map[string]any)
	if first["type"] != "vip_purchase" || first["amount"] != float64(-50) {
		t.Errorf("newest item = %v, want vip_purchase -50", first)
	}
	if first["balance_after"] != float64(400) {
		t.Errorf("newest balance_after = %v, want 400", first["balance_after"])
	}
	if _, ok := first["created_at"]; !ok {
		t.Errorf("item missing created_at")
	}
	cursor, _ := body["next_cursor"].(float64)
	if cursor == 0 {
		t.Fatalf("page1 next_cursor missing, body = %v", body)
	}

	// 逐页收集全部，验证不重不漏（共 5 笔：3 credit + settle + debit；freeze 无流水）
	seen := map[float64]bool{first["id"].(float64): true}
	total := len(items)
	for _, it := range items[1:] {
		seen[it.(map[string]any)["id"].(float64)] = true
	}
	for cursor != 0 {
		resp, body = getJSON(t, fmt.Sprintf("/api/v1/wallet/transactions?limit=2&cursor=%d", int64(cursor)), token)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("paging: status = %d, body = %v", resp.StatusCode, body)
		}
		items, _ = body["items"].([]any)
		for _, it := range items {
			id := it.(map[string]any)["id"].(float64)
			if seen[id] {
				t.Fatalf("duplicate item id %v across pages", id)
			}
			seen[id] = true
		}
		total += len(items)
		cursor, _ = body["next_cursor"].(float64)
	}
	if total != 5 {
		t.Errorf("total items = %d, want 5 (freeze must not appear)", total)
	}
}

func TestWalletTransactionsEmptyAndGuards(t *testing.T) {
	username := uniqueUsername(t)
	registerCustomer(t, username, "")
	token := loginCustomer(t, username, "secret123")

	t.Run("empty_wallet", func(t *testing.T) {
		resp, body := getJSON(t, "/api/v1/wallet/transactions", token)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, body = %v", resp.StatusCode, body)
		}
		if items, _ := body["items"].([]any); len(items) != 0 {
			t.Errorf("items = %v, want empty", items)
		}
		if _, has := body["next_cursor"]; has {
			t.Errorf("next_cursor present on empty result, body = %v", body)
		}
	})

	t.Run("limit_clamped", func(t *testing.T) {
		resp, _ := getJSON(t, "/api/v1/wallet/transactions?limit=99999", token)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d", resp.StatusCode)
		}
	})

	t.Run("without_token", func(t *testing.T) {
		resp, _ := getJSON(t, "/api/v1/wallet/transactions", "")
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", resp.StatusCode)
		}
	})
}
