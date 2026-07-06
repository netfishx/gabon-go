package app_test

import (
	"net/http"
	"strconv"
	"testing"
)

// teamChain 注册 A→B→C→D→E 五级邀请链，返回各自用户名与 A 的 token。
type teamChain struct {
	names  [5]string // A B C D E
	codes  [5]string
	tokens map[string]string
}

func buildTeamChain(t *testing.T) *teamChain {
	t.Helper()
	c := &teamChain{tokens: map[string]string{}}
	prevCode := ""
	for i := range c.names {
		c.names[i] = uniqueUsername(t)
		body := registerCustomer(t, c.names[i], prevCode)
		c.codes[i] = inviteCodeOf(t, body)
		prevCode = c.codes[i]
	}
	return c
}

func (c *teamChain) token(t *testing.T, i int) string {
	t.Helper()
	name := c.names[i]
	if c.tokens[name] == "" {
		c.tokens[name] = loginCustomer(t, name, "secret123")
	}
	return c.tokens[name]
}

// publicIDOf 查客户对外短码。
func publicIDOf(t *testing.T, username string) string {
	t.Helper()
	_, me := getJSON(t, "/api/v1/me", loginCustomer(t, username, "secret123"))
	id, _ := me["public_id"].(string)
	if id == "" {
		t.Fatalf("missing public_id for %s", username)
	}
	return id
}

// teamMembers 调团队下级列表端点。
func teamMembers(t *testing.T, token, parentPublicID, extra string) (*http.Response, map[string]any) {
	t.Helper()
	path := "/api/v1/team/members"
	sep := "?"
	if parentPublicID != "" {
		path += sep + "parent=" + parentPublicID
		sep = "&"
	}
	if extra != "" {
		path += sep + extra
	}
	return getJSON(t, path, token)
}

// memberUsernames 从响应体提取 username 列表。
func memberUsernames(t *testing.T, body map[string]any) []string {
	t.Helper()
	items, _ := body["items"].([]any)
	out := make([]string, 0, len(items))
	for _, it := range items {
		m, _ := it.(map[string]any)
		name, _ := m["username"].(string)
		out = append(out, name)
	}
	return out
}

func TestTeamMembersDrillDown(t *testing.T) {
	chain := buildTeamChain(t)
	tokenA := chain.token(t, 0)

	// 缺省 parent = 本人：A 的直接下级只有 B
	resp, body := teamMembers(t, tokenA, "", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("default parent: status = %d, body = %v", resp.StatusCode, body)
	}
	if got := memberUsernames(t, body); len(got) != 1 || got[0] != chain.names[1] {
		t.Fatalf("A members = %v, want [%s]", got, chain.names[1])
	}

	// 下钻 B（深度 1）→ C；下钻 C（深度 2）→ D（深度 3 成员可见）
	for parent, want := 1, 2; parent <= 2; parent, want = parent+1, want+1 {
		resp, body := teamMembers(t, tokenA, publicIDOf(t, chain.names[parent]), "")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("drill %s: status = %d, body = %v", chain.names[parent], resp.StatusCode, body)
		}
		if got := memberUsernames(t, body); len(got) != 1 || got[0] != chain.names[want] {
			t.Fatalf("members under %s = %v, want [%s]", chain.names[parent], got, chain.names[want])
		}
	}

	// 字段断言：B 下的 C 带直接下级数（D 一人）
	_, body = teamMembers(t, tokenA, publicIDOf(t, chain.names[1]), "")
	items, _ := body["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("B members = %d 条, want 1: %v", len(items), body)
	}
	first, _ := items[0].(map[string]any)
	if got, _ := first["subordinate_count"].(float64); got != 1 {
		t.Errorf("C.subordinate_count = %v, want 1", first["subordinate_count"])
	}

	// 边界断言：C 下的 D 是深度 3 成员，其下级（E，深度 4）在团队之外，
	// 计数必须归 0——不向查看者泄漏界外结构（PR #27 review P2）
	_, body = teamMembers(t, tokenA, publicIDOf(t, chain.names[2]), "")
	items, _ = body["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("C members = %d 条, want 1: %v", len(items), body)
	}
	dRow, _ := items[0].(map[string]any)
	if got, _ := dRow["subordinate_count"].(float64); got != 0 {
		t.Errorf("D.subordinate_count = %v, want 0（深度 4 不在团队内）", dRow["subordinate_count"])
	}
	if got, _ := first["valid"].(bool); got {
		t.Errorf("C.valid = true, want false (未凑齐条件)")
	}
	if _, ok := first["public_id"].(string); !ok {
		t.Errorf("member missing public_id: %v", first)
	}
	if _, ok := first["avatar_path"]; !ok {
		t.Errorf("member missing avatar_path field: %v", first)
	}
}

