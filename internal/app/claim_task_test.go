package app_test

import (
	"context"
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/netfishx/gabon-go/internal/auth"
)

var claimSeq atomic.Int64

// stageClaimTask 直插一个限时任务定义（窗口默认包含现在），返回 id。
func stageClaimTask(t *testing.T, reward int64, minVip int, startsAt, endsAt time.Time) int64 {
	t.Helper()
	var id int64
	if err := testPool.QueryRow(
		context.Background(),
		`INSERT INTO claim_tasks (name, min_vip_level, reward, requirement, flow, starts_at, ends_at)
		 VALUES ($1, $2, $3, '要求', '步骤', $4, $5) RETURNING id`,
		fmt.Sprintf("限时任务 %d", claimSeq.Add(1)), minVip, reward, startsAt, endsAt,
	).Scan(&id); err != nil {
		t.Fatalf("stage claim task: %v", err)
	}
	return id
}

// claimTask 领取任务并从响应体取 claim_id（公开接口，不走 DB 直查）。
func claimTask(t *testing.T, token string, taskID int64) int64 {
	t.Helper()
	resp, body := postJSON(t, fmt.Sprintf("/api/v1/claim-tasks/%d/claim", taskID), nil, token)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("claim: status = %d, body = %v", resp.StatusCode, body)
	}
	id, _ := body["claim_id"].(float64)
	if id == 0 {
		t.Fatalf("claim: missing claim_id, body = %v", body)
	}
	return int64(id)
}

// uploadProof 走 L 通道真上传一张证明图，返回 storage_path。
func uploadProof(t *testing.T, token string) string {
	t.Helper()
	return createImageUpload(t, token, "proof", "png")
}

// loginNormalAdmin 直插一个 NORMAL 角色管理员并登录（校验敏感操作仅 ADMIN）。
func loginNormalAdmin(t *testing.T) string {
	t.Helper()
	username := fmt.Sprintf("normadmin_%d", claimSeq.Add(1))
	hash, err := auth.HashPassword("secret123")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if _, err := testPool.Exec(context.Background(),
		`INSERT INTO admins (username, password_hash, role) VALUES ($1, $2, 'normal')`,
		username, hash); err != nil {
		t.Fatalf("stage normal admin: %v", err)
	}
	resp, body := postJSON(t, "/admin/v1/auth/login", map[string]any{
		"username": username, "password": "secret123",
	}, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("normal admin login: status = %d, body = %v", resp.StatusCode, body)
	}
	token, _ := body["token"].(string)
	return token
}

func TestClaimTaskHappyPath(t *testing.T) {
	now := time.Now()
	taskID := stageClaimTask(t, 100, 0, now.Add(-time.Hour), now.Add(time.Hour))

	username := uniqueUsername(t)
	registerCustomer(t, username, "")
	token := loginCustomer(t, username, "secret123")
	adminToken := loginAdmin(t)

	// 领取：响应体直出 claim_id，客户据此提交证明（无需外部查库）
	claimID := claimTask(t, token, taskID)

	// 提交证明（1-9 张，本人 proofs 前缀）
	proof := uploadProof(t, token)
	resp, body := postJSON(t, fmt.Sprintf("/api/v1/claim-tasks/claims/%d/submit", claimID), map[string]any{
		"proof_text": "已完成", "proof_images": []string{proof},
	}, token)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("submit: status = %d, body = %v", resp.StatusCode, body)
	}

	// admin 审核通过 = 同步发奖（普通档倍率）
	resp, body = postJSON(t, fmt.Sprintf("/admin/v1/claim-tasks/claims/%d/approve", claimID), nil, adminToken)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("approve: status = %d, body = %v", resp.StatusCode, body)
	}
	if got := availableOf(t, token); got != 100 {
		t.Fatalf("available after approve = %d, want 100", got)
	}
	_, body = getJSON(t, "/api/v1/wallet/transactions", token)
	items, _ := body["items"].([]any)
	first, _ := items[0].(map[string]any)
	if got := first["type"]; got != "claim_task_reward" {
		t.Errorf("tx type = %v, want claim_task_reward", got)
	}

	// 重复 approve 幂等失败（状态已 rewarded）
	resp, _ = postJSON(t, fmt.Sprintf("/admin/v1/claim-tasks/claims/%d/approve", claimID), nil, adminToken)
	if resp.StatusCode == http.StatusNoContent {
		t.Errorf("second approve should fail, got 204")
	}
}

