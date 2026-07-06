package app_test

import (
	"context"
	"net/http"
	"testing"
)

// signInStatus 查本月签到状态。
func signInStatus(t *testing.T, token string) map[string]any {
	t.Helper()
	resp, body := getJSON(t, "/api/v1/sign-in/status", token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("sign-in status: status = %d, body = %v", resp.StatusCode, body)
	}
	return body
}

// setMilestoneConfig 直插一个里程碑档位配置。
func setMilestoneConfig(t *testing.T, days int, reward int64) {
	t.Helper()
	if _, err := testPool.Exec(context.Background(),
		`INSERT INTO activity_reward_configs (kind, threshold, reward, enabled)
		 VALUES ('milestone', $1, $2, true)
		 ON CONFLICT (kind, threshold) DO UPDATE SET reward = $2, enabled = true`,
		days, reward); err != nil {
		t.Fatalf("set milestone config: %v", err)
	}
}

func TestSignInDailyReward(t *testing.T) {
	username := uniqueUsername(t)
	registerCustomer(t, username, "")
	token := loginCustomer(t, username, "secret123")

	// 首签：种子日签配置 1 钻，普通档倍率 → 到账 1
	resp, body := postJSON(t, "/api/v1/sign-in", nil, token)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("sign-in: status = %d, body = %v", resp.StatusCode, body)
	}
	if got := availableOf(t, token); got != 1 {
		t.Fatalf("available after sign-in = %d, want 1", got)
	}
	resp, tx := getJSON(t, "/api/v1/wallet/transactions", token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("transactions: status = %d", resp.StatusCode)
	}
	items, _ := tx["items"].([]any)
	first, _ := items[0].(map[string]any)
	if got := first["type"]; got != "sign_in_reward" {
		t.Errorf("tx type = %v, want sign_in_reward", got)
	}

	// 同日重签 409
	resp, _ = postJSON(t, "/api/v1/sign-in", nil, token)
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("re-sign same day: status = %d, want 409", resp.StatusCode)
	}
}

func TestSignInStatus(t *testing.T) {
	username := uniqueUsername(t)
	registerCustomer(t, username, "")
	token := loginCustomer(t, username, "secret123")

	// 未签：today_signed false，signed_days 0
	st := signInStatus(t, token)
	if got, _ := st["today_signed"].(bool); got {
		t.Errorf("today_signed before sign = %v, want false", got)
	}
	if got, _ := st["signed_days"].(float64); got != 0 {
		t.Errorf("signed_days before = %v, want 0", got)
	}

	postJSON(t, "/api/v1/sign-in", nil, token)
	st = signInStatus(t, token)
	if got, _ := st["today_signed"].(bool); !got {
		t.Errorf("today_signed after sign = %v, want true", got)
	}
	if got, _ := st["signed_days"].(float64); got != 1 {
		t.Errorf("signed_days after = %v, want 1", got)
	}
}

