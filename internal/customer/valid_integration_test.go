package customer

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/netfishx/gabon-go/internal/testdb"
	"github.com/netfishx/gabon-go/internal/wallet"
)

// 并发集成测（issue #24 验收）：多协程同时从**不同触发路径**对同一客户做有效用户判定——
// 直调判定原语（= 视频审核通过路径的钩子调用）、改资料写联系方式、被邀请人注册进邀请数。
// valid_at 的 CAS 必须保证恰好一次翻转、邀请人恰好一笔奖励，且对账恒等式成立。
func TestConcurrentValidFlipRewardsExactlyOnce(t *testing.T) {
	ctx := context.Background()
	pool, cleanup, err := testdb.Start(ctx)
	if err != nil {
		t.Fatalf("start testdb: %v", err)
	}
	t.Cleanup(cleanup)

	wallets := wallet.NewService(pool)
	svc := NewService(pool, wallets)

	inviter, err := svc.Register(ctx, "flip_inviter", "secret123", "")
	if err != nil {
		t.Fatalf("register inviter: %v", err)
	}
	invitee, err := svc.Register(ctx, "flip_invitee", "secret123", inviter.InviteCode)
	if err != nil {
		t.Fatalf("register invitee: %v", err)
	}

	// staging：只预置"有作品"；联系方式与成功邀请由并发的真实触发路径补齐
	if _, err := pool.Exec(ctx,
		`UPDATE customers SET video_count = 1 WHERE id = $1`, invitee.ID); err != nil {
		t.Fatalf("stage video_count: %v", err)
	}

	var flips atomic.Int64
	var wg sync.WaitGroup
	// 路径一 ×3：直调判定原语（审核通过钩子的调用形态）
	// 路径二 ×3：改资料写手机号（对本人判定）
	// 路径三 ×2：新客户以 invitee 邀请码注册（对 invitee 判定）
	jobs := make([]func() error, 0, 8)
	for range 3 {
		jobs = append(jobs, func() error {
			var flipped bool
			err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
				var err error
				flipped, err = svc.MarkValidIfQualifiedTx(ctx, tx, invitee.ID)
				return err
			})
			if err == nil && flipped {
				flips.Add(1)
			}
			return err
		})
	}
	for i := range 3 {
		phone := fmt.Sprintf("1380001000%d", i)
		jobs = append(jobs, func() error {
			_, err := svc.UpdateProfile(ctx, invitee.ID, ProfileUpdate{Phone: &phone})
			return err
		})
	}
	for i := range 2 {
		username := fmt.Sprintf("flip_path_reg_%d", i)
		jobs = append(jobs, func() error {
			_, err := svc.Register(ctx, username, "secret123", invitee.InviteCode)
			return err
		})
	}

	errs := make(chan error, len(jobs))
	start := make(chan struct{})
	for _, job := range jobs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if err := job(); err != nil {
				errs <- err
			}
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent trigger: %v", err)
	}

	if got := flips.Load(); got > 1 {
		t.Errorf("direct-primitive flips = %d, want at most 1", got)
	}
	var validAt *int64
	if err := pool.QueryRow(ctx,
		`SELECT EXTRACT(EPOCH FROM valid_at)::bigint FROM customers WHERE id = $1`,
		invitee.ID).Scan(&validAt); err != nil {
		t.Fatalf("query valid_at: %v", err)
	}
	if validAt == nil {
		t.Errorf("invitee not flipped after all trigger paths completed")
	}

	var available int64
	if err := pool.QueryRow(ctx,
		`SELECT available FROM wallets WHERE customer_id = $1`, inviter.ID).Scan(&available); err != nil {
		t.Fatalf("query inviter wallet: %v", err)
	}
	if available != 123 { // 种子迁移金额
		t.Errorf("inviter available = %d, want 123", available)
	}
	var rewardTx int
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM transactions WHERE type = 'invite_valid_reward' AND ref_id = $1`,
		invitee.ID).Scan(&rewardTx); err != nil {
		t.Fatalf("count reward tx: %v", err)
	}
	if rewardTx != 1 {
		t.Errorf("reward tx = %d, want exactly 1", rewardTx)
	}

	ledgerSum, walletTotal, err := wallets.Audit(ctx, inviter.ID)
	if err != nil {
		t.Fatalf("audit: %v", err)
	}
	if ledgerSum != walletTotal {
		t.Errorf("audit invariant broken: ledger sum = %d, wallet total = %d", ledgerSum, walletTotal)
	}
}

// 并发 cap 集成测（PR #27 review P1）：邀请人差 1 笔到 invite_reward_cap 时，
// 两个不同被邀请人并发翻转——cap 检查若不对邀请人行加锁串行化，
// 两个事务各自只见"已提交 + 自己"，会双双放行超发。
func TestInviteRewardCapNotExceededUnderConcurrentFlips(t *testing.T) {
	ctx := context.Background()
	pool, cleanup, err := testdb.Start(ctx)
	if err != nil {
		t.Fatalf("start testdb: %v", err)
	}
	t.Cleanup(cleanup)

	wallets := wallet.NewService(pool)
	svc := NewService(pool, wallets)

	inviter, err := svc.Register(ctx, "cap_inviter", "secret123", "")
	if err != nil {
		t.Fatalf("register inviter: %v", err)
	}
	// staging：4 个已有效的被邀请人（level 0 cap=5，差 1 笔到顶）
	for i := range 4 {
		if _, err := pool.Exec(ctx,
			`INSERT INTO customers (public_id, username, password_hash, invite_code, inviter_id, valid_at)
			 VALUES ($1, $2, 'not-a-real-hash', $3, $4, now())`,
			fmt.Sprintf("capfix%06d", i), fmt.Sprintf("cap_seed_%d", i),
			fmt.Sprintf("CP%06d", i), inviter.ID); err != nil {
			t.Fatalf("stage valid invitee %d: %v", i, err)
		}
	}

	stageContender := func(name, phone string) int64 {
		t.Helper()
		c, err := svc.Register(ctx, name, "secret123", inviter.InviteCode)
		if err != nil {
			t.Fatalf("register %s: %v", name, err)
		}
		if _, err := pool.Exec(ctx,
			`UPDATE customers SET video_count = 1, invite_count = 1, phone = $2 WHERE id = $1`,
			c.ID, phone); err != nil {
			t.Fatalf("stage %s: %v", name, err)
		}
		return c.ID
	}
	b1 := stageContender("cap_contender_1", "13800020001")
	b2 := stageContender("cap_contender_2", "13800020002")

	// 手工交错：tx1 完成 flip+grant 后持锁不提交；tx2 并发翻转另一个被邀请人
	tx1, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin tx1: %v", err)
	}
	defer tx1.Rollback(ctx) //nolint:errcheck // 提交后回滚是 no-op
	flipped1, err := svc.MarkValidIfQualifiedTx(ctx, tx1, b1)
	if err != nil || !flipped1 {
		t.Fatalf("tx1 flip: flipped = %v, err = %v", flipped1, err)
	}

	done := make(chan error, 1)
	go func() {
		done <- pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
			_, err := svc.MarkValidIfQualifiedTx(ctx, tx, b2)
			return err
		})
	}()

	// 让 tx2 有时间跑进 cap 检查（正确实现下它应阻塞在邀请人行锁上）
	time.Sleep(300 * time.Millisecond)
	if err := tx1.Commit(ctx); err != nil {
		t.Fatalf("commit tx1: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("tx2: %v", err)
	}

	// cap=5、已 4 笔有效：两个竞争者只能发出 1 笔
	var rewardTx int
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM transactions WHERE type = 'invite_valid_reward' AND customer_id = $1`,
		inviter.ID).Scan(&rewardTx); err != nil {
		t.Fatalf("count reward tx: %v", err)
	}
	if rewardTx != 1 {
		t.Errorf("reward tx = %d, want 1 (cap must not be exceeded)", rewardTx)
	}
	var available int64
	if err := pool.QueryRow(ctx,
		`SELECT available FROM wallets WHERE customer_id = $1`, inviter.ID).Scan(&available); err != nil {
		t.Fatalf("query wallet: %v", err)
	}
	if available != 123 {
		t.Errorf("inviter available = %d, want 123", available)
	}
}