func TestClaimTaskRewardUsesReviewTimeVip(t *testing.T) {
	now := time.Now()
	taskID := stageClaimTask(t, 100, 0, now.Add(-time.Hour), now.Add(time.Hour))
	username := uniqueUsername(t)
	registerCustomer(t, username, "")
	token := loginCustomer(t, username, "secret123")
	cid := customerIDOf(t, username)
	adminToken := loginAdmin(t)

	claimID := claimTask(t, token, taskID)
	proof := uploadProof(t, token)
	postJSON(t, fmt.Sprintf("/api/v1/claim-tasks/claims/%d/submit", claimID), map[string]any{
		"proof_images": []string{proof},
	}, token)

	// 审核前升 VIP 到银牌 14000bp：倍率取审核时刻 → floor(100×1.4)=140
	if _, err := testPool.Exec(context.Background(),
		`UPDATE customers SET vip_level = 2 WHERE id = $1`, cid); err != nil {
		t.Fatalf("bump vip: %v", err)
	}
	postJSON(t, fmt.Sprintf("/admin/v1/claim-tasks/claims/%d/approve", claimID), nil, adminToken)
	if got := availableOf(t, token); got != 140 {
		t.Errorf("available = %d, want 140 (review-time vip)", got)
	}
}

func TestClaimTaskClaimGuards(t *testing.T) {
	now := time.Now()
	username := uniqueUsername(t)
	registerCustomer(t, username, "")
	token := loginCustomer(t, username, "secret123")

	t.Run("vip_gate", func(t *testing.T) {
		taskID := stageClaimTask(t, 100, 2, now.Add(-time.Hour), now.Add(time.Hour))
		resp, body := postJSON(t, fmt.Sprintf("/api/v1/claim-tasks/%d/claim", taskID), nil, token)
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("vip gate: status = %d, want 403, body = %v", resp.StatusCode, body)
		}
	})
	t.Run("window_closed", func(t *testing.T) {
		taskID := stageClaimTask(t, 100, 0, now.Add(-2*time.Hour), now.Add(-time.Hour))
		resp, body := postJSON(t, fmt.Sprintf("/api/v1/claim-tasks/%d/claim", taskID), nil, token)
		if resp.StatusCode != http.StatusConflict && resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("window closed: status = %d, want 4xx, body = %v", resp.StatusCode, body)
		}
	})
	t.Run("double_claim", func(t *testing.T) {
		taskID := stageClaimTask(t, 100, 0, now.Add(-time.Hour), now.Add(time.Hour))
		claimTask(t, token, taskID) // 首次领取拿到 201 + claim_id
		resp, _ := postJSON(t, fmt.Sprintf("/api/v1/claim-tasks/%d/claim", taskID), nil, token)
		if resp.StatusCode != http.StatusConflict {
			t.Errorf("double claim: status = %d, want 409", resp.StatusCode)
		}
	})
}

func TestClaimTaskProofGuards(t *testing.T) {
	now := time.Now()
	taskID := stageClaimTask(t, 100, 0, now.Add(-time.Hour), now.Add(time.Hour))
	username := uniqueUsername(t)
	registerCustomer(t, username, "")
	token := loginCustomer(t, username, "secret123")
	claimID := claimTask(t, token, taskID)

	other := uniqueUsername(t)
	registerCustomer(t, other, "")
	otherToken := loginCustomer(t, other, "secret123")
	foreignProof := createImageUpload(t, otherToken, "proof", "png")

	tests := []struct {
		name   string
		images []string
		want   int
	}{
		{"empty", []string{}, http.StatusBadRequest},
		{"foreign_prefix", []string{foreignProof}, http.StatusForbidden},
		{"missing_object", []string{fmt.Sprintf("proofs/%d/nonexistent.png", customerIDOf(t, username))}, http.StatusBadRequest},
		{"too_many", func() []string {
			out := make([]string, 10)
			for i := range out {
				out[i] = uploadProof(t, token)
			}
			return out
		}(), http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, body := postJSON(t, fmt.Sprintf("/api/v1/claim-tasks/claims/%d/submit", claimID),
				map[string]any{"proof_images": tt.images}, token)
			if resp.StatusCode != tt.want {
				t.Fatalf("status = %d, want %d, body = %v", resp.StatusCode, tt.want, body)
			}
		})
	}
}

