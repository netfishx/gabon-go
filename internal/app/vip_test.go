package app_test

import (
	"context"
	"net/http"
	"testing"
)

// grantDiamonds 直接给客户钱包充值（测试夹具，绕开充值链路）。
func grantDiamonds(t *testing.T, username string, amount int64) {
	t.Helper()
	cid := customerIDOf(t, username)
	if _, err := testPool.Exec(context.Background(),
		`UPDATE wallets SET available = available + $2 WHERE customer_id = $1`, cid, amount); err != nil {
		t.Fatalf("grant diamonds: %v", err)
	}
}

// vipLevelOf 读客户当前 VIP 档。
func vipLevelOf(t *testing.T, username string) int {
	t.Helper()
	var level int
	if err := testPool.QueryRow(context.Background(),
		`SELECT vip_level FROM customers WHERE username = $1`, username).Scan(&level); err != nil {
		t.Fatalf("query vip_level: %v", err)
	}
	return level
}

func TestVipPurchase(t *testing.T) {
	username := uniqueUsername(t)
	registerCustomer(t, username, "")
	token := loginCustomer(t, username, "secret123")
	grantDiamonds(t, username, 100000) // 够买银牌 99900

	// 跳级买银牌（level 2，价 99900 钻）
	resp, body := postJSON(t, "/api/v1/vip/purchase", map[string]any{"level": 2}, token)
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		t.Fatalf("purchase: status = %d, body = %v", resp.StatusCode, body)
	}
	if got := vipLevelOf(t, username); got != 2 {
		t.Fatalf("vip_level after purchase = %d, want 2", got)
	}
	if got := availableOf(t, token); got != 100 {
		t.Fatalf("available after purchase = %d, want 100 (100000-99900)", got)
	}
	// 流水类型 vip_purchase
	_, tx := getJSON(t, "/api/v1/wallet/transactions", token)
	items, _ := tx["items"].([]any)
	first, _ := items[0].(map[string]any)
	if got := first["type"]; got != "vip_purchase" {
		t.Errorf("tx type = %v, want vip_purchase", got)
	}
}

func TestVipPurchaseGuards(t *testing.T) {
	t.Run("downgrade_rejected", func(t *testing.T) {
		username := uniqueUsername(t)
		registerCustomer(t, username, "")
		token := loginCustomer(t, username, "secret123")
		grantDiamonds(t, username, 500000)
		postJSON(t, "/api/v1/vip/purchase", map[string]any{"level": 2}, token) // 到银牌
		// 买铜牌（level 1 < 2）应拒
		resp, _ := postJSON(t, "/api/v1/vip/purchase", map[string]any{"level": 1}, token)
		if resp.StatusCode != http.StatusConflict {
			t.Errorf("downgrade purchase: status = %d, want 409", resp.StatusCode)
		}
	})
	t.Run("same_level_rejected", func(t *testing.T) {
		username := uniqueUsername(t)
		registerCustomer(t, username, "")
		token := loginCustomer(t, username, "secret123")
		grantDiamonds(t, username, 500000)
		postJSON(t, "/api/v1/vip/purchase", map[string]any{"level": 1}, token)
		resp, _ := postJSON(t, "/api/v1/vip/purchase", map[string]any{"level": 1}, token)
		if resp.StatusCode != http.StatusConflict {
			t.Errorf("same-level purchase: status = %d, want 409", resp.StatusCode)
		}
	})
	t.Run("insufficient_balance", func(t *testing.T) {
		username := uniqueUsername(t)
		registerCustomer(t, username, "")
		token := loginCustomer(t, username, "secret123")
		// 余额 0，买铜牌（39900）应拒且不升级不扣
		resp, _ := postJSON(t, "/api/v1/vip/purchase", map[string]any{"level": 1}, token)
		if resp.StatusCode != http.StatusConflict {
			t.Errorf("insufficient: status = %d, want 409", resp.StatusCode)
		}
		if got := vipLevelOf(t, username); got != 0 {
			t.Errorf("vip_level after failed purchase = %d, want 0", got)
		}
		if got := availableOf(t, token); got != 0 {
			t.Errorf("available after failed purchase = %d, want 0", got)
		}
	})
}

