package app_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 全链路冒烟（技术决策附录"ffmpeg 小样本真转码冒烟"）：
// 预签名 → 真 PUT → confirm → worker 真 ffmpeg 转码 → HLS/首帧产物落对象存储 → 视频待审核。
// 需要本机/CI 具备 ffmpeg（CI 已在 workflow 安装）。
func TestTranscodePipelineSmoke(t *testing.T) {
	sample := makeSampleVideo(t)
	raw, err := os.ReadFile(sample)
	if err != nil {
		t.Fatalf("read sample: %v", err)
	}

	username := uniqueUsername(t)
	registerCustomer(t, username, "")
	token := loginCustomer(t, username, "secret123")
	path := createUpload(t, token, raw)

	resp, body := postJSON(t, "/api/v1/videos", map[string]any{
		"storage_path": path, "title": "冒烟样本",
	}, token)
	if resp.StatusCode != 201 {
		t.Fatalf("confirm: status = %d, body = %v", resp.StatusCode, body)
	}
	publicID, _ := body["public_id"].(string)

	// 轮询等待 worker 完成（DB 可观察结果）
	deadline := time.Now().Add(90 * time.Second)
	var status string
	var hlsPath, thumbPath *string
	var duration *int32
	for {
		err := testPool.QueryRow(
			context.Background(),
			`SELECT status, hls_path, thumbnail_path, duration FROM videos WHERE public_id = $1`, publicID,
		).Scan(&status, &hlsPath, &thumbPath, &duration)
		if err != nil {
			t.Fatalf("poll video: %v", err)
		}
		if status == "pending_review" || status == "transcode_failed" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("transcode did not finish in time, status = %s", status)
		}
		time.Sleep(500 * time.Millisecond)
	}

	if status != "pending_review" {
		t.Fatalf("status = %s, want pending_review", status)
	}
	if hlsPath == nil || thumbPath == nil || duration == nil {
		t.Fatalf("media fields missing: hls=%v thumb=%v dur=%v", hlsPath, thumbPath, duration)
	}
	// 对外契约：媒体路径（进 CDN URL）只允许公开短码，不得泄漏内部自增 id
	if !strings.Contains(*hlsPath, publicID) || !strings.Contains(*thumbPath, publicID) {
		t.Errorf("media paths must use public_id: hls=%s thumb=%s public_id=%s", *hlsPath, *thumbPath, publicID)
	}
	if *duration < 1 {
		t.Errorf("duration = %d, want >= 1", *duration)
	}

	// 产物真实存在于对象存储
	for _, object := range []string{*hlsPath, *thumbPath} {
		exists, err := testStore.Exists(context.Background(), object)
		if err != nil {
			t.Fatalf("stat %s: %v", object, err)
		}
		if !exists {
			t.Errorf("object %s missing from storage", object)
		}
	}
}

// makeSampleVideo 用 ffmpeg testsrc 合成 1 秒小样本。
func makeSampleVideo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Fatalf("ffmpeg not installed (required for smoke test): %v", err)
	}
	out := filepath.Join(t.TempDir(), "sample.mp4")
	cmd := exec.Command("ffmpeg", "-y",
		"-f", "lavfi", "-i", "testsrc=duration=1:size=320x240:rate=15",
		"-c:v", "libx264", "-pix_fmt", "yuv420p", out)
	if raw, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("synthesize sample: %v: %s", err, raw[max(0, len(raw)-300):])
	}
	return out
}
