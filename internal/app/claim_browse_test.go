package app_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/netfishx/gabon-go/internal/task"
	"github.com/netfishx/gabon-go/internal/wallet"
)

// claimGroupOf 从可领取列表取指定任务的分组。
func claimGroupOf(t *testing.T, token string, taskID int64) string {
	t.Helper()
	resp, body := getJSON(t, "/api/v1/claim-tasks", token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("claim task list: status = %d, body = %v", resp.StatusCode, body)
	}
	items, _ := body["items"].([]any)
	for _, it := range items {
		m, _ := it.(map[string]any)
		if int64(m["id"].(float64)) == taskID {
			g, _ := m["group"].(string)
			return g
		}
	}
	t.Fatalf("task %d not in claim list", taskID)
	return ""
}

func TestClaimTaskListGrouping(t *testing.T) {
	now := time.Now()
	claimable := stageClaimTask(t, 100, 0, now.Add(-time.Hour), now.Add(time.Hour))
	vipGated := stageClaimTask(t, 100, 3, now.Add(-time.Hour), now.Add(time.Hour))
	windowClosed := stageClaimTask(t, 100, 0, now.Add(-2*time.Hour), now.Add(-time.Hour))
	claimed := stageClaimTask(t, 100, 0, now.Add(-time.Hour), now.Add(time.Hour))

	username := uniqueUsername(t)
	registerCustomer(t, username, "")
	token := loginCustomer(t, username, "secret123")

	claimTask(t, token, claimed) // 先领一个

	if g := claimGroupOf(t, token, claimable); g != "claimable" {
		t.Errorf("claimable task group = %s, want claimable", g)
	}
	if g := claimGroupOf(t, token, vipGated); g != "not_claimable" {
		t.Errorf("vip-gated task group = %s, want not_claimable", g)
	}
	if g := claimGroupOf(t, token, windowClosed); g != "not_claimable" {
		t.Errorf("window-closed task group = %s, want not_claimable", g)
	}
	if g := claimGroupOf(t, token, claimed); g != "claimed" {
		t.Errorf("claimed task group = %s, want claimed", g)
	}

	// 分组排序：claimable 在 claimed 之前，claimed 在 not_claimable 之前
	resp, body := getJSON(t, "/api/v1/claim-tasks", token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list: status = %d", resp.StatusCode)
	}
	items, _ := body["items"].([]any)
	order := map[string]int{"claimable": 0, "claimed": 1, "not_claimable": 2}
	last := -1
	for _, it := range items {
		m, _ := it.(map[string]any)
		g, _ := m["group"].(string)
		if order[g] < last {
			t.Errorf("group %s out of order (last group order %d)", g, last)
		}
		last = order[g]
	}
}

func TestClaimTaskDetail(t *testing.T) {
	now := time.Now()
	taskID := stageClaimTask(t, 250, 1, now.Add(-time.Hour), now.Add(time.Hour))
	username := uniqueUsername(t)
	registerCustomer(t, username, "")
	token := loginCustomer(t, username, "secret123")

	resp, body := getJSON(t, fmt.Sprintf("/api/v1/claim-tasks/%d", taskID), token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("detail: status = %d, body = %v", resp.StatusCode, body)
	}
	if got, _ := body["reward"].(float64); int64(got) != 250 {
		t.Errorf("reward = %v, want 250", body["reward"])
	}
	if got, _ := body["min_vip_level"].(float64); int64(got) != 1 {
		t.Errorf("min_vip_level = %v, want 1", body["min_vip_level"])
	}
	if body["requirement"] != "要求" || body["flow"] != "步骤" {
		t.Errorf("detail missing requirement/flow: %v", body)
	}
	if d, _ := body["deadline"].(string); d == "" {
		t.Errorf("detail missing deadline (US12): %v", body)
	}
	if body["claim_status"] != nil {
		t.Errorf("claim_status = %v, want null (not claimed)", body["claim_status"])
	}
}

func TestMyClaimsTabs(t *testing.T) {
	now := time.Now()
	doingTask := stageClaimTask(t, 100, 0, now.Add(-time.Hour), now.Add(time.Hour))
	doneTask := stageClaimTask(t, 100, 0, now.Add(-time.Hour), now.Add(time.Hour))

	username := uniqueUsername(t)
	registerCustomer(t, username, "")
	token := loginCustomer(t, username, "secret123")
	adminToken := loginAdmin(t)

	claimTask(t, token, doingTask) // 停在 claimed（进行中）

	doneClaim := claimTask(t, token, doneTask)
	postJSON(t, fmt.Sprintf("/api/v1/claim-tasks/claims/%d/submit", doneClaim),
		map[string]any{"proof_images": []string{uploadProof(t, token)}}, token)
	postJSON(t, fmt.Sprintf("/admin/v1/claim-tasks/claims/%d/approve", doneClaim), nil, adminToken) // rewarded（已完成）

	// doing tab 含 doingTask，不含已完成
	_, body := getJSON(t, "/api/v1/claim-tasks/mine?tab=doing", token)
	if !myClaimsContainsTask(body, "claimed") {
		t.Errorf("doing tab missing claimed record: %v", body)
	}
	if myClaimsContainsStatus(body, "rewarded") {
		t.Errorf("doing tab should not contain rewarded record")
	}

	// done tab 含 rewarded
	_, body = getJSON(t, "/api/v1/claim-tasks/mine?tab=done", token)
	if !myClaimsContainsStatus(body, "rewarded") {
		t.Errorf("done tab missing rewarded record: %v", body)
	}
	if myClaimsContainsStatus(body, "claimed") {
		t.Errorf("done tab should not contain claimed record")
	}
}

