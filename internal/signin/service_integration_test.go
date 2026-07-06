package signin

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/netfishx/gabon-go/internal/apierr"
	"github.com/netfishx/gabon-go/internal/testdb"
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
