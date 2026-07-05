package app_test

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// putBytes 模拟客户端直传：对预签名地址发起真实 PUT。
func putBytes(t *testing.T, url string, body []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPut, url, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("new put request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("put upload: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("put upload: status = %d", resp.StatusCode)
	}
}

// createUpload 请求预签名并真实上传，返回 storage_path。
func createUpload(t *testing.T, token string, content []byte) string {
	t.Helper()
	resp, body := postJSON(t, "/api/v1/videos/uploads", nil, token)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create upload: status = %d, body = %v", resp.StatusCode, body)
	}
	path, _ := body["storage_path"].(string)
	uploadURL, _ := body["upload_url"].(string)
	if path == "" || uploadURL == "" {
		t.Fatalf("create upload: missing fields, body = %v", body)
	}
	putBytes(t, uploadURL, content)
	return path
}

func TestVideoUploadAndConfirm(t *testing.T) {
	username := uniqueUsername(t)
	registerCustomer(t, username, "")
	token := loginCustomer(t, username, "secret123")

	path := createUpload(t, token, []byte("fake-video-bytes"))
	if !strings.Contains(path, "videos/") {
		t.Errorf("storage_path = %q, want videos/ prefix", path)
	}

	resp, body := postJSON(t, "/api/v1/videos", map[string]any{
		"storage_path": path,
		"title":        "我的第一条视频",
		"tags":         []string{"生活", "日常"},
	}, token)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("confirm: status = %d, body = %v", resp.StatusCode, body)
	}
	if body["status"] != "pending_transcode" {
		t.Errorf("status = %v, want pending_transcode", body["status"])
	}
	publicID, _ := body["public_id"].(string)
	if len(publicID) != 12 {
		t.Errorf("public_id = %q, want 12-char short code", publicID)
	}

	// 转码任务已入队（DB 可观察结果；worker 是下一切片）
	var jobStatus string
	err := testPool.QueryRow(
		context.Background(),
		`SELECT tj.status FROM transcode_jobs tj
		 JOIN videos v ON v.id = tj.video_id WHERE v.public_id = $1`, publicID,
	).Scan(&jobStatus)
	if err != nil {
		t.Fatalf("query transcode job: %v", err)
	}
	if jobStatus != "queued" {
		t.Errorf("job status = %s, want queued", jobStatus)
	}
}

func TestVideoConfirmGuards(t *testing.T) {
	username := uniqueUsername(t)
	registerCustomer(t, username, "")
	token := loginCustomer(t, username, "secret123")
	path := createUpload(t, token, []byte("guard-video"))

	var otherID int64
	other := uniqueUsername(t)
	registerCustomer(t, other, "")
	if err := testPool.QueryRow(context.Background(),
		`SELECT id FROM customers WHERE username = $1`, other).Scan(&otherID); err != nil {
		t.Fatalf("query other id: %v", err)
	}

	tests := []struct {
		name       string
		body       map[string]any
		wantStatus int
		wantCode   string
	}{
		{
			name:       "foreign_path",
			body:       map[string]any{"storage_path": fmt.Sprintf("videos/%d/stolen.mp4", otherID), "title": "t"},
			wantStatus: http.StatusForbidden,
			wantCode:   "VIDEO_PATH_FORBIDDEN",
		},
		{
			name:       "too_many_tags",
			body:       map[string]any{"storage_path": path, "title": "t", "tags": []string{"a", "b", "c", "d"}},
			wantStatus: http.StatusBadRequest,
			wantCode:   "COMMON_INVALID_ARGUMENT",
		},
		{
			name:       "missing_title",
			body:       map[string]any{"storage_path": path},
			wantStatus: http.StatusBadRequest,
			wantCode:   "COMMON_INVALID_ARGUMENT",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, body := postJSON(t, "/api/v1/videos", tt.body, token)
			if resp.StatusCode != tt.wantStatus {
				t.Fatalf("status = %d, want %d, body = %v", resp.StatusCode, tt.wantStatus, body)
			}
			if body["code"] != tt.wantCode {
				t.Errorf("code = %v, want %s", body["code"], tt.wantCode)
			}
		})
	}

	t.Run("object_missing_own_prefix", func(t *testing.T) {
		var cid int64
		if err := testPool.QueryRow(context.Background(),
			`SELECT id FROM customers WHERE username = $1`, username).Scan(&cid); err != nil {
			t.Fatalf("query id: %v", err)
		}
		resp, body := postJSON(t, "/api/v1/videos", map[string]any{
			"storage_path": fmt.Sprintf("videos/%d/never-uploaded.mp4", cid), "title": "t",
		}, token)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400, body = %v", resp.StatusCode, body)
		}
		if body["code"] != "VIDEO_OBJECT_MISSING" {
			t.Errorf("code = %v, want VIDEO_OBJECT_MISSING", body["code"])
		}
	})

	t.Run("without_token", func(t *testing.T) {
		if resp, _ := postJSON(t, "/api/v1/videos/uploads", nil, ""); resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("uploads without token: status = %d, want 401", resp.StatusCode)
		}
	})
}
