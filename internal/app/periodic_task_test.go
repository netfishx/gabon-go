package app_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"
)

// playAndValid 两段式播放上报：开播拿事件 id → 上报有效播放。
func playAndValid(t *testing.T, token, publicID string) {
	t.Helper()
	resp, body := postJSON(t, "/api/v1/videos/"+publicID+"/plays", nil, token)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("play: status = %d, body = %v", resp.StatusCode, body)
	}
	playID, _ := body["play_id"].(float64)
	if playID == 0 {
		t.Fatalf("play: missing play_id, body = %v", body)
	}
	resp, body = postJSON(t, fmt.Sprintf("/api/v1/plays/%d/valid", int64(playID)), nil, token)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("valid: status = %d, body = %v", resp.StatusCode, body)
	}
}

// taskProgressOf 从周期任务列表端点取指定类别的当期进度。
func taskProgressOf(t *testing.T, token, category string) (progress, target int) {
	t.Helper()
	resp, body := getJSON(t, "/api/v1/tasks", token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("tasks: status = %d, body = %v", resp.StatusCode, body)
	}
	items, _ := body["items"].([]any)
	for _, it := range items {
		m, _ := it.(map[string]any)
		if m["category"] == category {
			p, _ := m["progress"].(float64)
			tg, _ := m["target"].(float64)
			return int(p), int(tg)
		}
	}
	t.Fatalf("tasks: category %s not found in %v", category, body)
	return 0, 0
}

// stageOthersVideos 用独立作者直插 n 条已发布视频。
func stageOthersVideos(t *testing.T, n int) []string {
	t.Helper()
	author := uniqueUsername(t)
	registerCustomer(t, author, "")
	aid := customerIDOf(t, author)
	out := make([]string, 0, n)
	for range n {
		out = append(out, stagePublishedVideo(t, aid, 0))
	}
	return out
}

