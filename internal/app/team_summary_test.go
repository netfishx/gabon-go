package app_test

import (
	"net/http"
	"testing"
)

// layerOf 从汇总响应取指定深度的层。
func layerOf(t *testing.T, body map[string]any, depth int) map[string]any {
	t.Helper()
	layers, _ := body["layers"].([]any)
	if len(layers) != 3 {
		t.Fatalf("layers = %d 层, want 3（深度 1–3 恒定返回）: %v", len(layers), body)
	}
	l, _ := layers[depth-1].(map[string]any)
	if got, _ := l["depth"].(float64); int(got) != depth {
		t.Fatalf("layers[%d].depth = %v, want %d", depth-1, l["depth"], depth)
	}
	return l
}

func assertLayer(t *testing.T, body map[string]any, depth, wantCount, wantValid int) {
	t.Helper()
	l := layerOf(t, body, depth)
	if got, _ := l["count"].(float64); int(got) != wantCount {
		t.Errorf("depth %d count = %v, want %d", depth, l["count"], wantCount)
	}
	if got, _ := l["valid_count"].(float64); int(got) != wantValid {
		t.Errorf("depth %d valid_count = %v, want %d", depth, l["valid_count"], wantValid)
	}
}

func TestTeamSummary(t *testing.T) {
	// 结构：A ← {B1, B2}；B1 ← C；C ← D；D ← E（E 深度 4，不计入 A 的团队）
	nameA := uniqueUsername(t)
	bodyA := registerCustomer(t, nameA, "")
	codeA := inviteCodeOf(t, bodyA)
	tokenA := loginCustomer(t, nameA, "secret123")

	nameB1, nameB2 := uniqueUsername(t), uniqueUsername(t)
	bodyB1 := registerCustomer(t, nameB1, codeA)
	registerCustomer(t, nameB2, codeA)

	nameC := uniqueUsername(t)
	bodyC := registerCustomer(t, nameC, inviteCodeOf(t, bodyB1))
	nameD := uniqueUsername(t)
	bodyD := registerCustomer(t, nameD, inviteCodeOf(t, bodyC))
	registerCustomer(t, uniqueUsername(t), inviteCodeOf(t, bodyD)) // E：深度 4

	// B1 凑齐三条件翻转（邀请 C ✓ + 审核作品 + 绑手机）→ A 入账一笔邀请奖励
	tokenB1 := loginCustomer(t, nameB1, "secret123")
	approveVideoOf(t, nameB1)
	bindPhone(t, tokenB1)
	assertValid(t, tokenB1, true)

	resp, body := getJSON(t, "/api/v1/team/summary", tokenA)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("summary: status = %d, body = %v", resp.StatusCode, body)
	}
	if got, _ := body["total"].(float64); int(got) != 4 {
		t.Errorf("total = %v, want 4（B1,B2,C,D；E 深度 4 不计入）", body["total"])
	}
	if got, _ := body["total_valid"].(float64); int(got) != 1 {
		t.Errorf("total_valid = %v, want 1", body["total_valid"])
	}
	assertLayer(t, body, 1, 2, 1)
	assertLayer(t, body, 2, 1, 0)
	assertLayer(t, body, 3, 1, 0)

	// 累计邀请奖励 = 全部邀请有效奖励流水之和（B1 翻转产生的一笔）
	if got, _ := body["invite_reward_total"].(float64); int64(got) != inviteRewardSeed {
		t.Errorf("invite_reward_total = %v, want %d", body["invite_reward_total"], inviteRewardSeed)
	}
}

func TestTeamSummaryEmpty(t *testing.T) {
	lone := uniqueUsername(t)
	registerCustomer(t, lone, "")
	token := loginCustomer(t, lone, "secret123")

	resp, body := getJSON(t, "/api/v1/team/summary", token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("summary: status = %d, body = %v", resp.StatusCode, body)
	}
	if got, _ := body["total"].(float64); got != 0 {
		t.Errorf("total = %v, want 0", body["total"])
	}
	if got, _ := body["invite_reward_total"].(float64); got != 0 {
		t.Errorf("invite_reward_total = %v, want 0", body["invite_reward_total"])
	}
	for depth := 1; depth <= 3; depth++ {
		assertLayer(t, body, depth, 0, 0)
	}
}

func TestTeamSummaryRequiresAuth(t *testing.T) {
	resp, _ := getJSON(t, "/api/v1/team/summary", "")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
}
