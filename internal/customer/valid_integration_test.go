package customer

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/netfishx/gabon-go/internal/testdb"
	"github.com/netfishx/gabon-go/internal/wallet"
)

// 并发集成测（issue #24 验收）：多个触发路径同时对同一客户做有效用户判定，
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

	// staging：三条件是判定的输入而非被测行为，直接凑齐
	if _, err := pool.Exec(ctx,
		`UPDATE customers SET video_count = 1, invite_count = 1, phone = '13800009999' WHERE id = $1`,
		invitee.ID); err != nil {
		t.Fatalf("stage qualifications: %v", err)
	}

	const workers = 8
	var flips atomic.Int64
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	start := make(chan struct{})
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			var flipped bool
			err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
				var err error
				flipped, err = svc.MarkValidIfQualifiedTx(ctx, tx, invitee.ID)
				return err
			})
			if err != nil {
				errs <- err
				return
			}
			if flipped { // 仅统计提交成功的翻转
				flips.Add(1)
			}
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent flip: %v", err)
	}

	if got := flips.Load(); got != 1 {
		t.Errorf("flips = %d, want exactly 1", got)
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
