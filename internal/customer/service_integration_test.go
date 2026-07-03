package customer

import (
	"context"
	"strings"
	"testing"

	"github.com/netfishx/gabon-go/internal/testdb"
)

// 集成测试：短码唯一冲突重试（PRD Testing Decisions 三项之三）。
// 通过注入生成器强制邀请码碰撞，验证 Register 重试后成功、耗尽后报错。

func TestRegisterRetriesOnInviteCodeCollision(t *testing.T) {
	ctx := context.Background()
	pool, cleanup, err := testdb.Start(ctx)
	if err != nil {
		t.Fatalf("start testdb: %v", err)
	}
	t.Cleanup(cleanup)

	svc := NewService(pool)
	first, err := svc.Register(ctx, "collide_a", "secret123", "")
	if err != nil {
		t.Fatalf("register first: %v", err)
	}

	// 第一次生成撞上既有邀请码，之后恢复真实生成器
	calls := 0
	svc.genInviteCode = func() (string, error) {
		calls++
		if calls == 1 {
			return first.InviteCode, nil
		}
		return newInviteCode()
	}

	second, err := svc.Register(ctx, "collide_b", "secret123", "")
	if err != nil {
		t.Fatalf("register with forced collision: %v", err)
	}
	if calls < 2 {
		t.Errorf("generator calls = %d, want >= 2 (retry expected)", calls)
	}
	if second.InviteCode == first.InviteCode {
		t.Errorf("invite code not regenerated after collision")
	}

	// 钱包等副作用在重试后的成功事务中完整存在
	var available int64
	if err := pool.QueryRow(
		ctx,
		`SELECT available FROM wallets WHERE customer_id = $1`, second.ID,
	).Scan(&available); err != nil {
		t.Fatalf("wallet row after retry: %v", err)
	}

	t.Run("exhausts_retries", func(t *testing.T) {
		svc.genInviteCode = func() (string, error) { return first.InviteCode, nil }
		_, err := svc.Register(ctx, "collide_c", "secret123", "")
		if err == nil || !strings.Contains(err.Error(), "exhausted") {
			t.Fatalf("err = %v, want exhausted short code retries", err)
		}
	})
}