func TestWatchTaskEndToEnd(t *testing.T) {
	username := uniqueUsername(t)
	registerCustomer(t, username, "")
	token := loginCustomer(t, username, "secret123")
	videos := stageOthersVideos(t, 5)

	// 前 4 次有效播放：进度推进但未达标，无入账
	for i := range 4 {
		playAndValid(t, token, videos[i])
	}
	if p, tg := taskProgressOf(t, token, "watch_video"); p != 4 || tg != 5 {
		t.Fatalf("progress = %d/%d, want 4/5", p, tg)
	}
	if got := availableOf(t, token); got != 0 {
		t.Fatalf("available before target = %d, want 0", got)
	}

	// 第 5 次达标：自动入账 58（普通档倍率 10000bp）
	playAndValid(t, token, videos[4])
	if p, _ := taskProgressOf(t, token, "watch_video"); p != 5 {
		t.Fatalf("progress = %d, want 5", p)
	}
	if got := availableOf(t, token); got != 58 {
		t.Fatalf("available after target = %d, want 58", got)
	}
	resp, body := getJSON(t, "/api/v1/wallet/transactions", token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("transactions: status = %d", resp.StatusCode)
	}
	items, _ := body["items"].([]any)
	first, _ := items[0].(map[string]any)
	if got := first["type"]; got != "periodic_task_reward" {
		t.Errorf("tx type = %v, want periodic_task_reward", got)
	}
	// 流水 ref = 进度行 id（ref 无对应 API，走 DB 观察）
	var refMatches int
	if err := testPool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM transactions t
		 JOIN periodic_task_progress p ON p.id = t.ref_id AND p.customer_id = t.customer_id
		 WHERE t.type = 'periodic_task_reward'
		   AND t.customer_id = (SELECT id FROM customers WHERE username = $1)`,
		username).Scan(&refMatches); err != nil {
		t.Fatalf("query reward ref: %v", err)
	}
	if refMatches != 1 {
		t.Errorf("reward tx with ref=progress row = %d, want 1", refMatches)
	}

	// 超出目标继续播放：进度封顶、不再发奖
	extra := stageOthersVideos(t, 1)
	playAndValid(t, token, extra[0])
	if got := availableOf(t, token); got != 58 {
		t.Errorf("available after extra play = %d, want 58 (no double grant)", got)
	}
}

func TestWatchTaskAntiFarming(t *testing.T) {
	// 同客户同视频同周期：仅首次有效播放推进度
	username := uniqueUsername(t)
	registerCustomer(t, username, "")
	token := loginCustomer(t, username, "secret123")
	videos := stageOthersVideos(t, 1)

	playAndValid(t, token, videos[0])
	playAndValid(t, token, videos[0]) // 重看同一条
	if p, _ := taskProgressOf(t, token, "watch_video"); p != 1 {
		t.Fatalf("progress after re-watch = %d, want 1", p)
	}
}

func TestLikeTaskAntiFarmingAndGrant(t *testing.T) {
	username := uniqueUsername(t)
	registerCustomer(t, username, "")
	token := loginCustomer(t, username, "secret123")
	videos := stageOthersVideos(t, 20)

	// 首赞推进；取消再赞不再推进（终身一行口径）
	doJSON(t, http.MethodPost, "/api/v1/videos/"+videos[0]+"/like", token, nil)
	if p, _ := taskProgressOf(t, token, "like"); p != 1 {
		t.Fatalf("progress after first like = %d, want 1", p)
	}
	doJSON(t, http.MethodDelete, "/api/v1/videos/"+videos[0]+"/like", token, nil)
	doJSON(t, http.MethodPost, "/api/v1/videos/"+videos[0]+"/like", token, nil)
	if p, _ := taskProgressOf(t, token, "like"); p != 1 {
		t.Fatalf("progress after re-like = %d, want 1 (no re-count)", p)
	}

	// 补满 20 条：达标入账 58
	for i := 1; i < 20; i++ {
		doJSON(t, http.MethodPost, "/api/v1/videos/"+videos[i]+"/like", token, nil)
	}
	if p, tg := taskProgressOf(t, token, "like"); p != 20 || tg != 20 {
		t.Fatalf("progress = %d/%d, want 20/20", p, tg)
	}
	if got := availableOf(t, token); got != 58 {
		t.Fatalf("available = %d, want 58", got)
	}
}

func TestCommentTaskGrant(t *testing.T) {
	username := uniqueUsername(t)
	registerCustomer(t, username, "")
	token := loginCustomer(t, username, "secret123")
	videos := stageOthersVideos(t, 5)

	for i, pid := range videos {
		resp, body := postJSON(t, "/api/v1/videos/"+pid+"/comments", map[string]any{
			"content": fmt.Sprintf("不错 %d", i),
		}, token)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("comment %d: status = %d, body = %v", i, resp.StatusCode, body)
		}
	}
	if p, _ := taskProgressOf(t, token, "comment"); p != 5 {
		t.Fatalf("progress = %d, want 5", p)
	}
	if got := availableOf(t, token); got != 58 {
		t.Fatalf("available = %d, want 58", got)
	}
}

func TestRewardMultipliedByVipLevel(t *testing.T) {
	// 铜牌档 12000bp：58 × 1.2 = 69.6 → floor 69
	username := uniqueUsername(t)
	registerCustomer(t, username, "")
	token := loginCustomer(t, username, "secret123")
	cid := customerIDOf(t, username)
	if _, err := testPool.Exec(context.Background(),
		`UPDATE customers SET vip_level = 1 WHERE id = $1`, cid); err != nil {
		t.Fatalf("stage vip level: %v", err)
	}

	videos := stageOthersVideos(t, 5)
	for i, pid := range videos {
		resp, body := postJSON(t, "/api/v1/videos/"+pid+"/comments", map[string]any{
			"content": fmt.Sprintf("好看 %d", i),
		}, token)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("comment %d: status = %d, body = %v", i, resp.StatusCode, body)
		}
	}
	if got := availableOf(t, token); got != 69 {
		t.Fatalf("available = %d, want 69 (floor(58×1.2))", got)
	}
}

func TestNewPeriodStartsFresh(t *testing.T) {
	username := uniqueUsername(t)
	registerCustomer(t, username, "")
	token := loginCustomer(t, username, "secret123")
	cid := customerIDOf(t, username)
	videos := stageOthersVideos(t, 2)

	playAndValid(t, token, videos[0])
	// 把当期进度行挪到过去的周期（DB 操纵点：模拟跨周期）
	if _, err := testPool.Exec(context.Background(),
		`UPDATE periodic_task_progress SET period_key = '2020-01-01' WHERE customer_id = $1`, cid); err != nil {
		t.Fatalf("backdate period: %v", err)
	}
	// 同时把旧播放记录也挪出当期，避免防刷把新事件判为重看
	if _, err := testPool.Exec(context.Background(),
		`UPDATE plays SET valid_at = '2020-01-01T00:00:00Z' WHERE customer_id = $1`, cid); err != nil {
		t.Fatalf("backdate plays: %v", err)
	}

	playAndValid(t, token, videos[1])
	if p, _ := taskProgressOf(t, token, "watch_video"); p != 1 {
		t.Fatalf("progress in new period = %d, want 1 (fresh row)", p)
	}
}

func TestTaskListShapeAndOrder(t *testing.T) {
	username := uniqueUsername(t)
	registerCustomer(t, username, "")
	token := loginCustomer(t, username, "secret123")

	resp, body := getJSON(t, "/api/v1/tasks", token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("tasks: status = %d, body = %v", resp.StatusCode, body)
	}
	items, _ := body["items"].([]any)
	if len(items) < 4 {
		t.Fatalf("items = %d, want >= 4 (seeds)", len(items))
	}
	// 种子顺序 = display_order：看视频、看广告、点赞、评论
	wantOrder := []string{"watch_video", "watch_ad", "like", "comment"}
	for i, want := range wantOrder {
		m, _ := items[i].(map[string]any)
		if m["category"] != want {
			t.Errorf("items[%d].category = %v, want %s", i, m["category"], want)
		}
		if r, _ := m["reward"].(float64); r != 58 {
			t.Errorf("items[%d].reward = %v, want 58", i, m["reward"])
		}
	}

	// 未登录 401
	resp, _ = getJSON(t, "/api/v1/tasks", "")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("unauthenticated: status = %d, want 401", resp.StatusCode)
	}
}

func TestTaskFailureDoesNotBlockMainEvent(t *testing.T) {
	// 主链路隔离：破坏钱包行使发奖必败，点赞主事件仍成功
	username := uniqueUsername(t)
	registerCustomer(t, username, "")
	token := loginCustomer(t, username, "secret123")
	cid := customerIDOf(t, username)
	videos := stageOthersVideos(t, 20)
	ctx := context.Background()

	// 先推到 19（差 1 达标），再删钱包行
	for i := range 19 {
		doJSON(t, http.MethodPost, "/api/v1/videos/"+videos[i]+"/like", token, nil)
	}
	if _, err := testPool.Exec(ctx, `DELETE FROM wallets WHERE customer_id = $1`, cid); err != nil {
		t.Fatalf("break wallet: %v", err)
	}
	t.Cleanup(func() {
		if _, err := testPool.Exec(ctx,
			`INSERT INTO wallets (customer_id) VALUES ($1) ON CONFLICT DO NOTHING`, cid); err != nil {
			t.Fatalf("restore wallet: %v", err)
		}
	})

	// 第 20 赞：达标发奖必失败（无钱包行），但点赞本身成功
	resp, body := doJSON(t, http.MethodPost, "/api/v1/videos/"+videos[19]+"/like", token, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("like with broken wallet: status = %d, body = %v (main event must succeed)", resp.StatusCode, body)
	}
	_, _, like, _, _ := videoCounters(t, videos[19])
	if like != 1 {
		t.Errorf("like_count = %d, want 1 (main event committed)", like)
	}
	// 任务推进整体回滚：进度停在 19
	if p, _ := taskProgressOf(t, token, "like"); p != 19 {
		t.Errorf("progress = %d, want 19 (advance rolled back atomically)", p)
	}
}
