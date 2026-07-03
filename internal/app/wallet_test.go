package app_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/netfishx/gabon-go/internal/db"
	"github.com/netfishx/gabon-go/internal/wallet"
)

func TestWalletQuery(t *testing.T) {
	username := uniqueUsername(t)
	registerCustomer(t, username, "")
	token := loginCustomer(t, username, "secret123")

	t.Run("fresh_wallet_is_zero", func(t *testing.T) {
		resp, body := getJSON(t, "/api/v1/wallet", token)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, body = %v", resp.StatusCode, body)
		}
		if got := body["available"]; got != float64(0) {
			t.Errorf("available = %v, want 0", got)
		}
		if got := body["frozen"]; got != float64(0) {
			t.Errorf("frozen = %v, want 0", got)
		}
	})

	t.Run("without_token", func(t *testing.T) {
		resp, body := getJSON(t, "/api/v1/wallet", "")
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401, body = %v", resp.StatusCode, body)
		}
		if body["code"] != "AUTH_UNAUTHORIZED" {
			t.Errorf("code = %v, want AUTH_UNAUTHORIZED", body["code"])
		}
	})
}

// 越权不可达（issue #3 Testing Decisions）：钱包与流水的主体永远来自 token，
// 给 B 钱包充值后，A 的视图必须完全不受影响。
func TestWalletIsolationBetweenCustomers(t *testing.T) {
	nameA, nameB := uniqueUsername(t), uniqueUsername(t)
	registerCustomer(t, nameA, "")
	registerCustomer(t, nameB, "")
	tokenA := loginCustomer(t, nameA, "secret123")

	var idB int64
	if err := testPool.QueryRow(context.Background(),
		`SELECT id FROM customers WHERE username = $1`, nameB).Scan(&idB); err != nil {
		t.Fatalf("query customer B: %v", err)
	}
	if err := wallet.NewService(testPool).Credit(context.Background(), wallet.CreditParams{
		CustomerID: idB, Type: db.TransactionTypeSignInReward, Amount: 999,
	}); err != nil {
		t.Fatalf("credit B: %v", err)
	}

	resp, body := getJSON(t, "/api/v1/wallet", tokenA)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("wallet A: status = %d", resp.StatusCode)
	}
	if body["available"] != float64(0) {
		t.Errorf("A sees available = %v, want 0 (B's credit must be invisible)", body["available"])
	}
	resp, body = getJSON(t, "/api/v1/wallet/transactions", tokenA)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("transactions A: status = %d", resp.StatusCode)
	}
	if items, _ := body["items"].([]any); len(items) != 0 {
		t.Errorf("A sees %d transactions, want 0", len(items))
	}
}
