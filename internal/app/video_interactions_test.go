package app_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"
)

func jsonBody(t *testing.T, body map[string]any) io.Reader {
	t.Helper()
	if body == nil {
		return http.NoBody
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	return bytes.NewReader(raw)
}

func videoCounters(t *testing.T, publicID string) (click, valid, like, comment, hot int64) {
	t.Helper()
	if err := testPool.QueryRow(
		context.Background(),
		`SELECT click_count, valid_play_count, like_count, comment_count, hot_score
		 FROM videos WHERE public_id = $1`, publicID,
	).Scan(&click, &valid, &like, &comment, &hot); err != nil {
		t.Fatalf("query counters: %v", err)
	}
	return click, valid, like, comment, hot
}

func doJSON(t *testing.T, method, path, token string, body map[string]any) (*http.Response, map[string]any) {
	t.Helper()
	req, err := http.NewRequest(method, testServer.URL+path, jsonBody(t, body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	var decoded map[string]any
	if err := decodeBody(resp, &decoded); err != nil {
		decoded = nil
	}
	return resp, decoded
}

func TestLikeAntiFarming(t *testing.T) {
	username := uniqueUsername(t)
	registerCustomer(t, username, "")
	token := loginCustomer(t, username, "secret123")
	cid := customerIDOf(t, username)
	pid := stagePublishedVideo(t, cid, 0)

	// 首次点赞：计数 +1，热度 +2
	if resp, body := doJSON(t, http.MethodPost, "/api/v1/videos/"+pid+"/like", token, nil); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("like: status = %d, body = %v", resp.StatusCode, body)
	}
	_, _, like, _, hot := videoCounters(t, pid)
	if like != 1 || hot != 2 {
		t.Fatalf("after like: like = %d hot = %d, want 1 and 2", like, hot)
	}

	// 重复点赞幂等
	doJSON(t, http.MethodPost, "/api/v1/videos/"+pid+"/like", token, nil)
	if _, _, like, _, hot = videoCounters(t, pid); like != 1 || hot != 2 {
		t.Fatalf("after double like: like = %d hot = %d, want 1 and 2", like, hot)
	}

	// 取消：计数 -1，热度不减（只增不减）
	if resp, _ := doJSON(t, http.MethodDelete, "/api/v1/videos/"+pid+"/like", token, nil); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("unlike: status = %d", resp.StatusCode)
	}
	if _, _, like, _, hot = videoCounters(t, pid); like != 0 || hot != 2 {
		t.Fatalf("after unlike: like = %d hot = %d, want 0 and 2", like, hot)
	}

	// 再赞：计数恢复，热度不再增加（防刷分）
	if resp, _ := doJSON(t, http.MethodPost, "/api/v1/videos/"+pid+"/like", token, nil); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("re-like: status = %d", resp.StatusCode)
	}
	if _, _, like, _, hot = videoCounters(t, pid); like != 1 || hot != 2 {
		t.Errorf("after re-like: like = %d hot = %d, want 1 and 2 (no farming)", like, hot)
	}
}

func TestCommentDailyLimitAndScoring(t *testing.T) {
	username := uniqueUsername(t)
	registerCustomer(t, username, "")
	token := loginCustomer(t, username, "secret123")
	cid := customerIDOf(t, username)
	pid := stagePublishedVideo(t, cid, 0)

	resp, body := doJSON(t, http.MethodPost, "/api/v1/videos/"+pid+"/comments", token, map[string]any{
		"content": "第一条评论",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("comment: status = %d, body = %v", resp.StatusCode, body)
	}
	commentID, _ := body["id"].(float64)
	if commentID == 0 {
		t.Fatalf("comment missing id, body = %v", body)
	}
	if _, _, _, comment, hot := videoCounters(t, pid); comment != 1 || hot != 5 {
		t.Fatalf("after comment: comment = %d hot = %d, want 1 and 5", comment, hot)
	}

	// 当日第二条被拒
	resp, body = doJSON(t, http.MethodPost, "/api/v1/videos/"+pid+"/comments", token, map[string]any{
		"content": "第二条",
	})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("second comment: status = %d, want 409, body = %v", resp.StatusCode, body)
	}
	if body["code"] != "VIDEO_COMMENT_DAILY_LIMIT" {
		t.Errorf("code = %v, want VIDEO_COMMENT_DAILY_LIMIT", body["code"])
	}

	// 删除后当日仍不可再评（唯一约束含软删行），计数 -1、热度不变
	resp, _ = doJSON(t, http.MethodDelete, fmt.Sprintf("/api/v1/comments/%d", int64(commentID)), token, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete comment: status = %d", resp.StatusCode)
	}
	if _, _, _, comment, hot := videoCounters(t, pid); comment != 0 || hot != 5 {
		t.Errorf("after delete: comment = %d hot = %d, want 0 and 5", comment, hot)
	}
	resp, body = doJSON(t, http.MethodPost, "/api/v1/videos/"+pid+"/comments", token, map[string]any{
		"content": "删后再评",
	})
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("comment after delete same day: status = %d, want 409, body = %v", resp.StatusCode, body)
	}

	// 评论列表只见存活评论（公开访问）
	resp, body = getJSON(t, "/api/v1/videos/"+pid+"/comments", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list comments: status = %d", resp.StatusCode)
	}
	if items, _ := body["items"].([]any); len(items) != 0 {
		t.Errorf("comments list = %d items, want 0 (deleted hidden)", len(items))
	}
}

