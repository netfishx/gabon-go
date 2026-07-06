package app_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"
)

var adSeq serialSeq

// serialSeq 串行测试内的单调计数器（stage helper 串行调用，无并发）。
type serialSeq struct{ n int64 }

func (a *serialSeq) next() int64 { a.n++; return a.n }

// isolateServingAds 下架当前全部在投广告，返回精确恢复函数（只复活本次下架的那些，
// 不误动其他测试故意置 offline 的广告）。
func isolateServingAds(t *testing.T) func() {
	t.Helper()
	ctx := context.Background()
	rows, err := testPool.Query(ctx, `SELECT id FROM ads WHERE status = 'active' AND deleted_at IS NULL`)
	if err != nil {
		t.Fatalf("query serving ads: %v", err)
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan: %v", err)
		}
		ids = append(ids, id)
	}
	rows.Close()
	if _, err := testPool.Exec(ctx, `UPDATE ads SET status = 'offline' WHERE id = ANY($1)`, ids); err != nil {
		t.Fatalf("offline serving ads: %v", err)
	}
	return func() {
		testPool.Exec(ctx, `UPDATE ads SET status = 'active' WHERE id = ANY($1)`, ids)
	}
}

// stageAdvertiser 直插一个广告商，返回 id。
func stageAdvertiser(t *testing.T, status string) int64 {
	t.Helper()
	var id int64
	if err := testPool.QueryRow(context.Background(),
		`INSERT INTO advertisers (name, status) VALUES ($1, $2) RETURNING id`,
		fmt.Sprintf("广告商%d", adSeq.next()), status).Scan(&id); err != nil {
		t.Fatalf("stage advertiser: %v", err)
	}
	return id
}

// stageAd 直插一条广告。expiresAt 传零值表示 NULL（永不过期）。
func stageAd(t *testing.T, advertiserID int64, status string, stock int, expiresAt time.Time) int64 {
	t.Helper()
	var id int64
	var exp any
	if !expiresAt.IsZero() {
		exp = expiresAt
	}
	if err := testPool.QueryRow(context.Background(),
		`INSERT INTO ads (advertiser_id, title, media_path, stock_total, stock_remaining, status, expires_at)
		 VALUES ($1, $2, 'ads/x.mp4', $3, $3, $4, $5) RETURNING id`,
		advertiserID, fmt.Sprintf("广告%d", adSeq.next()), stock, status, exp).Scan(&id); err != nil {
		t.Fatalf("stage ad: %v", err)
	}
	return id
}

// watchAd 调看广告端点，返回响应体。
func watchAd(t *testing.T, token string) (*http.Response, map[string]any) {
	t.Helper()
	return postJSON(t, "/api/v1/ads/watch", nil, token)
}

func stockOf(t *testing.T, adID int64) int {
	t.Helper()
	var n int
	if err := testPool.QueryRow(context.Background(),
		`SELECT stock_remaining FROM ads WHERE id = $1`, adID).Scan(&n); err != nil {
		t.Fatalf("query stock: %v", err)
	}
	return n
}

