package task

import (
	"context"
	"sync"
	"testing"

	"github.com/netfishx/gabon-go/internal/db"
	"github.com/netfishx/gabon-go/internal/testdb"
	"github.com/netfishx/gabon-go/internal/wallet"
)

// 并发集成测（issue #46 验收）：同客户同任务 N 路并发推进——
// 进度必须恰好 N（UPSERT 行锁串行化）、达标发奖恰好一笔、钱包对账恒等式成立。
func TestConcurrentAdvanceCountsExactly(t *testing.T) {
	ctx := context.Background()
	pool, cleanup, err := testdb.Start(ctx)
	if err != nil {
		t.Fatalf("start testdb: %v", err)
	}
	t.Cleanup(cleanup)

	// 直插客户与钱包 fixture（同 wallet 集成测先例，避免域间测试依赖）
	var customerID int64
	if err := pool.QueryRow(
		ctx,
		`INSERT INTO customers (public_id, username, password_hash, invite_code)
		 VALUES ('taskfix00001', 'task_racer', 'not-a-real-hash', 'TK000001') RETURNING id`,
	).Scan(&customerID); err != nil {
		t.Fatalf("stage customer: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO wallets (customer_id) VALUES ($1)`, customerID); err != nil {
		t.Fatalf("stage wallet: %v", err)
	}

	wallets := wallet.NewService(pool)
	svc := NewService(pool, wallets)

	// 种子"每日点赞"任务 target=20：并发推进恰好 20 次
	const workers = 20
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	start := make(chan struct{})
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if err := svc.Advance(ctx, customerID, db.TaskCategoryLike, 0); err != nil {
				errs <- err
			}
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent advance: %v", err)
	}

	var progress int32
	var granted int
	if err := pool.QueryRow(ctx,
		`SELECT progress, (reward_granted_at IS NOT NULL)::int FROM periodic_task_progress
		 WHERE customer_id = $1`, customerID).Scan(&progress, &granted); err != nil {
		t.Fatalf("query progress: %v", err)
	}
	if progress != workers {
		t.Errorf("progress = %d, want %d (no lost updates)", progress, workers)
	}
	if granted != 1 {
		t.Errorf("granted flag = %d, want 1", granted)
	}

	var rewardTx int
	var available int64
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM transactions WHERE customer_id = $1 AND type = 'periodic_task_reward'`,
		customerID).Scan(&rewardTx); err != nil {
		t.Fatalf("count reward tx: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT available FROM wallets WHERE customer_id = $1`, customerID).Scan(&available); err != nil {
		t.Fatalf("query wallet: %v", err)
	}
	if rewardTx != 1 || available != 58 {
		t.Errorf("reward tx = %d available = %d, want exactly 1 and 58", rewardTx, available)
	}

	ledgerSum, walletTotal, err := wallets.Audit(ctx, customerID)
	if err != nil {
		t.Fatalf("audit: %v", err)
	}
	if ledgerSum != walletTotal {
		t.Errorf("audit invariant broken: ledger = %d, wallet = %d", ledgerSum, walletTotal)
	}
}

// watch 防刷恰好一次（PR #50 review P1 修复验证）：同客户同视频同周期
// 无论顺序或并发，推进恰好一次——唯一标记与增量同事务仲裁。
func TestWatchDedupExactlyOnce(t *testing.T) {
	ctx := context.Background()
	pool, cleanup, err := testdb.Start(ctx)
	if err != nil {
		t.Fatalf("start testdb: %v", err)
	}
	t.Cleanup(cleanup)

	var customerID int64
	if err := pool.QueryRow(
		ctx,
		`INSERT INTO customers (public_id, username, password_hash, invite_code)
		 VALUES ('taskfix00002', 'watch_racer', 'not-a-real-hash', 'TK000002') RETURNING id`,
	).Scan(&customerID); err != nil {
		t.Fatalf("stage customer: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO wallets (customer_id) VALUES ($1)`, customerID); err != nil {
		t.Fatalf("stage wallet: %v", err)
	}
	var videoID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO videos (public_id, customer_id, title, storage_path, status)
		 VALUES ('watchrace001', $1, '样本', 'videos/x/a.mp4', 'published') RETURNING id`,
		customerID).Scan(&videoID); err != nil {
		t.Fatalf("stage video: %v", err)
	}

	svc := NewService(pool, wallet.NewService(pool))

	// 串行两次（模拟同视频两条有效播放先后上报）
	for range 2 {
		if err := svc.Advance(ctx, customerID, db.TaskCategoryWatchVideo, videoID); err != nil {
			t.Fatalf("advance: %v", err)
		}
	}
	// 并发再打一轮
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = svc.Advance(ctx, customerID, db.TaskCategoryWatchVideo, videoID)
		}()
	}
	wg.Wait()

	var progress int32
	if err := pool.QueryRow(
		ctx,
		`SELECT progress FROM periodic_task_progress WHERE customer_id = $1`, customerID,
	).Scan(&progress); err != nil {
		t.Fatalf("query progress: %v", err)
	}
	if progress != 1 {
		t.Errorf("progress = %d, want exactly 1 (same video, same period)", progress)
	}
}
