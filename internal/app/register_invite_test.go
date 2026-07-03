package app_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// registerCustomer 注册一个客户并返回响应体（要求 201）。
func registerCustomer(t *testing.T, username, inviteCode string) map[string]any {
	t.Helper()
	req := map[string]any{"username": username, "password": "secret123"}
	if inviteCode != "" {
		req["invite_code"] = inviteCode
	}
	resp, body := postJSON(t, "/api/v1/auth/register", req, "")
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("register %s: status = %d, body = %v", username, resp.StatusCode, body)
	}
	return body
}

// customerRow 从数据库读取邀请关系的可观察结果（PRD 约定：无对应 API 前走 DB 观察）。
func customerRow(t *testing.T, username string) (id int64, inviterID *int64, ancestors []int64, inviteCount int32) {
	t.Helper()
	err := testPool.QueryRow(
		context.Background(),
		`SELECT id, inviter_id, ancestors, invite_count FROM customers WHERE username = $1`, username,
	).Scan(&id, &inviterID, &ancestors, &inviteCount)
	if err != nil {
		t.Fatalf("query customer %s: %v", username, err)
	}
	return id, inviterID, ancestors, inviteCount
}

func TestRegisterWithInviteCodeBuildsInviteTree(t *testing.T) {
	// 三级链：A ← B ← C，钉死祖先路径语义（自根到父，不含自身）
	nameA, nameB, nameC := uniqueUsername(t), uniqueUsername(t), uniqueUsername(t)

	bodyA := registerCustomer(t, nameA, "")
	codeA, _ := bodyA["invite_code"].(string)

	bodyB := registerCustomer(t, nameB, codeA)
	codeB, _ := bodyB["invite_code"].(string)

	registerCustomer(t, nameC, codeB)

	idA, inviterA, ancestorsA, inviteCountA := customerRow(t, nameA)
	idB, inviterB, ancestorsB, inviteCountB := customerRow(t, nameB)
	_, inviterC, ancestorsC, inviteCountC := customerRow(t, nameC)

	if inviterA != nil {
		t.Errorf("A.inviter_id = %v, want NULL (natural registration)", *inviterA)
	}
	if len(ancestorsA) != 0 {
		t.Errorf("A.ancestors = %v, want empty", ancestorsA)
	}
	if inviterB == nil || *inviterB != idA {
		t.Errorf("B.inviter_id = %v, want %d", inviterB, idA)
	}
	if diff := cmp.Diff([]int64{idA}, ancestorsB); diff != "" {
		t.Errorf("B.ancestors mismatch (-want +got):\n%s", diff)
	}
	if inviterC == nil || *inviterC != idB {
		t.Errorf("C.inviter_id = %v, want %d", inviterC, idB)
	}
	if diff := cmp.Diff([]int64{idA, idB}, ancestorsC); diff != "" {
		t.Errorf("C.ancestors mismatch (-want +got):\n%s", diff)
	}

	// 总邀请数：注册即 +1（不论被邀请人是否有效）
	if inviteCountA != 1 {
		t.Errorf("A.invite_count = %d, want 1", inviteCountA)
	}
	if inviteCountB != 1 {
		t.Errorf("B.invite_count = %d, want 1", inviteCountB)
	}
	if inviteCountC != 0 {
		t.Errorf("C.invite_count = %d, want 0", inviteCountC)
	}
}

func TestRegisterFailurePaths(t *testing.T) {
	existing := uniqueUsername(t)
	registerCustomer(t, existing, "")

	tests := []struct {
		name       string
		body       map[string]any
		wantStatus int
		wantCode   string
	}{
		{
			name:       "username_taken",
			body:       map[string]any{"username": existing, "password": "secret123"},
			wantStatus: http.StatusConflict,
			wantCode:   "CUSTOMER_USERNAME_TAKEN",
		},
		{
			name:       "invalid_invite_code",
			body:       map[string]any{"username": uniqueUsername(t), "password": "secret123", "invite_code": "ZZZZZZZZ"},
			wantStatus: http.StatusBadRequest,
			wantCode:   "CUSTOMER_INVITE_CODE_INVALID",
		},
		{
			name:       "password_too_short",
			body:       map[string]any{"username": uniqueUsername(t), "password": "abc"},
			wantStatus: http.StatusBadRequest,
			wantCode:   "COMMON_INVALID_ARGUMENT",
		},
		{
			name:       "bad_username_chars",
			body:       map[string]any{"username": "no spaces!", "password": "secret123"},
			wantStatus: http.StatusBadRequest,
			wantCode:   "COMMON_INVALID_ARGUMENT",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, body := postJSON(t, "/api/v1/auth/register", tt.body, "")
			if resp.StatusCode != tt.wantStatus {
				t.Fatalf("status = %d, want %d, body = %v", resp.StatusCode, tt.wantStatus, body)
			}
			if got := body["code"]; got != tt.wantCode {
				t.Errorf("code = %v, want %s", got, tt.wantCode)
			}
			if msg, _ := body["message"].(string); msg == "" {
				t.Errorf("message is empty, want human-readable text")
			}
		})
	}
}