func TestClaimTaskSubmittedNotResubmittable(t *testing.T) {
	now := time.Now()
	taskID := stageClaimTask(t, 100, 0, now.Add(-time.Hour), now.Add(time.Hour))
	username := uniqueUsername(t)
	registerCustomer(t, username, "")
	token := loginCustomer(t, username, "secret123")
	claimID := claimTask(t, token, taskID)

	if resp, _ := postJSON(t, fmt.Sprintf("/api/v1/claim-tasks/claims/%d/submit", claimID),
		map[string]any{"proof_images": []string{uploadProof(t, token)}}, token); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("first submit failed")
	}
	// 已 submitted：不可再提交（须等审核结果）
	resp, body := postJSON(t, fmt.Sprintf("/api/v1/claim-tasks/claims/%d/submit", claimID),
		map[string]any{"proof_images": []string{uploadProof(t, token)}}, token)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("resubmit while submitted: status = %d, want 409, body = %v", resp.StatusCode, body)
	}
	if got := body["code"]; got != "CLAIM_TASK_NOT_SUBMITTABLE" {
		t.Errorf("code = %v, want CLAIM_TASK_NOT_SUBMITTABLE", got)
	}
}

func TestClaimTaskNoReclaimAfterRejectOrExpire(t *testing.T) {
	now := time.Now()
	adminToken := loginAdmin(t)

	t.Run("after_reject", func(t *testing.T) {
		taskID := stageClaimTask(t, 100, 0, now.Add(-time.Hour), now.Add(time.Hour))
		username := uniqueUsername(t)
		registerCustomer(t, username, "")
		token := loginCustomer(t, username, "secret123")
		claimID := claimTask(t, token, taskID)
		postJSON(t, fmt.Sprintf("/api/v1/claim-tasks/claims/%d/submit", claimID),
			map[string]any{"proof_images": []string{uploadProof(t, token)}}, token)
		postJSON(t, fmt.Sprintf("/admin/v1/claim-tasks/claims/%d/reject", claimID),
			map[string]any{"remark": "no"}, adminToken)
		// 驳回后再领同一任务：唯一约束挡住
		resp, _ := postJSON(t, fmt.Sprintf("/api/v1/claim-tasks/%d/claim", taskID), nil, token)
		if resp.StatusCode != http.StatusConflict {
			t.Errorf("reclaim after reject: status = %d, want 409", resp.StatusCode)
		}
	})

	t.Run("after_expire", func(t *testing.T) {
		taskID := stageClaimTask(t, 100, 0, now.Add(-time.Hour), now.Add(time.Hour))
		username := uniqueUsername(t)
		registerCustomer(t, username, "")
		token := loginCustomer(t, username, "secret123")
		claimID := claimTask(t, token, taskID)
		// 直接把领取记录翻到 expired（过期 cron 属 #48，这里只验"过期后不可重领"）
		if _, err := testPool.Exec(context.Background(),
			`UPDATE task_claims SET status = 'expired' WHERE id = $1`, claimID); err != nil {
			t.Fatalf("expire claim: %v", err)
		}
		resp, _ := postJSON(t, fmt.Sprintf("/api/v1/claim-tasks/%d/claim", taskID), nil, token)
		if resp.StatusCode != http.StatusConflict {
			t.Errorf("reclaim after expire: status = %d, want 409", resp.StatusCode)
		}
	})
}

