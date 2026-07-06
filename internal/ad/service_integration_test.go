package ad

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/netfishx/gabon-go/internal/testdb"
)

// 并发抢最后 1 件库存（issue #57 验收）：条件 UPDATE 保证恰好一次成功、库存不为负、
// ad_watches 明细数与扣减一致（不超投）。
func TestConcurrentWatchStockNotOversold(t *testing.T) {
	ctx := context.Background()
	pool, cleanup, err := testdb.Start(ctx)
	if err != nil {
		t.Fatalf("start testdb: %v", err)
	}
	t.Cleanup(cleanup)

	var advID, adID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO advertisers (name, status) VALUES ('race广告商', 'active') RETURNING id`).Scan(&advID); err != nil {
		t.Fatalf("stage advertiser: %v", err)
	}
	// 只剩 1 件库存
	if err := pool.QueryRow(ctx,
		`INSERT INTO ads (advertiser_id, title, media_path, stock_total, stock_remaining, status)
		 VALUES ($1, 'race广告', 'ads/x.mp4', 1, 1, 'active') RETURNING id`, advID).Scan(&adID); err != nil {
		t.Fatalf("stage ad: %v", err)
	}
	// 造 8 个看客
	customerIDs := make([]int64, 8)
	for i := range customerIDs {
		if err := pool.QueryRow(
			ctx,
			`INSERT INTO customers (public_id, username, password_hash, invite_code)
			 VALUES ($1, $2, 'x', $3) RETURNING id`,
			fmt.Sprintf("adfix%06d", i), fmt.Sprintf("ad_watcher_%d", i), fmt.Sprintf("AD%06d", i),
		).Scan(&customerIDs[i]); err != nil {
			t.Fatalf("stage customer %d: %v", i, err)
		}
	}

	svc := NewService(pool)
	var served atomic.Int64
	var wg sync.WaitGroup
	start := make(chan struct{})
	for _, cid := range customerIDs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			s, err := svc.Watch(ctx, cid)
			if err != nil {
				t.Errorf("watch: %v", err)
				return
			}
			if s.Ad != nil {
				served.Add(1)
			}
		}()
	}
	close(start)
	wg.Wait()

	if got := served.Load(); got != 1 {
		t.Errorf("served count = %d, want exactly 1 (only 1 stock)", got)
	}
	var stock, watches int
	pool.QueryRow(ctx, `SELECT stock_remaining FROM ads WHERE id = $1`, adID).Scan(&stock)
	pool.QueryRow(ctx, `SELECT COUNT(*) FROM ad_watches WHERE ad_id = $1`, adID).Scan(&watches)
	if stock != 0 {
		t.Errorf("stock = %d, want 0 (not negative, not oversold)", stock)
	}
	if watches != 1 {
		t.Errorf("ad_watches = %d, want exactly 1 (matches the single decrement)", watches)
	}
}
