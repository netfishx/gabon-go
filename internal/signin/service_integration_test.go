package signin

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/netfishx/gabon-go/internal/apierr"
	"github.com/netfishx/gabon-go/internal/testdb"
	"github.com/netfishx/gabon-go/internal/tz"
	"github.com/netfishx/gabon-go/internal/wallet"
)

// 并发同日签到：唯一约束 (customer, sign_date) 保证恰好一次成功、恰好一笔日签奖励。
func TestConcurrentSignInExactlyOnce(t *testing.T) {
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
		 VALUES ('signfix00001', 'sign_racer', 'not-a-real-hash', 'SI000001') RETURNING id`,
	).Scan(&customerID); err != nil {
		t.Fatalf("stage customer: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO wallets (customer_id) VALUES ($1)`, customerID); err != nil {
		t.Fatalf("stage wallet: %v", err)
	}
	// VIP 0 档种子（倍率 10000bp）与日签配置由迁移提供
	if _, err := pool.Exec(ctx,
		`INSERT INTO vip_level_configs (level, name, price, reward_multiplier_bp, invite_reward_cap)
		 VALUES (0, '普通', 0, 10000, 5) ON CONFLICT DO NOTHING`); err != nil {
		t.Fatalf("seed vip: %v", err)
	}

	wallets := wallet.NewService(pool)
	svc := NewService(pool, wallets)

	const workers = 8
	var success atomic.Int64
	var wg sync.WaitGroup
	start := make(chan struct{})
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			err := svc.SignIn(ctx, customerID)
			if err == nil {
				success.Add(1)
				return
			}
			var apiErr *apierr.Error
			if !errors.As(err, &apiErr) || apiErr.Code != apierr.CodeSignInAlreadyToday {
				t.Errorf("unexpected sign-in error: %v", err)
			}
		}()
	}
	close(start)
	wg.Wait()

	if got := success.Load(); got != 1 {
		t.Errorf("successful sign-ins = %d, want exactly 1", got)
	}
	var signRows, txRows int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM sign_ins WHERE customer_id = $1`, customerID).Scan(&signRows); err != nil {
		t.Fatalf("count sign-ins: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM transactions WHERE customer_id = $1 AND type = 'sign_in_reward'`,
		customerID).Scan(&txRows); err != nil {
		t.Fatalf("count reward tx: %v", err)
	}
	if signRows != 1 || txRows != 1 {
		t.Errorf("sign_ins = %d, reward tx = %d, want exactly 1 each", signRows, txRows)
	}

	ledgerSum, walletTotal, err := wallets.Audit(ctx, customerID)
	if err != nil {
		t.Fatalf("audit: %v", err)
	}
	if ledgerSum != walletTotal {
		t.Errorf("audit invariant broken: ledger = %d, wallet = %d", ledgerSum, walletTotal)
	}
}

// 相邻日跨午夜并发（PR #58 review P2）：客户行锁串行化同一客户签到，
// 两个不同 sign_date 的合法签到并发时都成功、月累计不丢、里程碑不撞约束。
// 用内部 signInOn 直插两个相邻日模拟跨午夜（服务缝直测锁的串行化效果）。
func TestAdjacentDaySignInSerialized(t *testing.T) {
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
		 VALUES ('signfix00002', 'sign_midnight', 'not-a-real-hash', 'SI000002') RETURNING id`,
	).Scan(&customerID); err != nil {
		t.Fatalf("stage customer: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO wallets (customer_id) VALUES ($1)`, customerID); err != nil {
		t.Fatalf("stage wallet: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO vip_level_configs (level, name, price, reward_multiplier_bp, invite_reward_cap)
		 VALUES (0, '普通', 0, 10000, 5) ON CONFLICT DO NOTHING`); err != nil {
		t.Fatalf("seed vip: %v", err)
	}
	// 本月已签 6 天（种子），使下一签为第 7 天——配一个 threshold=7 里程碑
	if _, err := pool.Exec(ctx,
		`INSERT INTO activity_reward_configs (kind, threshold, reward, enabled) VALUES ('milestone', 7, 50, true)`); err != nil {
		t.Fatalf("seed milestone: %v", err)
	}

	// 直接验证锁的串行化：两个 goroutine 各签一个相邻日（月内第 7、第 8 天），
	// 锁保证 count 视图一致——两次都成功、无里程碑撞约束回滚。
	// 预置月内前 6 天
	for d := 1; d <= 6; d++ {
		if _, err := pool.Exec(ctx,
			`INSERT INTO sign_ins (customer_id, sign_date, reward_amount)
			 VALUES ($1, date_trunc('month', now() AT TIME ZONE 'Asia/Shanghai')::date + ($2 - 1), 1)`,
			customerID, d); err != nil {
			t.Fatalf("preseed day %d: %v", d, err)
		}
	}

	svc := NewService(pool, wallet.NewService(pool))
	// 本月第 7、8 天两个相邻签到日（Asia/Shanghai 月初 + 偏移）
	ms := time.Now().In(tz.Shanghai)
	monthStart := time.Date(ms.Year(), ms.Month(), 1, 0, 0, 0, 0, tz.Shanghai)
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	start := make(chan struct{})
	for _, offset := range []int{6, 7} { // 第 7、8 天
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if err := svc.signInAt(ctx, customerID, monthStart.AddDate(0, 0, offset)); err != nil {
				errs <- err
			}
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("adjacent-day sign-in: %v", err)
	}

	// 两天都签上、月累计=8、里程碑第 7 天恰好一笔
	var signRows, milestoneRows int
	pool.QueryRow(ctx, `SELECT COUNT(*) FROM sign_ins WHERE customer_id = $1
		AND date_trunc('month', sign_date) = date_trunc('month', now() AT TIME ZONE 'Asia/Shanghai')`, customerID).Scan(&signRows)
	pool.QueryRow(ctx, `SELECT COUNT(*) FROM milestone_awards WHERE customer_id = $1 AND threshold = 7`, customerID).Scan(&milestoneRows)
	if signRows != 8 {
		t.Errorf("month sign-ins = %d, want 8", signRows)
	}
	if milestoneRows != 1 {
		t.Errorf("milestone(7) awards = %d, want exactly 1", milestoneRows)
	}
}