func TestTeamMembersValidFlag(t *testing.T) {
	chain := buildTeamChain(t)
	tokenA := chain.token(t, 0)
	tokenB := chain.token(t, 1)

	// B 凑齐三条件翻转（有 C 邀请、审核作品、绑手机）
	approveVideoOf(t, chain.names[1])
	bindPhone(t, tokenB)
	assertValid(t, tokenB, true)

	_, body := teamMembers(t, tokenA, "", "")
	items, _ := body["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("A members = %d 条, want 1: %v", len(items), body)
	}
	first, _ := items[0].(map[string]any)
	if got, _ := first["valid"].(bool); !got {
		t.Errorf("B.valid = %v, want true", first["valid"])
	}
}

func TestTeamMembersGuard(t *testing.T) {
	chain := buildTeamChain(t)
	tokenA := chain.token(t, 0)

	// 深度 3 成员（D）不能作为 parent
	resp, body := teamMembers(t, tokenA, publicIDOf(t, chain.names[3]), "")
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("depth-3 parent: status = %d, want 403, body = %v", resp.StatusCode, body)
	}
	if got := body["code"]; got != "CUSTOMER_TEAM_FORBIDDEN" {
		t.Errorf("code = %v, want CUSTOMER_TEAM_FORBIDDEN", got)
	}

	// 团队外客户不能作为 parent（哪怕对方真实存在）
	outsider := uniqueUsername(t)
	registerCustomer(t, outsider, "")
	resp, body = teamMembers(t, tokenA, publicIDOf(t, outsider), "")
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("outsider parent: status = %d, want 403, body = %v", resp.StatusCode, body)
	}

	// 不存在的 parent → 404
	resp, body = teamMembers(t, tokenA, "nope00000000", "")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown parent: status = %d, want 404, body = %v", resp.StatusCode, body)
	}

	// 未登录 → 401
	resp, _ = teamMembers(t, "", "", "")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated: status = %d, want 401", resp.StatusCode)
	}
}

func TestTeamMembersPagination(t *testing.T) {
	inviter := uniqueUsername(t)
	body := registerCustomer(t, inviter, "")
	code := inviteCodeOf(t, body)
	token := loginCustomer(t, inviter, "secret123")

	want := map[string]bool{}
	for range 5 {
		name := uniqueUsername(t)
		registerCustomer(t, name, code)
		want[name] = true
	}

	// limit=2 翻页收集全部，断言不重不漏
	got := map[string]bool{}
	cursor := ""
	for range 4 { // 最多 3 页 + 保险
		extra := "limit=2"
		if cursor != "" {
			extra += "&cursor=" + cursor
		}
		resp, body := teamMembers(t, token, "", extra)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("page: status = %d, body = %v", resp.StatusCode, body)
		}
		for _, name := range memberUsernames(t, body) {
			if got[name] {
				t.Fatalf("duplicate member across pages: %s", name)
			}
			got[name] = true
		}
		next, _ := body["next_cursor"].(float64)
		if next == 0 {
			break
		}
		cursor = strconv.FormatInt(int64(next), 10)
	}
	if len(got) != len(want) {
		t.Fatalf("collected %d members, want %d", len(got), len(want))
	}
	for name := range want {
		if !got[name] {
			t.Errorf("missing member %s", name)
		}
	}
}
