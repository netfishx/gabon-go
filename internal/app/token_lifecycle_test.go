package app_test

import (
	"context"
	"net/http"
	"testing"
)

func TestChangePasswordInvalidatesOldTokens(t *testing.T) {
	username := uniqueUsername(t)
	registerCustomer(t, username, "")
	oldToken := loginCustomer(t, username, "secret123")

	resp, body := postJSON(t, "/api/v1/me/password", map[string]any{
		"old_password": "secret123",
		"new_password": "newpass456",
	}, oldToken)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("change password: status = %d, body = %v", resp.StatusCode, body)
	}

	// 旧 token 立即失效
	resp, body = getJSON(t, "/api/v1/me", oldToken)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("old token after change: status = %d, want 401, body = %v", resp.StatusCode, body)
	}

	// 旧密码不可再登录，新密码可以
	resp, _ = postJSON(t, "/api/v1/auth/login", map[string]any{
		"username": username, "password": "secret123",
	}, "")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("old password login: status = %d, want 401", resp.StatusCode)
	}
	newToken := loginCustomer(t, username, "newpass456")
	if resp, _ := getJSON(t, "/api/v1/me", newToken); resp.StatusCode != http.StatusOK {
		t.Errorf("new token /me: status = %d, want 200", resp.StatusCode)
	}
}

func TestChangePasswordRequiresOldPassword(t *testing.T) {
	username := uniqueUsername(t)
	registerCustomer(t, username, "")
	token := loginCustomer(t, username, "secret123")

	resp, body := postJSON(t, "/api/v1/me/password", map[string]any{
		"old_password": "wrong-old",
		"new_password": "newpass456",
	}, token)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401, body = %v", resp.StatusCode, body)
	}
	if body["code"] != "AUTH_BAD_CREDENTIALS" {
		t.Errorf("code = %v, want AUTH_BAD_CREDENTIALS", body["code"])
	}
}

func TestRefreshToken(t *testing.T) {
	username := uniqueUsername(t)
	registerCustomer(t, username, "")
	token := loginCustomer(t, username, "secret123")

	resp, body := postJSON(t, "/api/v1/auth/refresh", nil, token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("refresh: status = %d, body = %v", resp.StatusCode, body)
	}
	newToken, _ := body["token"].(string)
	if newToken == "" {
		t.Fatalf("refresh: empty token, body = %v", body)
	}
	if resp, _ := getJSON(t, "/api/v1/me", newToken); resp.StatusCode != http.StatusOK {
		t.Errorf("refreshed token /me: status = %d, want 200", resp.StatusCode)
	}
}

func TestBannedCustomerRejectedImmediately(t *testing.T) {
	username := uniqueUsername(t)
	registerCustomer(t, username, "")
	token := loginCustomer(t, username, "secret123")

	// 封禁操作的 admin API 属 M7，此处直接翻转状态（PRD 约定的 DB 观察/操纵点）
	if _, err := testPool.Exec(context.Background(),
		`UPDATE customers SET status = 'banned' WHERE username = $1`, username); err != nil {
		t.Fatalf("ban customer: %v", err)
	}

	// 既有 token 立即被拒
	resp, body := getJSON(t, "/api/v1/me", token)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("banned /me: status = %d, want 403, body = %v", resp.StatusCode, body)
	}
	if body["code"] != "CUSTOMER_BANNED" {
		t.Errorf("code = %v, want CUSTOMER_BANNED", body["code"])
	}

	// 重新登录同样被拒
	resp, body = postJSON(t, "/api/v1/auth/login", map[string]any{
		"username": username, "password": "secret123",
	}, "")
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("banned login: status = %d, want 403, body = %v", resp.StatusCode, body)
	}
}
