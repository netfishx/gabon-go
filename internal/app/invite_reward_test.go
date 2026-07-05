package app_test

import (
	"context"
	"net/http"
	"testing"
)

const inviteRewardSeed = int64(123) // 种子迁移金额，对齐现网实跑值

// availableOf 走钱包端点读可用余额。
func availableOf(t *testing.T, token string) int64 {
	t.Helper()
	resp, body := getJSON(t, "/api/v1/wallet", token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("wallet: status = %d, body = %v", resp.StatusCode, body)
	}
	available, ok := body["available"].(float64)
	if !ok {
		t.Fatalf("wallet body missing available: %v", body)
	}
	return int64(available)
}

// flipInviteeOf 用 inviterCode 注册一个新客户并让其凑齐三条件翻转为有效用户，返回其用户名。
func flipInviteeOf(t *testing.T, inviterCode string) string {
	t.Helper()
	invitee := uniqueUsername(t)
	body := registerCustomer(t, invitee, inviterCode)
	registerCustomer(t, uniqueUsername(t), inviteCodeOf(t, body)) // 凑"有成功邀请"
	token := loginCustomer(t, invitee, "secret123")
	approveVideoOf(t, invitee) // 凑"有作品"
	bindPhone(t, token)        // 凑"有联系方式"→ 翻转
	assertValid(t, token, true)
	return invitee
}

