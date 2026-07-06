package app_test

import (
	"context"
	"net/http"
	"testing"
	"time"
)

// approveVideoOf 为该客户直插一条待审核视频并走 admin 审核通过（作者 video_count+1）。
func approveVideoOf(t *testing.T, username string) {
	t.Helper()
	adminToken := loginAdmin(t)
	publicID, _ := stagePendingVideo(t, username)
	resp, body := postJSON(t, "/admin/v1/videos/"+publicID+"/approve", nil, adminToken)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("approve %s: status = %d, body = %v", publicID, resp.StatusCode, body)
	}
}

// bindPhone 给客户绑定一个唯一手机号（凑"有联系方式"）。
func bindPhone(t *testing.T, token string) {
	t.Helper()
	resp, body := doJSON(t, http.MethodPatch, "/api/v1/me/profile", token, map[string]any{
		"phone": uniquePhone(t),
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("bind phone: status = %d, body = %v", resp.StatusCode, body)
	}
}

// assertValid 断言 /me 的有效用户状态。
func assertValid(t *testing.T, token string, want bool) map[string]any {
	t.Helper()
	resp, body := getJSON(t, "/api/v1/me", token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("me: status = %d, body = %v", resp.StatusCode, body)
	}
	if got, _ := body["valid"].(bool); got != want {
		t.Fatalf("me.valid = %v, want %v (body = %v)", body["valid"], want, body)
	}
	return body
}

// validAtOf 读 valid_at 原始时间戳（无对应 API 的中间态观察，走 DB，先例约定）。
func validAtOf(t *testing.T, username string) *time.Time {
	t.Helper()
	var ts *time.Time
	if err := testPool.QueryRow(context.Background(),
		`SELECT valid_at FROM customers WHERE username = $1`, username).Scan(&ts); err != nil {
		t.Fatalf("query valid_at: %v", err)
	}
	return ts
}

// inviteCode 从注册响应体取邀请码。
func inviteCodeOf(t *testing.T, body map[string]any) string {
	t.Helper()
	code, _ := body["invite_code"].(string)
	if code == "" {
		t.Fatalf("register body missing invite_code: %v", body)
	}
	return code
}

func TestValidUserFlipOnContact(t *testing.T) {
	// B 已有作品、有成功邀请，最后一块拼图 = 联系方式
	nameA, nameB, nameC := uniqueUsername(t), uniqueUsername(t), uniqueUsername(t)
	bodyA := registerCustomer(t, nameA, "")
	bodyB := registerCustomer(t, nameB, inviteCodeOf(t, bodyA))
	registerCustomer(t, nameC, inviteCodeOf(t, bodyB))
	tokenB := loginCustomer(t, nameB, "secret123")

	approveVideoOf(t, nameB)
	assertValid(t, tokenB, false) // 缺联系方式，不翻转

	// 补齐最后一块拼图的这次 PATCH，响应体本身必须已反映翻转（不得返回旧行快照）
	resp, patchBody := doJSON(t, http.MethodPatch, "/api/v1/me/profile", tokenB, map[string]any{
		"phone": uniquePhone(t),
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("bind phone: status = %d, body = %v", resp.StatusCode, patchBody)
	}
	if got, _ := patchBody["valid"].(bool); !got {
		t.Errorf("PATCH response valid = %v, want true（翻转须反映在本次响应中）", patchBody["valid"])
	}

	body := assertValid(t, tokenB, true)

	// 两种邀请数口径命名区分：总邀请数（注册即算）=1，有效邀请数=0（C 未有效）
	if got, _ := body["invite_count"].(float64); got != 1 {
		t.Errorf("me.invite_count = %v, want 1", body["invite_count"])
	}
	if got, _ := body["valid_invite_count"].(float64); got != 0 {
		t.Errorf("me.valid_invite_count = %v, want 0", body["valid_invite_count"])
	}
}

func TestValidUserFlipOnVideoApprove(t *testing.T) {
	// B 已有联系方式、有成功邀请，最后一块拼图 = 审核通过的作品
	nameA, nameB, nameC := uniqueUsername(t), uniqueUsername(t), uniqueUsername(t)
	bodyA := registerCustomer(t, nameA, "")
	bodyB := registerCustomer(t, nameB, inviteCodeOf(t, bodyA))
	registerCustomer(t, nameC, inviteCodeOf(t, bodyB))
	tokenB := loginCustomer(t, nameB, "secret123")

	bindPhone(t, tokenB)
	assertValid(t, tokenB, false) // 缺作品，不翻转

	approveVideoOf(t, nameB)
	assertValid(t, tokenB, true)
}

func TestValidUserFlipOnInviteeRegister(t *testing.T) {
	// B 已有作品、有联系方式，最后一块拼图 = 首个被邀请人注册
	nameA, nameB, nameC := uniqueUsername(t), uniqueUsername(t), uniqueUsername(t)
	bodyA := registerCustomer(t, nameA, "")
	bodyB := registerCustomer(t, nameB, inviteCodeOf(t, bodyA))
	tokenB := loginCustomer(t, nameB, "secret123")

	bindPhone(t, tokenB)
	approveVideoOf(t, nameB)
	assertValid(t, tokenB, false) // 缺成功邀请，不翻转

	registerCustomer(t, nameC, inviteCodeOf(t, bodyB))
	assertValid(t, tokenB, true)
}

func TestValidUserNeverReverts(t *testing.T) {
	nameA, nameB, nameC := uniqueUsername(t), uniqueUsername(t), uniqueUsername(t)
	bodyA := registerCustomer(t, nameA, "")
	bodyB := registerCustomer(t, nameB, inviteCodeOf(t, bodyA))
	registerCustomer(t, nameC, inviteCodeOf(t, bodyB))
	tokenB := loginCustomer(t, nameB, "secret123")

	approveVideoOf(t, nameB)
	bindPhone(t, tokenB)
	assertValid(t, tokenB, true)

	first := validAtOf(t, nameB)
	if first == nil {
		t.Fatal("valid_at is NULL after flip")
	}

	// 三条路径再各触发一次：状态与时间戳都不得变化
	bindPhone(t, tokenB)
	approveVideoOf(t, nameB)
	registerCustomer(t, uniqueUsername(t), inviteCodeOf(t, bodyB))

	assertValid(t, tokenB, true)
	if again := validAtOf(t, nameB); again == nil || !again.Equal(*first) {
		t.Errorf("valid_at changed: first = %v, again = %v", first, again)
	}
}
