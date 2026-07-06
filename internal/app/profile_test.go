package app_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// registerAndLogin 注册并登录一个客户，返回 token。
func registerAndLogin(t *testing.T, username string) string {
	t.Helper()
	registerCustomer(t, username, "")
	return loginCustomer(t, username, "secret123")
}

// uniquePhone 生成测试内唯一的合法手机号（138 段 + 8 位序列）。
func uniquePhone(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("138%08d", usernameSeq.Add(1))
}

func TestUpdateProfile(t *testing.T) {
	username := uniqueUsername(t)
	token := registerAndLogin(t, username)
	phone := uniquePhone(t)

	// 全字段更新：email 归一为小写
	resp, body := doJSON(t, http.MethodPatch, "/api/v1/me/profile", token, map[string]any{
		"name":      "小王",
		"signature": "hello",
		"email":     "Foo." + username + "@Example.COM",
		"phone":     phone,
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("update profile: status = %d, body = %v", resp.StatusCode, body)
	}
	wantEmail := "foo." + username + "@example.com"
	if got := body["email"]; got != wantEmail {
		t.Errorf("email = %v, want %s (lowercased)", got, wantEmail)
	}

	// /me 反映变更
	_, me := getJSON(t, "/api/v1/me", token)
	if got := me["name"]; got != "小王" {
		t.Errorf("me.name = %v, want 小王", got)
	}
	if got := me["signature"]; got != "hello" {
		t.Errorf("me.signature = %v, want hello", got)
	}
	if got := me["email"]; got != wantEmail {
		t.Errorf("me.email = %v, want %s", got, wantEmail)
	}
	if got := me["phone"]; got != phone {
		t.Errorf("me.phone = %v, want %s", got, phone)
	}

	// 部分更新：仅提交 name，其余字段保持不变
	resp, body = doJSON(t, http.MethodPatch, "/api/v1/me/profile", token, map[string]any{
		"name": "老王",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("partial update: status = %d, body = %v", resp.StatusCode, body)
	}
	_, me = getJSON(t, "/api/v1/me", token)
	if got := me["name"]; got != "老王" {
		t.Errorf("me.name = %v, want 老王", got)
	}
	if got := me["phone"]; got != phone {
		t.Errorf("me.phone after partial update = %v, want %s (unchanged)", got, phone)
	}
}

func TestUpdateProfileValidation(t *testing.T) {
	username := uniqueUsername(t)
	token := registerAndLogin(t, username)

	tests := []struct {
		name string
		body map[string]any
	}{
		{"phone_too_short", map[string]any{"phone": "1381234"}},
		{"phone_bad_prefix", map[string]any{"phone": "12812345678"}},
		{"phone_not_digits", map[string]any{"phone": "1381234567a"}},
		{"phone_empty", map[string]any{"phone": ""}},
		{"email_no_at", map[string]any{"email": "not-an-email"}},
		{"email_empty", map[string]any{"email": ""}},
		{"name_empty", map[string]any{"name": ""}},
		{"name_too_long", map[string]any{"name": strings.Repeat("名", 51)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, body := doJSON(t, http.MethodPatch, "/api/v1/me/profile", token, tt.body)
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400, body = %v", resp.StatusCode, body)
			}
			if got := body["code"]; got != "COMMON_INVALID_ARGUMENT" {
				t.Errorf("code = %v, want COMMON_INVALID_ARGUMENT", got)
			}
		})
	}

	// 修正旧版正则遗漏：16x / 19x 号段必须通过
	for _, phone := range []string{"16712345678", "19912345678"} {
		t.Run("segment_"+phone[:3], func(t *testing.T) {
			owner := uniqueUsername(t)
			ownToken := registerAndLogin(t, owner)
			resp, body := doJSON(t, http.MethodPatch, "/api/v1/me/profile", ownToken, map[string]any{"phone": phone})
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("phone %s: status = %d, want 200, body = %v", phone, resp.StatusCode, body)
			}
		})
	}
}

func TestUpdateProfileContactConflict(t *testing.T) {
	first := uniqueUsername(t)
	firstToken := registerAndLogin(t, first)
	phone := uniquePhone(t)
	email := first + "@example.com"

	if resp, body := doJSON(t, http.MethodPatch, "/api/v1/me/profile", firstToken, map[string]any{
		"phone": phone, "email": email,
	}); resp.StatusCode != http.StatusOK {
		t.Fatalf("first bind: status = %d, body = %v", resp.StatusCode, body)
	}

	second := uniqueUsername(t)
	secondToken := registerAndLogin(t, second)

	resp, body := doJSON(t, http.MethodPatch, "/api/v1/me/profile", secondToken, map[string]any{"phone": phone})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("dup phone: status = %d, want 409, body = %v", resp.StatusCode, body)
	}
	if got := body["code"]; got != "CUSTOMER_PHONE_TAKEN" {
		t.Errorf("code = %v, want CUSTOMER_PHONE_TAKEN", got)
	}

	// 邮箱大小写不同也视为占用（写入侧统一小写）
	resp, body = doJSON(t, http.MethodPatch, "/api/v1/me/profile", secondToken, map[string]any{
		"email": first + "@EXAMPLE.com",
	})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("dup email: status = %d, want 409, body = %v", resp.StatusCode, body)
	}
	if got := body["code"]; got != "CUSTOMER_EMAIL_TAKEN" {
		t.Errorf("code = %v, want CUSTOMER_EMAIL_TAKEN", got)
	}

	// 自己重复提交同一联系方式不算冲突（更新为相同值）
	if resp, body := doJSON(t, http.MethodPatch, "/api/v1/me/profile", firstToken, map[string]any{
		"phone": phone,
	}); resp.StatusCode != http.StatusOK {
		t.Fatalf("rebind own phone: status = %d, body = %v", resp.StatusCode, body)
	}
}

func TestUpdateProfileRequiresAuth(t *testing.T) {
	resp, _ := doJSON(t, http.MethodPatch, "/api/v1/me/profile", "", map[string]any{"name": "x"})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
}
