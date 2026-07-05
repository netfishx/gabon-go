package app_test

import (
	"context"
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"
)

var reviewSeq atomic.Int64

// stagePendingVideo 直插一条待审核视频（DB 操纵点：绕开转码链路，且不建 job 避免 worker 竞争）。
func stagePendingVideo(t *testing.T, username string) (publicID string, customerID int64) {
	t.Helper()
	ctx := context.Background()
	if err := testPool.QueryRow(ctx,
		`SELECT id FROM customers WHERE username = $1`, username).Scan(&customerID); err != nil {
		t.Fatalf("query customer: %v", err)
	}
	n := reviewSeq.Add(1)
	publicID = fmt.Sprintf("rvw%09d", n)
	if _, err := testPool.Exec(ctx,
		`INSERT INTO videos (public_id, customer_id, title, storage_path, status, hls_path, thumbnail_path, duration)
		 VALUES ($1, $2, '审核样本', $3, 'pending_review', 'hls/x/index.m3u8', 'thumbs/x.jpg', 1)`,
		publicID, customerID, fmt.Sprintf("videos/%d/rv%d.mp4", customerID, n)); err != nil {
		t.Fatalf("stage video: %v", err)
	}
	return publicID, customerID
}

func TestAdminVideoReviewFlow(t *testing.T) {
	username := uniqueUsername(t)
	registerCustomer(t, username, "")
	adminToken := loginAdmin(t)

	publicID, customerID := stagePendingVideo(t, username)

	t.Run("pending_list_contains_video", func(t *testing.T) {
		resp, body := getJSON(t, "/admin/v1/videos/pending", adminToken)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, body = %v", resp.StatusCode, body)
		}
		items, _ := body["items"].([]any)
		found := false
		for _, it := range items {
			if it.(map[string]any)["public_id"] == publicID {
				found = true
			}
		}
		if !found {
			t.Errorf("staged video %s not in pending list", publicID)
		}
	})

	t.Run("approve_publishes_and_counts", func(t *testing.T) {
		resp, body := postJSON(t, "/admin/v1/videos/"+publicID+"/approve", nil, adminToken)
		if resp.StatusCode != http.StatusNoContent {
			t.Fatalf("approve: status = %d, body = %v", resp.StatusCode, body)
		}
		var status string
		var videoCount int
		if err := testPool.QueryRow(
			context.Background(),
			`SELECT v.status, c.video_count FROM videos v
			 JOIN customers c ON c.id = v.customer_id WHERE v.public_id = $1`, publicID,
		).Scan(&status, &videoCount); err != nil {
			t.Fatalf("query after approve: %v", err)
		}
		if status != "published" {
			t.Errorf("status = %s, want published", status)
		}
		if videoCount != 1 {
			t.Errorf("author video_count = %d, want 1", videoCount)
		}
	})

	t.Run("approve_again_conflict", func(t *testing.T) {
		resp, body := postJSON(t, "/admin/v1/videos/"+publicID+"/approve", nil, adminToken)
		if resp.StatusCode != http.StatusConflict {
			t.Fatalf("status = %d, want 409, body = %v", resp.StatusCode, body)
		}
		if body["code"] != "VIDEO_NOT_REVIEWABLE" {
			t.Errorf("code = %v, want VIDEO_NOT_REVIEWABLE", body["code"])
		}
	})

	t.Run("reject_requires_reason_and_records", func(t *testing.T) {
		rejectID, _ := stagePendingVideo(t, username)

		resp, body := postJSON(t, "/admin/v1/videos/"+rejectID+"/reject", map[string]any{}, adminToken)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("empty reason: status = %d, body = %v", resp.StatusCode, body)
		}

		resp, body = postJSON(t, "/admin/v1/videos/"+rejectID+"/reject", map[string]any{
			"reason": "内容不合规",
		}, adminToken)
		if resp.StatusCode != http.StatusNoContent {
			t.Fatalf("reject: status = %d, body = %v", resp.StatusCode, body)
		}
		var status string
		var notes *string
		if err := testPool.QueryRow(
			context.Background(),
			`SELECT status, review_notes FROM videos WHERE public_id = $1`, rejectID,
		).Scan(&status, &notes); err != nil {
			t.Fatalf("query after reject: %v", err)
		}
		if status != "rejected" || notes == nil || *notes != "内容不合规" {
			t.Errorf("status = %s notes = %v, want rejected with reason", status, notes)
		}
		// 驳回不加作品数
		var videoCount int
		if err := testPool.QueryRow(context.Background(),
			`SELECT video_count FROM customers WHERE id = $1`, customerID).Scan(&videoCount); err != nil {
			t.Fatalf("query count: %v", err)
		}
		if videoCount != 1 {
			t.Errorf("video_count = %d, want 1 (reject must not count)", videoCount)
		}
	})

	t.Run("unknown_video_404", func(t *testing.T) {
		resp, body := postJSON(t, "/admin/v1/videos/nope00000000/approve", nil, adminToken)
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("status = %d, want 404, body = %v", resp.StatusCode, body)
		}
	})

	t.Run("customer_token_rejected", func(t *testing.T) {
		customerToken := loginCustomer(t, username, "secret123")
		resp, _ := getJSON(t, "/admin/v1/videos/pending", customerToken)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("customer token on admin videos: status = %d, want 401", resp.StatusCode)
		}
	})
}
