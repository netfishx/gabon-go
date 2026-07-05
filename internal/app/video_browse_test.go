package app_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

var browseSeq atomic.Int64

// stagePublishedVideo 直插已发布视频（含媒体字段与热度），返回 public_id。
func stagePublishedVideo(t *testing.T, customerID int64, hotScore int64) string {
	t.Helper()
	n := browseSeq.Add(1)
	publicID := fmt.Sprintf("brw%09d", n)
	if _, err := testPool.Exec(
		context.Background(),
		`INSERT INTO videos (public_id, customer_id, title, storage_path, status,
		                     hls_path, thumbnail_path, duration, width, height, hot_score)
		 VALUES ($1, $2, $3, $4, 'published', $5, $6, 1, 320, 240, $7)`,
		publicID, customerID, fmt.Sprintf("浏览样本%d", n),
		fmt.Sprintf("videos/%d/b%d.mp4", customerID, n),
		fmt.Sprintf("hls/b%d/index.m3u8", n), fmt.Sprintf("thumbs/b%d.jpg", n), hotScore,
	); err != nil {
		t.Fatalf("stage published video: %v", err)
	}
	return publicID
}

func customerIDOf(t *testing.T, username string) int64 {
	t.Helper()
	var id int64
	if err := testPool.QueryRow(context.Background(),
		`SELECT id FROM customers WHERE username = $1`, username).Scan(&id); err != nil {
		t.Fatalf("query customer id: %v", err)
	}
	return id
}

func TestFeedSeedPagination(t *testing.T) {
	username := uniqueUsername(t)
	registerCustomer(t, username, "")
	cid := customerIDOf(t, username)

	mine := map[string]bool{}
	for range 8 {
		mine[stagePublishedVideo(t, cid, 0)] = true
	}

	// 第一页拿 seed，逐页收集全部（feed 是全局的，容忍其他测试的视频，断言不重不漏）
	resp, body := getJSON(t, "/api/v1/feed?limit=3", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("feed: status = %d, body = %v", resp.StatusCode, body)
	}
	seed, _ := body["seed"].(string)
	if seed == "" {
		t.Fatalf("feed missing seed, body keys = %v", body)
	}

	seen := map[string]bool{}
	var firstOrder []string
	page := 0
	for {
		page++
		if page > 100 {
			t.Fatalf("pagination did not terminate")
		}
		items, _ := body["items"].([]any)
		for _, it := range items {
			pid := it.(map[string]any)["public_id"].(string)
			if seen[pid] {
				t.Fatalf("duplicate %s across pages (seed stable paging broken)", pid)
			}
			seen[pid] = true
			firstOrder = append(firstOrder, pid)
		}
		cursor, has := body["next_cursor"].(string)
		if !has || cursor == "" {
			break
		}
		resp, body = getJSON(t, fmt.Sprintf("/api/v1/feed?limit=3&seed=%s&cursor=%s", seed, cursor), "")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("feed page %d: status = %d", page, resp.StatusCode)
		}
	}
	for pid := range mine {
		if !seen[pid] {
			t.Errorf("video %s missing from full feed walk", pid)
		}
	}

	// 换 seed 顺序应变化（8+ 视频下碰撞概率可忽略）
	resp, body = getJSON(t, "/api/v1/feed?limit=100&seed=another-seed-value", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("feed reseed: status = %d", resp.StatusCode)
	}
	var secondOrder []string
	for _, it := range body["items"].([]any) {
		secondOrder = append(secondOrder, it.(map[string]any)["public_id"].(string))
	}
	if len(firstOrder) >= 8 && strings.Join(firstOrder, ",") == strings.Join(secondOrder[:len(firstOrder)], ",") {
		t.Errorf("order identical across different seeds")
	}
}

func TestFeaturedOrdersByHotScore(t *testing.T) {
	username := uniqueUsername(t)
	registerCustomer(t, username, "")
	cid := customerIDOf(t, username)

	low := stagePublishedVideo(t, cid, 10)
	mid := stagePublishedVideo(t, cid, 1000)
	high := stagePublishedVideo(t, cid, 100000)

	resp, body := getJSON(t, "/api/v1/featured?limit=100", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("featured: status = %d, body = %v", resp.StatusCode, body)
	}
	pos := map[string]int{}
	for i, it := range body["items"].([]any) {
		pos[it.(map[string]any)["public_id"].(string)] = i
	}
	ph, okH := pos[high]
	pm, okM := pos[mid]
	pl, okL := pos[low]
	if !okH || !okM || !okL {
		t.Fatalf("staged videos missing from featured: %v", pos)
	}
	if ph >= pm || pm >= pl {
		t.Errorf("featured order = high:%d mid:%d low:%d, want descending hot score", ph, pm, pl)
	}
}