func TestWatchAd(t *testing.T) {
	adv := stageAdvertiser(t, "active")
	adID := stageAd(t, adv, "active", 10, time.Time{})

	username := uniqueUsername(t)
	registerCustomer(t, username, "")
	token := loginCustomer(t, username, "secret123")
	cid := customerIDOf(t, username)

	resp, body := watchAd(t, token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("watch ad: status = %d, body = %v", resp.StatusCode, body)
	}
	ad, _ := body["ad"].(map[string]any)
	if ad == nil {
		t.Fatalf("watch ad: no ad returned, body = %v", body)
	}
	if int64(ad["id"].(float64)) != adID {
		t.Errorf("served ad id = %v, want %d", ad["id"], adID)
	}

	// 库存扣减
	if got := stockOf(t, adID); got != 9 {
		t.Errorf("stock after watch = %d, want 9", got)
	}
	// ad_watches 落明细
	var watches int
	testPool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM ad_watches WHERE customer_id = $1 AND ad_id = $2`, cid, adID).Scan(&watches)
	if watches != 1 {
		t.Errorf("ad_watches = %d, want 1", watches)
	}
}

func TestWatchAdAdvancesTask(t *testing.T) {
	// 看广告推进"看广告"周期任务进度（种子任务 target 3）
	adv := stageAdvertiser(t, "active")
	stageAd(t, adv, "active", 100, time.Time{})
	username := uniqueUsername(t)
	registerCustomer(t, username, "")
	token := loginCustomer(t, username, "secret123")

	watchAd(t, token)
	if p, _ := taskProgressOf(t, token, "watch_ad"); p != 1 {
		t.Errorf("watch_ad task progress = %d, want 1", p)
	}
}

func TestWatchAdNoServingAd(t *testing.T) {
	username := uniqueUsername(t)
	registerCustomer(t, username, "")
	token := loginCustomer(t, username, "secret123")

	// 确定性无在投：下架当前全部在投广告，测试后精确复活
	t.Cleanup(isolateServingAds(t))

	resp, body := watchAd(t, token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("watch with no ad: status = %d, body = %v", resp.StatusCode, body)
	}
	if body["ad"] != nil {
		t.Errorf("ad = %v, want null (no serving ad)", body["ad"])
	}
}

func TestWatchAdServingConditions(t *testing.T) {
	// 逐反例：下架广告 / 下架广告商 / 零库存 / 已过期 都不返回
	username := uniqueUsername(t)
	registerCustomer(t, username, "")
	token := loginCustomer(t, username, "secret123")
	// 隔离环境：下架当前全部在投广告，测试后精确复活
	t.Cleanup(isolateServingAds(t))

	activeAdv := stageAdvertiser(t, "active")
	offlineAdv := stageAdvertiser(t, "offline")

	stageAd(t, activeAdv, "offline", 10, time.Time{})                      // 广告下架
	stageAd(t, offlineAdv, "active", 10, time.Time{})                      // 广告商下架
	stageAd(t, activeAdv, "active", 0, time.Time{})                        // 零库存
	stageAd(t, activeAdv, "active", 10, time.Now().Add(-time.Hour))        // 已过期
	good := stageAd(t, activeAdv, "active", 10, time.Now().Add(time.Hour)) // 唯一在投

	// 多看几次都只可能返回 good
	for range 5 {
		resp, body := watchAd(t, token)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("watch: status = %d", resp.StatusCode)
		}
		ad, _ := body["ad"].(map[string]any)
		if ad == nil {
			t.Fatalf("no ad returned, want the only serving ad")
		}
		if int64(ad["id"].(float64)) != good {
			t.Errorf("served ad = %v, want only-serving %d", ad["id"], good)
		}
	}
}

// ---- admin 广告商/广告 CRUD ----

func TestAdvertiserAndAdCRUD(t *testing.T) {
	adminToken := loginAdmin(t)

	// 建广告商
	resp, body := postJSON(t, "/admin/v1/advertisers", map[string]any{"name": "新广告商", "contact": "c@x.test"}, adminToken)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create advertiser: status = %d, body = %v", resp.StatusCode, body)
	}
	advID := int64(body["id"].(float64))

	// 建广告
	resp, body = postJSON(t, "/admin/v1/ads", map[string]any{
		"advertiser_id": advID, "title": "新广告", "media_path": "ads/new.mp4", "stock": 50,
	}, adminToken)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create ad: status = %d, body = %v", resp.StatusCode, body)
	}
	adID := int64(body["id"].(float64))

	// 列表含新建
	resp, list := getJSON(t, "/admin/v1/ads", adminToken)
	if resp.StatusCode != http.StatusOK || !adminListContainsID(list, adID) {
		t.Errorf("ad not in admin list")
	}

	// 编辑广告标题
	resp, _ = doJSON(t, http.MethodPatch, fmt.Sprintf("/admin/v1/ads/%d", adID), adminToken, map[string]any{"title": "改名"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("update ad: status = %d", resp.StatusCode)
	}

	// 软删广告
	resp, _ = doJSON(t, http.MethodDelete, fmt.Sprintf("/admin/v1/ads/%d", adID), adminToken, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete ad: status = %d", resp.StatusCode)
	}

	// 新建默认下架（须手动上架才投放）
	_, list = getJSON(t, "/admin/v1/advertisers", adminToken)
	items, _ := list["items"].([]any)
	for _, it := range items {
		m, _ := it.(map[string]any)
		if int64(m["id"].(float64)) == advID {
			if m["status"] != "offline" {
				t.Errorf("new advertiser status = %v, want offline (manual publish gate)", m["status"])
			}
		}
	}

	// 删广告商（软删）：从列表消失
	resp, _ = doJSON(t, http.MethodDelete, fmt.Sprintf("/admin/v1/advertisers/%d", advID), adminToken, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete advertiser: status = %d", resp.StatusCode)
	}
	_, list = getJSON(t, "/admin/v1/advertisers", adminToken)
	if adminListContainsID(list, advID) {
		t.Errorf("deleted advertiser still in list")
	}
}

func TestCreateAdUnknownAdvertiser(t *testing.T) {
	adminToken := loginAdmin(t)
	resp, body := postJSON(t, "/admin/v1/ads", map[string]any{
		"advertiser_id": int64(999999999), "title": "孤儿广告", "media_path": "ads/x.mp4", "stock": 1,
	}, adminToken)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("create ad with unknown advertiser: status = %d, want 400, body = %v", resp.StatusCode, body)
	}
}

func TestAdvertiserOfflineCascadesAds(t *testing.T) {
	adminToken := loginAdmin(t)
	advID := stageAdvertiser(t, "active")
	ad1 := stageAd(t, advID, "active", 10, time.Time{})
	ad2 := stageAd(t, advID, "active", 10, time.Time{})

	// 下架广告商 → 名下广告一并下架
	resp, _ := doJSON(t, http.MethodPatch, fmt.Sprintf("/admin/v1/advertisers/%d/status", advID),
		adminToken, map[string]any{"enabled": false})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("offline advertiser: status = %d", resp.StatusCode)
	}
	for _, adID := range []int64{ad1, ad2} {
		var status string
		testPool.QueryRow(context.Background(), `SELECT status FROM ads WHERE id = $1`, adID).Scan(&status)
		if status != "offline" {
			t.Errorf("ad %d status = %s, want offline (cascade)", adID, status)
		}
	}
}

func TestAdAdminRequiresAdminRole(t *testing.T) {
	normalToken := loginNormalAdmin(t)
	advID := stageAdvertiser(t, "active")
	endpoints := []struct {
		method, path string
		body         map[string]any
	}{
		{http.MethodGet, "/admin/v1/advertisers", nil},
		{http.MethodPost, "/admin/v1/advertisers", map[string]any{"name": "x"}},
		{http.MethodGet, "/admin/v1/ads", nil},
		{http.MethodPost, "/admin/v1/ads", map[string]any{"advertiser_id": advID, "title": "x", "media_path": "y", "stock": 1}},
		{http.MethodPatch, fmt.Sprintf("/admin/v1/advertisers/%d/status", advID), map[string]any{"enabled": false}},
		{http.MethodDelete, fmt.Sprintf("/admin/v1/advertisers/%d", advID), nil},
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

func TestWatchAdRequiresAuth(t *testing.T) {
	resp, _ := watchAd(t, "")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("unauthenticated watch: status = %d, want 401", resp.StatusCode)
	}
}
