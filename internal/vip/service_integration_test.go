package vip

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/netfishx/gabon-go/internal/db"
	"github.com/netfishx/gabon-go/internal/testdb"
	"github.com/netfishx/gabon-go/internal/wallet"
)

// 并发购买同档（issue #56 验收）：只升级 CAS 保证恰好一次成功、恰好一笔扣钻，
// 钱包对账恒等式成立（不会因并发重复扣两次全价）。
func TestConcurrentPurchaseExactlyOnce(t *testing.T) {
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
		 VALUES ('vipfix000001', 'vip_racer', 'not-a-real-hash', 'VP000001') RETURNING id`,
	).Scan(&customerID); err != nil {
		t.Fatalf("stage customer: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO wallets (customer_id) VALUES ($1)`, customerID); err != nil {
		t.Fatalf("stage wallet: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO vip_level_configs (level, name, price, reward_multiplier_bp, invite_reward_cap, upload_video_limit)
		 VALUES (0,'普通',0,10000,5,6),(2,'银牌',99900,14000,50,50) ON CONFLICT DO NOTHING`); err != nil {
		t.Fatalf("seed vip: %v", err)
	}

	wallets := wallet.NewService(pool)
	svc := NewService(pool, wallets)

	// 经账本充值播种初始余额（只够买一次银牌），保持流水与钱包一致以便对账断言
	if err := wallets.Credit(ctx, wallet.CreditParams{
		CustomerID: customerID, Type: db.TransactionTypeRecharge, Amount: 99900,
	}); err != nil {
		t.Fatalf("seed balance: %v", err)
	}

	const workers = 8
	var success atomic.Int64
	var wg sync.WaitGroup
	start := make(chan struct{})
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if err := svc.Purchase(ctx, customerID, 2); err == nil {
				success.Add(1)
			}
		}()
	}
	close(start)
	wg.Wait()

	if got := success.Load(); got != 1 {
		t.Errorf("successful purchases = %d, want exactly 1", got)
	}
	var level int
	var available int64
	var purchases int
	pool.QueryRow(ctx, `SELECT vip_level FROM customers WHERE id = $1`, customerID).Scan(&level)
	pool.QueryRow(ctx, `SELECT available FROM wallets WHERE customer_id = $1`, customerID).Scan(&available)
	pool.QueryRow(ctx, `SELECT COUNT(*) FROM vip_purchases WHERE customer_id = $1`, customerID).Scan(&purchases)
	if level != 2 {
		t.Errorf("vip_level = %d, want 2", level)
	}
	if available != 0 {
		t.Errorf("available = %d, want 0 (exactly one full-price debit)", available)
	}
	if purchases != 1 {
		t.Errorf("vip_purchases = %d, want exactly 1", purchases)
	}

	ledgerSum, walletTotal, err := wallets.Audit(ctx, customerID)
	if err != nil {
		t.Fatalf("audit: %v", err)
	}
	if ledgerSum != walletTotal {
		t.Errorf("audit invariant broken: ledger = %d, wallet = %d", ledgerSum, walletTotal)
	}
}