func TestPlayTwoPhase(t *testing.T) {
	username := uniqueUsername(t)
	registerCustomer(t, username, "")
	token := loginCustomer(t, username, "secret123")
	cid := customerIDOf(t, username)
	pid := stagePublishedVideo(t, cid, 0)

	// 播放点击不去重
	resp, body := doJSON(t, http.MethodPost, "/api/v1/videos/"+pid+"/plays", token, nil)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("play: status = %d, body = %v", resp.StatusCode, body)
	}
	playID, _ := body["play_id"].(float64)
	if playID == 0 {
		t.Fatalf("play missing play_id, body = %v", body)
	}
	doJSON(t, http.MethodPost, "/api/v1/videos/"+pid+"/plays", token, nil)
	click, valid, _, _, hot := videoCounters(t, pid)
	if click != 2 || valid != 0 || hot != 2 {
		t.Fatalf("after 2 plays: click = %d valid = %d hot = %d, want 2/0/2", click, valid, hot)
	}

	// 有效播放：同一事件 id 只计一次
	if resp, _ := doJSON(t, http.MethodPost, fmt.Sprintf("/api/v1/plays/%d/valid", int64(playID)), token, nil); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("valid play: status = %d", resp.StatusCode)
	}
	if resp, _ := doJSON(t, http.MethodPost, fmt.Sprintf("/api/v1/plays/%d/valid", int64(playID)), token, nil); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("valid play repeat: status = %d", resp.StatusCode)
	}
	click, valid, _, _, hot = videoCounters(t, pid)
	if click != 2 || valid != 1 || hot != 3 {
		t.Errorf("after valid: click = %d valid = %d hot = %d, want 2/1/3", click, valid, hot)
	}
}

func TestInteractionsGuards(t *testing.T) {
	username := uniqueUsername(t)
	registerCustomer(t, username, "")
	token := loginCustomer(t, username, "secret123")
	pendingID, _ := stagePendingVideo(t, username)

	// 未发布视频不可互动
	if resp, _ := doJSON(t, http.MethodPost, "/api/v1/videos/"+pendingID+"/like", token, nil); resp.StatusCode != http.StatusNotFound {
		t.Errorf("like pending: status = %d, want 404", resp.StatusCode)
	}
	// 未登录不可互动
	cid := customerIDOf(t, username)
	pid := stagePublishedVideo(t, cid, 0)
	if resp, _ := doJSON(t, http.MethodPost, "/api/v1/videos/"+pid+"/like", "", nil); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("like without token: status = %d, want 401", resp.StatusCode)
	}
	// 删他人评论 404
	other := uniqueUsername(t)
	registerCustomer(t, other, "")
	otherToken := loginCustomer(t, other, "secret123")
	resp, body := doJSON(t, http.MethodPost, "/api/v1/videos/"+pid+"/comments", token, map[string]any{"content": "我的评论"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("comment: status = %d", resp.StatusCode)
	}
	commentID := int64(body["id"].(float64))
	if resp, _ := doJSON(t, http.MethodDelete, fmt.Sprintf("/api/v1/comments/%d", commentID), otherToken, nil); resp.StatusCode != http.StatusNotFound {
		t.Errorf("delete others comment: status = %d, want 404", resp.StatusCode)
	}
}
