package app_test

import (
	"context"
	"net/http"
	"testing"
	"time"
)

// 测试基建以 bootstrapAdminUsername/Password 引导初始管理员（见 main_test.go 配置）。

func loginAdmin(t *testing.T) string {
	t.Helper()
	resp, body := postJSON(t, "/admin/v1/auth/login", map[string]any{
		"username": bootstrapAdminUsername, "password": bootstrapAdminPassword,
	}, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("admin login: status = %d, body = %v", resp.StatusCode, body)
	}
	token, _ := body["token"].(string)
	if token == "" {
		t.Fatalf("admin login: empty token, body = %v", body)
	}
	return token
}

func TestAdminBootstrapAndLogin(t *testing.T) {
	token := loginAdmin(t)

	resp, body := getJSON(t, "/admin/v1/me", token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("admin /me: status = %d, body = %v", resp.StatusCode, body)
	}
	if body["username"] != bootstrapAdminUsername {
		t.Errorf("username = %v, want %s", body["username"], bootstrapAdminUsername)
	}
	if body["role"] != "admin" {
		t.Errorf("role = %v, want admin (bootstrap admin gets full role)", body["role"])
	}

	t.Run("bad_credentials", func(t *testing.T) {
		resp, body := postJSON(t, "/admin/v1/auth/login", map[string]any{
			"username": bootstrapAdminUsername, "password": "wrong",
		}, "")
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401, body = %v", resp.StatusCode, body)
		}
		if body["code"] != "AUTH_BAD_CREDENTIALS" {
			t.Errorf("code = %v, want AUTH_BAD_CREDENTIALS", body["code"])
		}
	})
}

func TestAudienceIsolation(t *testing.T) {
	username := uniqueUsername(t)
	registerCustomer(t, username, "")
	customerToken := loginCustomer(t, username, "secret123")
	adminToken := loginAdmin(t)

	// 客户 token 打后台 → 401；后台 token 打客户面 → 401
	if resp, body := getJSON(t, "/admin/v1/me", customerToken); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("customer token on /admin/v1/me: status = %d, want 401, body = %v", resp.StatusCode, body)
	}
	if resp, body := getJSON(t, "/api/v1/me", adminToken); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("admin token on /api/v1/me: status = %d, want 401, body = %v", resp.StatusCode, body)
	}
}

func TestAuthenticatedRequestRecordsDailyActive(t *testing.T) {
	username := uniqueUsername(t)
	registerCustomer(t, username, "")
	token := loginCustomer(t, username, "secret123")

	if resp, _ := getJSON(t, "/api/v1/me", token); resp.StatusCode != http.StatusOK {
		t.Fatalf("/me failed")
	}

	// active_date 按 Asia/Shanghai 记天
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}
	today := time.Now().In(loc).Format("2006-01-02")

	var count int
	err = testPool.QueryRow(
		context.Background(),
		`SELECT count(*) FROM daily_actives d
		 JOIN customers c ON c.id = d.customer_id
		 WHERE c.username = $1 AND d.active_date = $2`, username, today,
	).Scan(&count)
	if err != nil {
		t.Fatalf("query daily_actives: %v", err)
	}
	if count != 1 {
		t.Errorf("daily_actives rows = %d, want 1", count)
	}

	// 再次请求不重复计行
	if resp, _ := getJSON(t, "/api/v1/me", token); resp.StatusCode != http.StatusOK {
		t.Fatalf("/me second call failed")
	}
	if err := testPool.QueryRow(
		context.Background(),
		`SELECT count(*) FROM daily_actives d
		 JOIN customers c ON c.id = d.customer_id WHERE c.username = $1`, username,
	).Scan(&count); err != nil {
		t.Fatalf("recount daily_actives: %v", err)
	}
	if count != 1 {
		t.Errorf("daily_actives rows after second request = %d, want 1", count)
	}
}
