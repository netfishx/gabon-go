package app_test

import (
	"encoding/json"
	"net/http"
	"testing"
)

func decodeBody(resp *http.Response, dst any) error {
	return json.NewDecoder(resp.Body).Decode(dst)
}

// loginCustomer 登录并返回 token（要求 200）。
func loginCustomer(t *testing.T, username, password string) string {
	t.Helper()
	resp, body := postJSON(t, "/api/v1/auth/login", map[string]any{
		"username": username, "password": password,
	}, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login %s: status = %d, body = %v", username, resp.StatusCode, body)
	}
	token, _ := body["token"].(string)
	if token == "" {
		t.Fatalf("login %s: empty token, body = %v", username, body)
	}
	return token
}

func getJSON(t *testing.T, path, token string) (*http.Response, map[string]any) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, testServer.URL+path, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	var decoded map[string]any
	if err := decodeBody(resp, &decoded); err != nil {
		decoded = nil
	}
	return resp, decoded
}

func TestLogin(t *testing.T) {
	username := uniqueUsername(t)
	registerCustomer(t, username, "")

	t.Run("success", func(t *testing.T) {
		resp, body := postJSON(t, "/api/v1/auth/login", map[string]any{
			"username": username, "password": "secret123",
		}, "")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, body = %v", resp.StatusCode, body)
		}
		if token, _ := body["token"].(string); token == "" {
			t.Errorf("token missing in body %v", body)
		}
		cust, _ := body["customer"].(map[string]any)
		if cust["username"] != username {
			t.Errorf("customer.username = %v, want %s", cust["username"], username)
		}
	})

	// 凭证错误统一返回 AUTH_BAD_CREDENTIALS，不泄露用户名是否存在
	for name, creds := range map[string]map[string]any{
		"wrong_password": {"username": username, "password": "wrong-pass"},
		"unknown_user":   {"username": "nobody_here_404", "password": "secret123"},
	} {
		t.Run(name, func(t *testing.T) {
			resp, body := postJSON(t, "/api/v1/auth/login", creds, "")
			if resp.StatusCode != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401, body = %v", resp.StatusCode, body)
			}
			if body["code"] != "AUTH_BAD_CREDENTIALS" {
				t.Errorf("code = %v, want AUTH_BAD_CREDENTIALS", body["code"])
			}
		})
	}
}

func TestMe(t *testing.T) {
	username := uniqueUsername(t)
	created := registerCustomer(t, username, "")
	token := loginCustomer(t, username, "secret123")

	t.Run("without_token", func(t *testing.T) {
		resp, body := getJSON(t, "/api/v1/me", "")
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401, body = %v", resp.StatusCode, body)
		}
		if body["code"] != "AUTH_UNAUTHORIZED" {
			t.Errorf("code = %v, want AUTH_UNAUTHORIZED", body["code"])
		}
	})

	t.Run("garbage_token", func(t *testing.T) {
		resp, body := getJSON(t, "/api/v1/me", "not-a-jwt")
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401, body = %v", resp.StatusCode, body)
		}
	})

	t.Run("with_token", func(t *testing.T) {
		resp, body := getJSON(t, "/api/v1/me", token)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, body = %v", resp.StatusCode, body)
		}
		if body["username"] != username {
			t.Errorf("username = %v, want %s", body["username"], username)
		}
		if body["public_id"] != created["public_id"] {
			t.Errorf("public_id = %v, want %v", body["public_id"], created["public_id"])
		}
	})
}