func myClaimsContainsStatus(body map[string]any, status string) bool {
	items, _ := body["items"].([]any)
	for _, it := range items {
		m, _ := it.(map[string]any)
		if m["status"] == status {
			return true
		}
	}
	return false
}

func myClaimsContainsTask(body map[string]any, status string) bool {
	return myClaimsContainsStatus(body, status)
}

func TestClaimExpiry(t *testing.T) {
	now := time.Now()
	taskID := stageClaimTask(t, 100, 0, now.Add(-time.Hour), now.Add(time.Hour))
	username := uniqueUsername(t)
	registerCustomer(t, username, "")
	token := loginCustomer(t, username, "secret123")
	adminToken := loginAdmin(t)

	// 造四态各一：claimed / submitted / rejected / rewarded
	claimedID := claimTask(t, token, taskID)

	u2 := uniqueUsername(t)
	registerCustomer(t, u2, "")
	tok2 := loginCustomer(t, u2, "secret123")
	submittedID := claimTask(t, tok2, taskID)
	postJSON(t, fmt.Sprintf("/api/v1/claim-tasks/claims/%d/submit", submittedID),
		map[string]any{"proof_images": []string{uploadProof(t, tok2)}}, tok2)

	u3 := uniqueUsername(t)
	registerCustomer(t, u3, "")
	tok3 := loginCustomer(t, u3, "secret123")
	rejectedID := claimTask(t, tok3, taskID)
	postJSON(t, fmt.Sprintf("/api/v1/claim-tasks/claims/%d/submit", rejectedID),
		map[string]any{"proof_images": []string{uploadProof(t, tok3)}}, tok3)
	postJSON(t, fmt.Sprintf("/admin/v1/claim-tasks/claims/%d/reject", rejectedID),
		map[string]any{"remark": "no"}, adminToken)

	u4 := uniqueUsername(t)
	registerCustomer(t, u4, "")
	tok4 := loginCustomer(t, u4, "secret123")
	rewardedID := claimTask(t, tok4, taskID)
	postJSON(t, fmt.Sprintf("/api/v1/claim-tasks/claims/%d/submit", rewardedID),
		map[string]any{"proof_images": []string{uploadProof(t, tok4)}}, tok4)
	postJSON(t, fmt.Sprintf("/admin/v1/claim-tasks/claims/%d/approve", rewardedID), nil, adminToken)

	// 四态已就位（claimed/submitted/rejected/rewarded）；全部 backdate 到期后跑过期逻辑
	if _, err := testPool.Exec(context.Background(),
		`UPDATE task_claims SET expires_at = now() - interval '1 hour' WHERE task_id = $1`, taskID); err != nil {
		t.Fatalf("backdate expiry: %v", err)
	}
	expireClaimsViaService(t)

	assertClaimStatus(t, claimedID, "expired")
	assertClaimStatus(t, rejectedID, "expired")
	assertClaimStatus(t, submittedID, "submitted") // 待审核豁免
	assertClaimStatus(t, rewardedID, "rewarded")   // 已发奖终态不动
}

func assertClaimStatus(t *testing.T, claimID int64, want string) {
	t.Helper()
	var got string
	if err := testPool.QueryRow(context.Background(),
		`SELECT status FROM task_claims WHERE id = $1`, claimID).Scan(&got); err != nil {
		t.Fatalf("query claim %d status: %v", claimID, err)
	}
	if got != want {
		t.Errorf("claim %d status = %s, want %s", claimID, got, want)
	}
}

// expireClaimsViaService 走真实服务缝触发过期逻辑（不等 cron 真实 5 分钟）。
func expireClaimsViaService(t *testing.T) {
	t.Helper()
	svc := task.NewService(testPool, wallet.NewService(testPool))
	if _, err := svc.ExpireClaims(context.Background()); err != nil {
		t.Fatalf("expire claims: %v", err)
	}
}

func TestHiddenClaimTaskDetail(t *testing.T) {
	now := time.Now()
	taskID := stageClaimTask(t, 100, 0, now.Add(-time.Hour), now.Add(time.Hour))

	claimant := uniqueUsername(t)
	registerCustomer(t, claimant, "")
	claimantTok := loginCustomer(t, claimant, "secret123")
	claimTask(t, claimantTok, taskID) // 领取者留下历史

	// 下架该任务
	if _, err := testPool.Exec(context.Background(),
		`UPDATE claim_tasks SET enabled = false WHERE id = $1`, taskID); err != nil {
		t.Fatalf("disable task: %v", err)
	}

	// 从未领取的路人猜 id 查详情 → 404（不泄露已下架定义）
	stranger := uniqueUsername(t)
	registerCustomer(t, stranger, "")
	strangerTok := loginCustomer(t, stranger, "secret123")
	resp, body := getJSON(t, fmt.Sprintf("/api/v1/claim-tasks/%d", taskID), strangerTok)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("stranger on hidden task: status = %d, want 404, body = %v", resp.StatusCode, body)
	}

	// 领取者仍可看历史详情（200）
	resp, _ = getJSON(t, fmt.Sprintf("/api/v1/claim-tasks/%d", taskID), claimantTok)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("claimant on own hidden task: status = %d, want 200 (history visible)", resp.StatusCode)
	}
}
