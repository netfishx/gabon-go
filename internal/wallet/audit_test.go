package wallet

import (
	"context"
	"math/rand/v2"
	"testing"

	"github.com/netfishx/gabon-go/internal/db"
)

// 对账恒等式 property 测试（issue #8）：任意合法/被拒操作交错后，
// SUM(流水金额) = available + frozen 恒成立。固定种子保证可复现。
func TestLedgerInvariantUnderRandomOps(t *testing.T) {
	svc, cid := setup(t)
	ctx := context.Background()
	rng := rand.New(rand.NewPCG(20260703, 42))

	assertInvariant := func(step int) {
		t.Helper()
		ledgerSum, walletTotal, err := svc.Audit(ctx, cid)
		if err != nil {
			t.Fatalf("step %d: audit: %v", step, err)
		}
		if ledgerSum != walletTotal {
			t.Fatalf("step %d: ledger sum %d != wallet total %d", step, ledgerSum, walletTotal)
		}
	}

	var applied [5]int
	refSeq := int64(500000)
	const steps = 200
	for i := range steps {
		amount := rng.Int64N(300) + 1
		switch rng.IntN(5) {
		case 0: // Credit（带 ref，偶尔复用制造幂等拒绝）
			ref := refSeq
			if rng.IntN(4) == 0 && refSeq > 500000 {
				ref = 500000 + rng.Int64N(refSeq-500000) // 复用旧 ref
			} else {
				refSeq++
			}
			err := svc.Credit(ctx, CreditParams{
				CustomerID: cid, Type: db.TransactionTypeWatchReward, Amount: amount, RefID: &ref,
			})
			if err == nil {
				applied[0]++
			}
		case 1: // Debit（可能余额不足被拒）
			if err := svc.Debit(ctx, DebitParams{
				CustomerID: cid, Type: db.TransactionTypeVipPurchase, Amount: amount,
			}); err == nil {
				applied[1]++
			}
		case 2: // Freeze
			if err := svc.Freeze(ctx, cid, amount); err == nil {
				applied[2]++
			}
		case 3: // Unfreeze
			if err := svc.Unfreeze(ctx, cid, amount); err == nil {
				applied[3]++
			}
		case 4: // SettleFrozen
			if err := svc.SettleFrozen(ctx, SettleParams{
				CustomerID: cid, Type: db.TransactionTypeWithdrawal, Amount: amount,
			}); err == nil {
				applied[4]++
			}
		}
		if i%20 == 0 {
			assertInvariant(i)
		}
	}
	assertInvariant(steps)

	// 每个原语都必须真实发生过，否则性质测试形同虚设
	for i, n := range applied {
		if n == 0 {
			t.Errorf("primitive %d never applied; adjust generator (applied = %v)", i, applied)
		}
	}
	t.Logf("applied credit/debit/freeze/unfreeze/settle = %v", applied)
}
