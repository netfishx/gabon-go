package app_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"
)

var usernameSeq atomic.Int64

// uniqueUsername 生成测试内唯一用户名，避免测试间数据干扰。
func uniqueUsername(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("u%d_%d", usernameSeq.Add(1), testServerPort())
}

func testServerPort() int {
	// httptest server URL 形如 http://127.0.0.1:PORT，端口参与用户名保证跨次运行也唯一性足够
	var port int
	fmt.Sscanf(testServer.URL, "http://127.0.0.1:%d", &port)
	return port
}

func postJSON(t *testing.T, path string, body any, token string) (*http.Response, map[string]any) {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, testServer.URL+path, bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	t.Cleanup(func() { resp.Body.Close() })

	var decoded map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		decoded = nil // 空 body 或非 JSON，由断言方处理
	}
	return resp, decoded
}

func TestRegisterWithoutInviteCode(t *testing.T) {
	username := uniqueUsername(t)

	resp, body := postJSON(t, "/api/v1/auth/register", map[string]any{
		"username": username,
		"password": "secret123",
	}, "")

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201, body = %v", resp.StatusCode, body)
	}
	if got := body["username"]; got != username {
		t.Errorf("username = %v, want %s", got, username)
	}
	publicID, _ := body["public_id"].(string)
	if len(publicID) != 12 {
		t.Errorf("public_id = %q, want 12-char short code", publicID)
	}
	inviteCode, _ := body["invite_code"].(string)
	if len(inviteCode) != 8 {
		t.Errorf("invite_code = %q, want 8-char code", inviteCode)
	}
	if _, exposed := body["id"]; exposed {
		t.Errorf("response exposes internal id, want public_id only")
	}

	// 注册即建零余额钱包（PRD 约定的数据库可观察结果，钱包 API 属 M2）
	var available, frozen int64
	err := testPool.QueryRow(
		context.Background(),
		`SELECT w.available, w.frozen FROM wallets w
		 JOIN customers c ON c.id = w.customer_id WHERE c.username = $1`, username,
	).Scan(&available, &frozen)
	if err != nil {
		t.Fatalf("query wallet row: %v", err)
	}
	if available != 0 || frozen != 0 {
		t.Errorf("wallet = (%d, %d), want (0, 0)", available, frozen)
	}
}
