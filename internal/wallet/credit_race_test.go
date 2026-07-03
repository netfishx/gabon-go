package wallet

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/netfishx/gabon-go/internal/db"
)

// P2 回归：消费方在自身事务内用 CreditTx 时，同 (type, ref_id) 竞态
// 必须归一为 ErrAlreadyGranted，而不是裸的唯一约束错误。
// 时序设计：A 先插入未提交 → B 的 exists-check 看不见（READ COMMITTED）→
// B 的 INSERT 阻塞在 A 的索引项上 → A 提交 → B 撞 23505。
// 无论 B 走到哪一步，结果都必须是 ErrAlreadyGranted。
func TestCreditTxDuplicateRefRaceNormalized(t *testing.T) {
	svc, cid := setup(t)
	ctx := context.Background()
	ref := int64(880001)
	params := CreditParams{
		CustomerID: cid, Type: db.TransactionTypeClaimTaskReward, Amount: 60, RefID: &ref,
	}

	txA, err := testPool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin A: %v", err)
	}
	defer func() { _ = txA.Rollback(ctx) }()
	if err := svc.CreditTx(ctx, txA, params); err != nil {
		t.Fatalf("CreditTx A: %v", err)
	}

	bDone := make(chan error, 1)
	go func() {
		txB, err := testPool.Begin(ctx)
		if err != nil {
			bDone <- err
			return
		}
		defer func() { _ = txB.Rollback(ctx) }()
		bDone <- svc.CreditTx(ctx, txB, params)
	}()

	// 确保 B 已越过 exists-check、阻塞在 A 的未提交索引项上，才提交 A——
	// 否则 B 会走存在性检查路径，竞态路径测不到
	deadline := time.Now().Add(10 * time.Second)
	for {
		var waiting int
		if err := testPool.QueryRow(ctx,
			`SELECT count(*) FROM pg_locks WHERE NOT granted`).Scan(&waiting); err != nil {
			t.Fatalf("poll pg_locks: %v", err)
		}
		if waiting > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("B never blocked on the unique index")
		}
		time.Sleep(10 * time.Millisecond)
	}

	if err := txA.Commit(ctx); err != nil {
		t.Fatalf("commit A: %v", err)
	}
	if err := <-bDone; !errors.Is(err, ErrAlreadyGranted) {
		t.Fatalf("CreditTx B err = %v, want ErrAlreadyGranted", err)
	}

	// 仅 A 生效
	if available, _ := balances(t, cid); available != 60 {
		t.Errorf("available = %d, want 60", available)
	}
	if rows := ledgerRows(t, cid); len(rows) != 1 {
		t.Errorf("ledger rows = %d, want 1", len(rows))
	}
}

// issue #5 验收补钉：无关联单据（RefID=nil）的同类型入账不受幂等约束影响，
// 连续多次均须成功、各落一笔。
func TestCreditNilRefRepeatable(t *testing.T) {
	svc, cid := setup(t)
	ctx := context.Background()

	for i := range 3 {
		if err := svc.Credit(ctx, CreditParams{
			CustomerID: cid, Type: db.TransactionTypeRecharge, Amount: 100,
		}); err != nil {
			t.Fatalf("credit #%d: %v", i+1, err)
		}
	}
	if available, _ := balances(t, cid); available != 300 {
		t.Errorf("available = %d, want 300", available)
	}
	if rows := ledgerRows(t, cid); len(rows) != 3 {
		t.Errorf("ledger rows = %d, want 3", len(rows))
	}
}