// inviteRewardTxCount 统计指向某被邀请人的邀请有效奖励流水条数（ref 无对应 API，走 DB 观察）。
func inviteRewardTxCount(t *testing.T, inviteeUsername string) int {
	t.Helper()
	var n int
	if err := testPool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM transactions
		 WHERE type = 'invite_valid_reward'
		   AND ref_id = (SELECT id FROM customers WHERE username = $1)`,
		inviteeUsername).Scan(&n); err != nil {
		t.Fatalf("count reward tx: %v", err)
	}
	return n
}

func TestInviteRewardOnFlip(t *testing.T) {
	inviter := uniqueUsername(t)
	inviterBody := registerCustomer(t, inviter, "")
	inviterToken := loginCustomer(t, inviter, "secret123")
	if got := availableOf(t, inviterToken); got != 0 {
		t.Fatalf("initial available = %d, want 0", got)
	}

	invitee := flipInviteeOf(t, inviteCodeOf(t, inviterBody))

	// 邀请人入账一笔 123 钻，流水类型与关联单据正确
	if got := availableOf(t, inviterToken); got != inviteRewardSeed {
		t.Fatalf("available after flip = %d, want %d", got, inviteRewardSeed)
	}
	resp, body := getJSON(t, "/api/v1/wallet/transactions", inviterToken)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("transactions: status = %d, body = %v", resp.StatusCode, body)
	}
	items, _ := body["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("transactions = %d 条, want 1: %v", len(items), body)
	}
	first, _ := items[0].(map[string]any)
	if got := first["type"]; got != "invite_valid_reward" {
		t.Errorf("tx type = %v, want invite_valid_reward", got)
	}
	if got, _ := first["amount"].(float64); int64(got) != inviteRewardSeed {
		t.Errorf("tx amount = %v, want %d", first["amount"], inviteRewardSeed)
	}
	if n := inviteRewardTxCount(t, invitee); n != 1 {
		t.Errorf("reward tx count for invitee = %d, want 1", n)
	}

	// 幂等：翻转后再触发任一路径不再发奖
	inviteeToken := loginCustomer(t, invitee, "secret123")
	bindPhone(t, inviteeToken)
	approveVideoOf(t, invitee)
	if got := availableOf(t, inviterToken); got != inviteRewardSeed {
		t.Errorf("available after re-trigger = %d, want %d (no double grant)", got, inviteRewardSeed)
	}
}

func TestInviteRewardCap(t *testing.T) {
	// 普通档（level 0）invite_reward_cap = 5：第 6 个有效下级翻转成功但跳过发放
	inviter := uniqueUsername(t)
	inviterBody := registerCustomer(t, inviter, "")
	inviterToken := loginCustomer(t, inviter, "secret123")
	code := inviteCodeOf(t, inviterBody)

	for i := 1; i <= 6; i++ {
		invitee := flipInviteeOf(t, code)
		want := inviteRewardSeed * int64(min(i, 5))
		if got := availableOf(t, inviterToken); got != want {
			t.Fatalf("after invitee %d: available = %d, want %d", i, got, want)
		}
		wantTx := 1
		if i == 6 {
			wantTx = 0
		}
		if n := inviteRewardTxCount(t, invitee); n != wantTx {
			t.Errorf("invitee %d reward tx = %d, want %d", i, n, wantTx)
		}
	}
}

func TestInviteRewardConfigDisabled(t *testing.T) {
	ctx := context.Background()
	if _, err := testPool.Exec(ctx,
		`UPDATE activity_reward_configs SET enabled = false WHERE kind = 'invite_valid'`); err != nil {
		t.Fatalf("disable config: %v", err)
	}
	t.Cleanup(func() {
		if _, err := testPool.Exec(ctx,
			`UPDATE activity_reward_configs SET enabled = true WHERE kind = 'invite_valid'`); err != nil {
			t.Fatalf("re-enable config: %v", err)
		}
	})

	inviter := uniqueUsername(t)
	inviterBody := registerCustomer(t, inviter, "")
	inviterToken := loginCustomer(t, inviter, "secret123")

	// 翻转照常发生，但不入账（不复刻旧版代码 123 兜底）
	invitee := flipInviteeOf(t, inviteCodeOf(t, inviterBody))
	if got := availableOf(t, inviterToken); got != 0 {
		t.Errorf("available with disabled config = %d, want 0", got)
	}
	if n := inviteRewardTxCount(t, invitee); n != 0 {
		t.Errorf("reward tx with disabled config = %d, want 0", n)
	}
}

func TestInviteRewardConfigMissing(t *testing.T) {
	// 配置行整体缺失（与停用同为"不发放"，验收清单并列两种）
	ctx := context.Background()
	if _, err := testPool.Exec(ctx,
		`DELETE FROM activity_reward_configs WHERE kind = 'invite_valid'`); err != nil {
		t.Fatalf("delete config: %v", err)
	}
	t.Cleanup(func() {
		if _, err := testPool.Exec(ctx,
			`INSERT INTO activity_reward_configs (kind, threshold, reward, enabled)
			 VALUES ('invite_valid', 0, 123, true)`); err != nil {
			t.Fatalf("restore config: %v", err)
		}
	})

	inviter := uniqueUsername(t)
	inviterBody := registerCustomer(t, inviter, "")
	inviterToken := loginCustomer(t, inviter, "secret123")

	invitee := flipInviteeOf(t, inviteCodeOf(t, inviterBody))
	if got := availableOf(t, inviterToken); got != 0 {
		t.Errorf("available with missing config = %d, want 0", got)
	}
	if n := inviteRewardTxCount(t, invitee); n != 0 {
		t.Errorf("reward tx with missing config = %d, want 0", n)
	}
}

func TestNaturalRegistrantFlipNoReward(t *testing.T) {
	// 自然注册（无邀请人）翻转：正常完成、不产生任何奖励
	natural := uniqueUsername(t)
	body := registerCustomer(t, natural, "")
	token := loginCustomer(t, natural, "secret123")

	registerCustomer(t, uniqueUsername(t), inviteCodeOf(t, body))
	approveVideoOf(t, natural)
	bindPhone(t, token)
	assertValid(t, token, true)

	if n := inviteRewardTxCount(t, natural); n != 0 {
		t.Errorf("reward tx for natural registrant = %d, want 0", n)
	}
	if got := availableOf(t, token); got != 0 {
		t.Errorf("own available = %d, want 0", got)
	}
}
