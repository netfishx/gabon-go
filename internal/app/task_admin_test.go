package app_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"
)

// ---- 周期任务定义 CRUD ----

func TestPeriodicTaskDefinitionCRUD(t *testing.T) {
	adminToken := loginAdmin(t)

	// 新增
	resp, body := postJSON(t, "/admin/v1/periodic-tasks", map[string]any{
		"name": "每周分享", "category": "share_video", "period": "weekly",
		"target": 3, "reward": 200, "display_order": 9,
	}, adminToken)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create periodic task: status = %d, body = %v", resp.StatusCode, body)
	}
	id, _ := body["id"].(float64)
	if id == 0 {
		t.Fatalf("create: missing id, body = %v", body)
	}
	taskID := int64(id)

	// 列表含新建项
	resp, body = getJSON(t, "/admin/v1/periodic-tasks", adminToken)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list periodic tasks: status = %d", resp.StatusCode)
	}
	if !adminListContainsID(body, taskID) {
		t.Errorf("periodic task %d not in admin list", taskID)
	}

	// 编辑奖励
	resp, body = doJSON(t, http.MethodPatch, fmt.Sprintf("/admin/v1/periodic-tasks/%d", taskID),
		adminToken, map[string]any{"reward": 300})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("update periodic task: status = %d, body = %v", resp.StatusCode, body)
	}

	// 停用 → 客户面列表不再出现
	resp, _ = doJSON(t, http.MethodPatch, fmt.Sprintf("/admin/v1/periodic-tasks/%d", taskID),
		adminToken, map[string]any{"enabled": false})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("disable periodic task: status = %d", resp.StatusCode)
	}
	username := uniqueUsername(t)
	registerCustomer(t, username, "")
	custToken := loginCustomer(t, username, "secret123")
	_, tasks := getJSON(t, "/api/v1/tasks", custToken)
	items, _ := tasks["items"].([]any)
	for _, it := range items {
		m, _ := it.(map[string]any)
		if m["name"] == "每周分享" {
			t.Errorf("disabled periodic task still visible to customer")
		}
	}
}

// ---- 限时任务定义 CRUD ----

func TestClaimTaskDefinitionCRUD(t *testing.T) {
	adminToken := loginAdmin(t)
	now := time.Now()

	resp, body := postJSON(t, "/admin/v1/claim-tasks", map[string]any{
		"name": "注册试玩", "reward": 500, "min_vip_level": 0,
		"requirement": "完成注册", "flow": "步骤说明", "link": "https://x.test",
		"starts_at": now.Add(-time.Hour).Format(time.RFC3339),
		"ends_at":   now.Add(time.Hour).Format(time.RFC3339),
	}, adminToken)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create claim task: status = %d, body = %v", resp.StatusCode, body)
	}
	taskID := int64(body["id"].(float64))

	// 客户面可见（可领取）
	username := uniqueUsername(t)
	registerCustomer(t, username, "")
	custToken := loginCustomer(t, username, "secret123")
	if g := claimGroupOf(t, custToken, taskID); g != "claimable" {
		t.Errorf("new claim task group = %s, want claimable", g)
	}

	// 上下架切换
	resp, _ = doJSON(t, http.MethodPatch, fmt.Sprintf("/admin/v1/claim-tasks/%d/status", taskID),
		adminToken, map[string]any{"enabled": false})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("toggle status: status = %d", resp.StatusCode)
	}
	// 下架后客户面列表隐藏
	resp, list := getJSON(t, "/api/v1/claim-tasks", custToken)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("claim list: status = %d", resp.StatusCode)
	}
	if adminListContainsID(list, taskID) {
		t.Errorf("offline claim task still in customer list")
	}
}

// ---- 运营语义：延期回写 ----

func TestClaimTaskDeadlineExtensionRewritesInflight(t *testing.T) {
	adminToken := loginAdmin(t)
	now := time.Now()
	taskID := stageClaimTask(t, 100, 0, now.Add(-time.Hour), now.Add(time.Hour))

	username := uniqueUsername(t)
	registerCustomer(t, username, "")
	custToken := loginCustomer(t, username, "secret123")
	claimID := claimTask(t, custToken, taskID) // 快照 expires_at = 原 ends_at

	// 运营延期 ends_at → 在途未终态记录 expires_at 同步回写
	newEnd := now.Add(48 * time.Hour)
	resp, body := doJSON(t, http.MethodPatch, fmt.Sprintf("/admin/v1/claim-tasks/%d", taskID),
		adminToken, map[string]any{"ends_at": newEnd.Format(time.RFC3339)})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("extend deadline: status = %d, body = %v", resp.StatusCode, body)
	}

	var expiresAt time.Time
	if err := testPool.QueryRow(context.Background(),
		`SELECT expires_at FROM task_claims WHERE id = $1`, claimID).Scan(&expiresAt); err != nil {
		t.Fatalf("query expires_at: %v", err)
	}
	if expiresAt.Before(now.Add(47 * time.Hour)) {
		t.Errorf("inflight claim expires_at = %v, want rewritten to ~%v", expiresAt, newEnd)
	}
}

// ---- 运营语义：软删定义作废在途 ----