func TestVideoDetailAndVisibility(t *testing.T) {
	username := uniqueUsername(t)
	registerCustomer(t, username, "")
	cid := customerIDOf(t, username)
	publicID := stagePublishedVideo(t, cid, 0)

	t.Run("detail_public_with_cdn_urls", func(t *testing.T) {
		resp, body := getJSON(t, "/api/v1/videos/"+publicID, "")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("detail: status = %d, body = %v", resp.StatusCode, body)
		}
		hlsURL, _ := body["hls_url"].(string)
		thumbURL, _ := body["thumbnail_url"].(string)
		if !strings.HasPrefix(hlsURL, "http") || !strings.Contains(hlsURL, "index.m3u8") {
			t.Errorf("hls_url = %q, want CDN-based full url", hlsURL)
		}
		if !strings.HasPrefix(thumbURL, "http") {
			t.Errorf("thumbnail_url = %q, want CDN-based full url", thumbURL)
		}
		author, _ := body["author"].(map[string]any)
		if author["username"] != username {
			t.Errorf("author = %v, want %s", author, username)
		}
	})

	t.Run("unpublished_detail_404", func(t *testing.T) {
		pending, _ := stagePendingVideo(t, username)
		resp, _ := getJSON(t, "/api/v1/videos/"+pending, "")
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("pending detail: status = %d, want 404", resp.StatusCode)
		}
	})

	t.Run("profile_only_published_my_list_all", func(t *testing.T) {
		var customerPublicID string
		if err := testPool.QueryRow(context.Background(),
			`SELECT public_id FROM customers WHERE id = $1`, cid).Scan(&customerPublicID); err != nil {
			t.Fatalf("query customer public id: %v", err)
		}
		resp, body := getJSON(t, "/api/v1/customers/"+customerPublicID+"/videos", "")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("profile videos: status = %d", resp.StatusCode)
		}
		for _, it := range body["items"].([]any) {
			if s, has := it.(map[string]any)["status"]; has && s != "published" {
				t.Errorf("profile list leaked status %v", s)
			}
		}

		token := loginCustomer(t, username, "secret123")
		resp, body = getJSON(t, "/api/v1/me/videos", token)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("my videos: status = %d", resp.StatusCode)
		}
		statuses := map[string]bool{}
		for _, it := range body["items"].([]any) {
			statuses[it.(map[string]any)["status"].(string)] = true
		}
		if !statuses["pending_review"] || !statuses["published"] {
			t.Errorf("my list statuses = %v, want both pending_review and published", statuses)
		}
	})

	t.Run("delete_own_then_gone", func(t *testing.T) {
		token := loginCustomer(t, username, "secret123")
		victim := stagePublishedVideo(t, cid, 0)

		req, _ := http.NewRequest(http.MethodDelete, testServer.URL+"/api/v1/videos/"+victim, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("delete: %v", err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNoContent {
			t.Fatalf("delete: status = %d, want 204", resp.StatusCode)
		}
		if resp, _ := getJSON(t, "/api/v1/videos/"+victim, ""); resp.StatusCode != http.StatusNotFound {
			t.Errorf("deleted detail: status = %d, want 404", resp.StatusCode)
		}
	})

	t.Run("delete_others_forbidden", func(t *testing.T) {
		intruder := uniqueUsername(t)
		registerCustomer(t, intruder, "")
		intruderToken := loginCustomer(t, intruder, "secret123")
		victim := stagePublishedVideo(t, cid, 0)

		req, _ := http.NewRequest(http.MethodDelete, testServer.URL+"/api/v1/videos/"+victim, nil)
		req.Header.Set("Authorization", "Bearer "+intruderToken)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("delete: %v", err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("delete others: status = %d, want 404", resp.StatusCode)
		}
	})
}
