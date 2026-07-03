package app_test

import (
	"net/http"
	"testing"
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