func TestClaimTaskSoftDeleteVoidsInflight(t *testing.T) {
	adminToken := loginAdmin(t)
	now := time.Now()
	taskID := stageClaimTask(t, 100, 0, now.Add(-time.Hour), now.Add(time.Hour))

	// 一个在途 claimed + 一个已 rewarded
	uInflight := uniqueUsername(t)
	registerCustomer(t, uInflight, "")
	tokInflight := loginCustomer(t, uInflight, "secret123")
	inflightClaim := claimTask(t, tokInflight, taskID)

	uDone := uniqueUsername(t)
	registerCustomer(t, uDone, "")
	tokDone := loginCustomer(t, uDone, "secret123")
	doneClaim := claimTask(t, tokDone, taskID)
	postJSON(t, fmt.Sprintf("/api/v1/claim-tasks/claims/%d/submit", doneClaim),
		map[string]any{"proof_images": []string{uploadProof(t, tokDone)}}, tokDone)
	postJSON(t, fmt.Sprintf("/admin/v1/claim-tasks/claims/%d/approve", doneClaim), nil, adminToken)

	// 软删定义
	resp, body := doJSON(t, http.MethodDelete, fmt.Sprintf("/admin/v1/claim-tasks/%d", taskID), adminToken, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("soft delete: status = %d, body = %v", resp.StatusCode, body)
	}

	assertClaimStatus(t, inflightClaim, "expired") // 在途未终态作废
	assertClaimStatus(t, doneClaim, "rewarded")    // 已发奖终态不动
}

// ---- 运营语义：下架冻结流转 ----

func TestClaimTaskOfflineFreezesFlow(t *testing.T) {
	adminToken := loginAdmin(t)
	now := time.Now()
	taskID := stageClaimTask(t, 100, 0, now.Add(-time.Hour), now.Add(time.Hour))

	username := uniqueUsername(t)
	registerCustomer(t, username, "")
	custToken := loginCustomer(t, username, "secret123")
	claimID := claimTask(t, custToken, taskID)

	// 下架
	doJSON(t, http.MethodPatch, fmt.Sprintf("/admin/v1/claim-tasks/%d/status", taskID),
		adminToken, map[string]any{"enabled": false})

	// 下架后提交被冻结
	resp, body := postJSON(t, fmt.Sprintf("/api/v1/claim-tasks/claims/%d/submit", claimID),
		map[string]any{"proof_images": []string{uploadProof(t, custToken)}}, custToken)
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("submit on offline task: status = %d, want 409, body = %v", resp.StatusCode, body)
	}
}

// ---- 快照隔离：编辑定义不改已领取奖励基数 ----

func TestClaimTaskEditKeepsClaimRewardSnapshot(t *testing.T) {
	adminToken := loginAdmin(t)
	now := time.Now()
	taskID := stageClaimTask(t, 100, 0, now.Add(-time.Hour), now.Add(time.Hour))

	username := uniqueUsername(t)
	registerCustomer(t, username, "")
	custToken := loginCustomer(t, username, "secret123")
	claimID := claimTask(t, custToken, taskID) // 快照 reward_base=100

	// 改定义奖励为 999
	doJSON(t, http.MethodPatch, fmt.Sprintf("/admin/v1/claim-tasks/%d", taskID),
		adminToken, map[string]any{"reward": 999})

	// 审核发奖仍按快照 100
	postJSON(t, fmt.Sprintf("/api/v1/claim-tasks/claims/%d/submit", claimID),
		map[string]any{"proof_images": []string{uploadProof(t, custToken)}}, custToken)
	postJSON(t, fmt.Sprintf("/admin/v1/claim-tasks/claims/%d/approve", claimID), nil, adminToken)
	if got := availableOf(t, custToken); got != 100 {
		t.Errorf("granted = %d, want 100 (claim snapshot, not edited 999)", got)
	}
}

// ---- NORMAL 角色门禁矩阵 ----

func TestTaskAdminRequiresAdminRole(t *testing.T) {
	normalToken := loginNormalAdmin(t)
	now := time.Now()
	taskID := stageClaimTask(t, 100, 0, now.Add(-time.Hour), now.Add(time.Hour))

	endpoints := []struct {
		method, path string
		body         map[string]any
	}{
		{http.MethodGet, "/admin/v1/periodic-tasks", nil},
		{http.MethodPost, "/admin/v1/periodic-tasks", map[string]any{"name": "x", "category": "like", "period": "daily", "target": 1, "reward": 1}},
		{http.MethodGet, "/admin/v1/claim-tasks", nil},
		{http.MethodPost, "/admin/v1/claim-tasks", map[string]any{"name": "x", "reward": 1}},
		{http.MethodPatch, fmt.Sprintf("/admin/v1/claim-tasks/%d", taskID), map[string]any{"reward": 2}},
		{http.MethodDelete, fmt.Sprintf("/admin/v1/claim-tasks/%d", taskID), nil},
	}
	for _, e := range endpoints {
		t.Run(e.method+e.path, func(t *testing.T) {
			resp, _ := doJSON(t, e.method, e.path, normalToken, e.body)
			if resp.StatusCode != http.StatusForbidden {
				t.Errorf("NORMAL admin %s %s: status = %d, want 403", e.method, e.path, resp.StatusCode)
			}
		})
	}
}

// adminListContainsID 检查 admin/客户列表响应含指定 id。
func adminListContainsID(body map[string]any, id int64) bool {
	items, _ := body["items"].([]any)
	for _, it := range items {
		m, _ := it.(map[string]any)
		if v, ok := m["id"].(float64); ok && int64(v) == id {
			return true
		}
	}
	return false
}