func TestClaimTaskRejectAndResubmit(t *testing.T) {
	now := time.Now()
	taskID := stageClaimTask(t, 100, 0, now.Add(-time.Hour), now.Add(time.Hour))
	username := uniqueUsername(t)
	registerCustomer(t, username, "")
	token := loginCustomer(t, username, "secret123")
	adminToken := loginAdmin(t)

	claimID := claimTask(t, token, taskID)
	postJSON(t, fmt.Sprintf("/api/v1/claim-tasks/claims/%d/submit", claimID),
		map[string]any{"proof_images": []string{uploadProof(t, token)}}, token)

	// 驳回理由必填
	resp, _ := postJSON(t, fmt.Sprintf("/admin/v1/claim-tasks/claims/%d/reject", claimID), map[string]any{}, adminToken)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("reject without reason: status = %d, want 400", resp.StatusCode)
	}
	resp, body := postJSON(t, fmt.Sprintf("/admin/v1/claim-tasks/claims/%d/reject", claimID),
		map[string]any{"remark": "证明不清晰"}, adminToken)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("reject: status = %d, body = %v", resp.StatusCode, body)
	}

	// 驳回后可重提（覆盖凭证回待审核），再审通过发奖
	resp, body = postJSON(t, fmt.Sprintf("/api/v1/claim-tasks/claims/%d/submit", claimID),
		map[string]any{"proof_images": []string{uploadProof(t, token)}}, token)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("resubmit: status = %d, body = %v", resp.StatusCode, body)
	}
	postJSON(t, fmt.Sprintf("/admin/v1/claim-tasks/claims/%d/approve", claimID), nil, adminToken)
	if got := availableOf(t, token); got != 100 {
		t.Errorf("available after resubmit+approve = %d, want 100", got)
	}
}

func TestReviewQueueShowsSnapshotReward(t *testing.T) {
	// 领取后运营改定义奖励：审核队列须显示领取快照值（与实际发奖口径一致），非当前定义值
	now := time.Now()
	taskID := stageClaimTask(t, 100, 0, now.Add(-time.Hour), now.Add(time.Hour))
	username := uniqueUsername(t)
	registerCustomer(t, username, "")
	token := loginCustomer(t, username, "secret123")
	adminToken := loginAdmin(t)

	claimID := claimTask(t, token, taskID) // 快照 reward_base=100
	postJSON(t, fmt.Sprintf("/api/v1/claim-tasks/claims/%d/submit", claimID),
		map[string]any{"proof_images": []string{uploadProof(t, token)}}, token)

	// 运营改定义奖励为 999（不影响已领取快照）
	if _, err := testPool.Exec(context.Background(),
		`UPDATE claim_tasks SET reward = 999 WHERE id = $1`, taskID); err != nil {
		t.Fatalf("bump task reward: %v", err)
	}

	resp, body := getJSON(t, "/admin/v1/claim-tasks/reviews", adminToken)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("review queue: status = %d, body = %v", resp.StatusCode, body)
	}
	items, _ := body["items"].([]any)
	var found bool
	for _, it := range items {
		m, _ := it.(map[string]any)
		if int64(m["claim_id"].(float64)) == claimID {
			found = true
			if r, _ := m["reward"].(float64); int64(r) != 100 {
				t.Errorf("queue reward = %v, want 100 (snapshot, not current 999)", m["reward"])
			}
		}
	}
	if !found {
		t.Fatalf("claim %d not in review queue", claimID)
	}

	// 审核实发 = 快照 100（与队列显示一致）
	postJSON(t, fmt.Sprintf("/admin/v1/claim-tasks/claims/%d/approve", claimID), nil, adminToken)
	if got := availableOf(t, token); got != 100 {
		t.Errorf("granted = %d, want 100 (snapshot payout)", got)
	}
}

func TestClaimReviewRequiresAdminRole(t *testing.T) {
	now := time.Now()
	taskID := stageClaimTask(t, 100, 0, now.Add(-time.Hour), now.Add(time.Hour))
	username := uniqueUsername(t)
	registerCustomer(t, username, "")
	token := loginCustomer(t, username, "secret123")
	claimID := claimTask(t, token, taskID)

	// NORMAL 管理员被拒
	normalToken := loginNormalAdmin(t)
	resp, _ := postJSON(t, fmt.Sprintf("/admin/v1/claim-tasks/claims/%d/approve", claimID), nil, normalToken)
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("normal admin approve: status = %d, want 403", resp.StatusCode)
	}
	// 客户 token 访问 admin 面被拒（鉴权层）
	resp, _ = getJSON(t, "/admin/v1/claim-tasks/reviews", token)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("customer token on admin: status = %d, want 401", resp.StatusCode)
	}
}
