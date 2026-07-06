package app_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// createImageUpload 申请图片预签名并真实 PUT，返回 storage_path。
func createImageUpload(t *testing.T, token, kind, ext string) string {
	t.Helper()
	resp, body := postJSON(t, "/api/v1/uploads/images", map[string]any{
		"kind": kind, "ext": ext,
	}, token)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create image upload: status = %d, body = %v", resp.StatusCode, body)
	}
	path, _ := body["storage_path"].(string)
	uploadURL, _ := body["upload_url"].(string)
	if path == "" || uploadURL == "" {
		t.Fatalf("create image upload: missing fields, body = %v", body)
	}
	putBytes(t, uploadURL, []byte("fake-image-bytes"))
	return path
}

func TestAvatarUploadEndToEnd(t *testing.T) {
	username := uniqueUsername(t)
	registerCustomer(t, username, "")
	token := loginCustomer(t, username, "secret123")

	// 未设置头像时 avatar_url 为 null
	_, me := getJSON(t, "/api/v1/me", token)
	if got := me["avatar_url"]; got != nil {
		t.Errorf("avatar_url before set = %v, want null", got)
	}

	cid := customerIDOf(t, username)
	path := createImageUpload(t, token, "avatar", "png")
	wantPrefix := fmt.Sprintf("avatars/%d/", cid)
	if !strings.HasPrefix(path, wantPrefix) {
		t.Fatalf("storage_path = %q, want prefix %s", path, wantPrefix)
	}
	if !strings.HasSuffix(path, ".png") {
		t.Fatalf("storage_path = %q, want .png suffix", path)
	}

	// 消费：改资料设头像
	resp, body := doJSON(t, http.MethodPatch, "/api/v1/me/profile", token, map[string]any{
		"avatar_path": path,
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("set avatar: status = %d, body = %v", resp.StatusCode, body)
	}

	// 展示：/me 输出 CDN 完整 URL（CDN 基址 + "/" + path，与视频封面同模式）
	_, me = getJSON(t, "/api/v1/me", token)
	avatarURL, _ := me["avatar_url"].(string)
	if avatarURL == "" || !strings.HasSuffix(avatarURL, "/"+path) || !strings.HasPrefix(avatarURL, "http") {
		t.Errorf("avatar_url = %q, want CDN url ending with /%s", avatarURL, path)
	}

	// 换头像：重复流程覆盖
	path2 := createImageUpload(t, token, "avatar", "jpg")
	if resp, body := doJSON(t, http.MethodPatch, "/api/v1/me/profile", token, map[string]any{
		"avatar_path": path2,
	}); resp.StatusCode != http.StatusOK {
		t.Fatalf("replace avatar: status = %d, body = %v", resp.StatusCode, body)
	}
	_, me = getJSON(t, "/api/v1/me", token)
	if got, _ := me["avatar_url"].(string); !strings.HasSuffix(got, "/"+path2) {
		t.Errorf("avatar_url after replace = %q, want ending with /%s", got, path2)
	}
}

func TestProofUploadChannel(t *testing.T) {
	username := uniqueUsername(t)
	registerCustomer(t, username, "")
	token := loginCustomer(t, username, "secret123")
	cid := customerIDOf(t, username)

	path := createImageUpload(t, token, "proof", "webp")
	wantPrefix := fmt.Sprintf("proofs/%d/", cid)
	if !strings.HasPrefix(path, wantPrefix) {
		t.Fatalf("storage_path = %q, want prefix %s", path, wantPrefix)
	}
}

func TestTeamMemberAvatarURL(t *testing.T) {
	inviter := uniqueUsername(t)
	body := registerCustomer(t, inviter, "")
	inviterToken := loginCustomer(t, inviter, "secret123")

	invitee := uniqueUsername(t)
	registerCustomer(t, invitee, inviteCodeOf(t, body))
	inviteeToken := loginCustomer(t, invitee, "secret123")

	// 未设头像：avatar_url 为 null
	_, list := teamMembers(t, inviterToken, "", "")
	items, _ := list["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("team members = %d, want 1: %v", len(items), list)
	}
	first, _ := items[0].(map[string]any)
	if got := first["avatar_url"]; got != nil {
		t.Errorf("member avatar_url before set = %v, want null", got)
	}

	// 设头像后：输出 CDN URL
	path := createImageUpload(t, inviteeToken, "avatar", "png")
	if resp, b := doJSON(t, http.MethodPatch, "/api/v1/me/profile", inviteeToken, map[string]any{
		"avatar_path": path,
	}); resp.StatusCode != http.StatusOK {
		t.Fatalf("set avatar: status = %d, body = %v", resp.StatusCode, b)
	}
	_, list = teamMembers(t, inviterToken, "", "")
	items, _ = list["items"].([]any)
	first, _ = items[0].(map[string]any)
	if got, _ := first["avatar_url"].(string); !strings.HasSuffix(got, "/"+path) {
		t.Errorf("member avatar_url = %q, want ending with /%s", got, path)
	}
}

func TestImageUploadGuards(t *testing.T) {
	username := uniqueUsername(t)
	registerCustomer(t, username, "")
	token := loginCustomer(t, username, "secret123")

	other := uniqueUsername(t)
	registerCustomer(t, other, "")
	otherToken := loginCustomer(t, other, "secret123")
	otherPath := createImageUpload(t, otherToken, "avatar", "png")

	t.Run("presign_bad_kind", func(t *testing.T) {
		resp, body := postJSON(t, "/api/v1/uploads/images", map[string]any{"kind": "video", "ext": "png"}, token)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400, body = %v", resp.StatusCode, body)
		}
	})
	t.Run("presign_bad_ext", func(t *testing.T) {
		resp, body := postJSON(t, "/api/v1/uploads/images", map[string]any{"kind": "avatar", "ext": "exe"}, token)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400, body = %v", resp.StatusCode, body)
		}
	})
	t.Run("presign_unauthenticated", func(t *testing.T) {
		resp, _ := postJSON(t, "/api/v1/uploads/images", map[string]any{"kind": "avatar"}, "")
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", resp.StatusCode)
		}
	})
	t.Run("avatar_foreign_path_rejected", func(t *testing.T) {
		resp, body := doJSON(t, http.MethodPatch, "/api/v1/me/profile", token, map[string]any{
			"avatar_path": otherPath, // 他人上传的真实存在对象
		})
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("status = %d, want 403, body = %v", resp.StatusCode, body)
		}
	})
	t.Run("avatar_missing_object_rejected", func(t *testing.T) {
		cid := customerIDOf(t, username)
		resp, body := doJSON(t, http.MethodPatch, "/api/v1/me/profile", token, map[string]any{
			"avatar_path": fmt.Sprintf("avatars/%d/nonexistent.png", cid),
		})
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400, body = %v", resp.StatusCode, body)
		}
	})
	t.Run("avatar_proof_prefix_rejected", func(t *testing.T) {
		// proof 通道的路径不能当头像用（kind 前缀不符）
		proofPath := createImageUpload(t, token, "proof", "png")
		resp, body := doJSON(t, http.MethodPatch, "/api/v1/me/profile", token, map[string]any{
			"avatar_path": proofPath,
		})
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("status = %d, want 403, body = %v", resp.StatusCode, body)
		}
	})
}