func TestSignInStatusNextMilestone(t *testing.T) {
	// 配置 7 天里程碑：首签后状态应显示下一里程碑 = 7
	setMilestoneConfig(t, 7, 50)
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM activity_reward_configs WHERE kind = 'milestone' AND threshold = 7`)
	})
	username := uniqueUsername(t)
	registerCustomer(t, username, "")
	token := loginCustomer(t, username, "secret123")

	postJSON(t, "/api/v1/sign-in", nil, token)
	st := signInStatus(t, token)
	if got, _ := st["next_milestone"].(float64); int(got) != 7 {
		t.Errorf("next_milestone = %v, want 7", st["next_milestone"])
	}
}

func TestSignInRewardFloorWithVip(t *testing.T) {
	// 日签 base=3、铜牌 12000bp → floor(3×1.2)=floor(3.6)=3（验证各自 floor 截断，非恒等）
	ctx := context.Background()
	if _, err := testPool.Exec(ctx,
		`UPDATE activity_reward_configs SET reward = 3 WHERE kind = 'daily' AND threshold = 0`); err != nil {
		t.Fatalf("bump daily reward: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(ctx, `UPDATE activity_reward_configs SET reward = 1 WHERE kind = 'daily' AND threshold = 0`)
	})
	username := uniqueUsername(t)
	registerCustomer(t, username, "")
	token := loginCustomer(t, username, "secret123")
	cid := customerIDOf(t, username)
	if _, err := testPool.Exec(ctx, `UPDATE customers SET vip_level = 1 WHERE id = $1`, cid); err != nil {
		t.Fatalf("set vip: %v", err)
	}

	postJSON(t, "/api/v1/sign-in", nil, token)
	if got := availableOf(t, token); got != 3 {
		t.Errorf("available = %d, want 3 (floor(3×1.2))", got)
	}
}

func TestSignInMilestoneReward(t *testing.T) {
	// 里程碑档位 = 1 天（首签即命中），额外发 50 钻；日签 1 → 共 51
	setMilestoneConfig(t, 1, 50)
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM activity_reward_configs WHERE kind = 'milestone' AND threshold = 1`)
	})

	username := uniqueUsername(t)
	registerCustomer(t, username, "")
	token := loginCustomer(t, username, "secret123")

	postJSON(t, "/api/v1/sign-in", nil, token)
	if got := availableOf(t, token); got != 51 {
		t.Fatalf("available after milestone sign-in = %d, want 51 (1 daily + 50 milestone)", got)
	}
	// 两类流水各一条
	_, tx := getJSON(t, "/api/v1/wallet/transactions", token)
	items, _ := tx["items"].([]any)
	types := map[string]int{}
	for _, it := range items {
		m, _ := it.(map[string]any)
		types[m["type"].(string)]++
	}
	if types["sign_in_reward"] != 1 || types["milestone_reward"] != 1 {
		t.Errorf("tx types = %v, want one sign_in_reward and one milestone_reward", types)
	}
}

func TestSignInMilestoneNotHit(t *testing.T) {
	// 里程碑档位 = 7 天，首签不命中：只发日签
	setMilestoneConfig(t, 7, 50)
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM activity_reward_configs WHERE kind = 'milestone' AND threshold = 7`)
	})
	username := uniqueUsername(t)
	registerCustomer(t, username, "")
	token := loginCustomer(t, username, "secret123")

	postJSON(t, "/api/v1/sign-in", nil, token)
	if got := availableOf(t, token); got != 1 {
		t.Errorf("available = %d, want 1 (milestone not hit at day 1)", got)
	}
}

func TestSignInNewMonthResets(t *testing.T) {
	username := uniqueUsername(t)
	registerCustomer(t, username, "")
	token := loginCustomer(t, username, "secret123")
	cid := customerIDOf(t, username)

	postJSON(t, "/api/v1/sign-in", nil, token)
	// 把签到记录挪到上个月
	if _, err := testPool.Exec(context.Background(),
		`UPDATE sign_ins SET sign_date = sign_date - interval '40 days' WHERE customer_id = $1`, cid); err != nil {
		t.Fatalf("backdate sign-in: %v", err)
	}
	// 本月状态：signed_days 归零
	st := signInStatus(t, token)
	if got, _ := st["signed_days"].(float64); got != 0 {
		t.Errorf("signed_days new month = %v, want 0", got)
	}
	// 今日可再签（新月首签）
	resp, _ := postJSON(t, "/api/v1/sign-in", nil, token)
	if resp.StatusCode != http.StatusCreated {
		t.Errorf("sign-in in new month: status = %d, want 201", resp.StatusCode)
	}
}

func TestSignInDailyConfigDisabled(t *testing.T) {
	ctx := context.Background()
	if _, err := testPool.Exec(ctx,
		`UPDATE activity_reward_configs SET enabled = false WHERE kind = 'daily' AND threshold = 0`); err != nil {
		t.Fatalf("disable daily config: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(ctx, `UPDATE activity_reward_configs SET enabled = true WHERE kind = 'daily' AND threshold = 0`)
	})
	username := uniqueUsername(t)
	registerCustomer(t, username, "")
	token := loginCustomer(t, username, "secret123")

	// 签到成功但不发日签奖励
	resp, _ := postJSON(t, "/api/v1/sign-in", nil, token)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("sign-in with disabled config: status = %d, want 201", resp.StatusCode)
	}
	if got := availableOf(t, token); got != 0 {
		t.Errorf("available with disabled daily config = %d, want 0", got)
	}
}

func TestSignInRequiresAuth(t *testing.T) {
	resp, _ := postJSON(t, "/api/v1/sign-in", nil, "")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("unauthenticated sign-in: status = %d, want 401", resp.StatusCode)
	}
}