func TestVipLevelConfigs(t *testing.T) {
	username := uniqueUsername(t)
	registerCustomer(t, username, "")
	token := loginCustomer(t, username, "secret123")

	resp, body := getJSON(t, "/api/v1/vip/levels", token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("vip levels: status = %d, body = %v", resp.StatusCode, body)
	}
	items, _ := body["items"].([]any)
	if len(items) != 4 {
		t.Fatalf("vip levels = %d, want 4", len(items))
	}
	// 金卡（level 3）价 299900、发布上限 100
	gold, _ := items[3].(map[string]any)
	if got, _ := gold["price"].(float64); int64(got) != 299900 {
		t.Errorf("gold price = %v, want 299900", gold["price"])
	}
	if got, _ := gold["upload_video_limit"].(float64); int(got) != 100 {
		t.Errorf("gold upload_video_limit = %v, want 100", gold["upload_video_limit"])
	}
}

func TestVipUploadLimit(t *testing.T) {
	// 普通档发布上限 6：传满 6 条后第 7 条 confirm 被拒；删一条后可再传
	username := uniqueUsername(t)
	registerCustomer(t, username, "")
	token := loginCustomer(t, username, "secret123")
	cid := customerIDOf(t, username)

	// 直接把 video_count 顶到 6（模拟已发布 6 条）
	if _, err := testPool.Exec(context.Background(),
		`UPDATE customers SET video_count = 6 WHERE id = $1`, cid); err != nil {
		t.Fatalf("set video_count: %v", err)
	}

	// 第 7 条：上传 confirm 被拒
	path := createUpload(t, token, []byte("v7"))
	resp, body := postJSON(t, "/api/v1/videos", map[string]any{
		"storage_path": path, "title": "第七条",
	}, token)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("7th upload at limit 6: status = %d, want 409, body = %v", resp.StatusCode, body)
	}
	if got := body["code"]; got != "VIDEO_UPLOAD_LIMIT_REACHED" {
		t.Errorf("code = %v, want VIDEO_UPLOAD_LIMIT_REACHED", got)
	}

	// 降到 5 条后可再传
	if _, err := testPool.Exec(context.Background(),
		`UPDATE customers SET video_count = 5 WHERE id = $1`, cid); err != nil {
		t.Fatalf("lower video_count: %v", err)
	}
	path2 := createUpload(t, token, []byte("v6b"))
	resp, body = postJSON(t, "/api/v1/videos", map[string]any{
		"storage_path": path2, "title": "补一条",
	}, token)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("upload after freeing slot: status = %d, body = %v", resp.StatusCode, body)
	}
}

func TestVipUploadLimitRaisedAfterUpgrade(t *testing.T) {
	// 升级到银牌（上限 50）后，video_count=6 不再受普通档 6 限制
	username := uniqueUsername(t)
	registerCustomer(t, username, "")
	token := loginCustomer(t, username, "secret123")
	cid := customerIDOf(t, username)
	grantDiamonds(t, username, 100000)
	postJSON(t, "/api/v1/vip/purchase", map[string]any{"level": 2}, token)

	if _, err := testPool.Exec(context.Background(),
		`UPDATE customers SET video_count = 6 WHERE id = $1`, cid); err != nil {
		t.Fatalf("set video_count: %v", err)
	}
	path := createUpload(t, token, []byte("silver7"))
	resp, body := postJSON(t, "/api/v1/videos", map[string]any{
		"storage_path": path, "title": "银牌第七条",
	}, token)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("upload as silver at count 6: status = %d, body = %v", resp.StatusCode, body)
	}
}

func TestVipPurchaseRequiresAuth(t *testing.T) {
	resp, _ := postJSON(t, "/api/v1/vip/purchase", map[string]any{"level": 1}, "")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("unauthenticated purchase: status = %d, want 401", resp.StatusCode)
	}
}
