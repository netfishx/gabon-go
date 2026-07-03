package wallet

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/netfishx/gabon-go/internal/db"
)

// ErrAlreadyGranted 表示带关联单据的入账此前已发放过——对发奖调用方这是幂等成功信号，非故障。
// 注意：若在调用方事务内撞唯一约束触发本错误，该事务已被 Postgres 置为 aborted，调用方必须整体回滚；
// 业务侧的第一道幂等闸（如进度行条件 UPDATE）应让这种撞击停留在"最后防线"级别的罕见事件。
var ErrAlreadyGranted = errors.New("wallet: already granted for this ref")

// CreditParams 入账参数。RefID 非空时受 (type, ref_id) 唯一约束保护，天然幂等。
type CreditParams struct {
	CustomerID int64
	Type       db.TransactionType
	Amount     int64 // 必须 > 0
	RefID      *int64
}

// Credit 在自管事务内入账。
func (s *Service) Credit(ctx context.Context, p CreditParams) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		return s.CreditTx(ctx, tx, p)
	})
}

// CreditTx 在调用方事务内入账：原子增加可用余额，同事务落一条正金额流水。
// 消费方（发奖/充值到账）应把业务状态翻转与本调用包进同一事务。
func (s *Service) CreditTx(ctx context.Context, tx pgx.Tx, p CreditParams) error {
	if p.Amount <= 0 {
		return fmt.Errorf("wallet: credit amount must be positive, got %d", p.Amount)
	}
	q := s.q.WithTx(tx)

	if p.RefID != nil {
		exists, err := q.TransactionRefExists(ctx, db.TransactionRefExistsParams{
			Type: p.Type, RefID: p.RefID,
		})
		if err != nil {
			return fmt.Errorf("check ref exists: %w", err)
		}
		if exists {
			return ErrAlreadyGranted
		}
	}

	w, err := q.CreditWallet(ctx, db.CreditWalletParams{
		CustomerID: p.CustomerID, Amount: p.Amount,
	})
	if err != nil {
		return fmt.Errorf("credit wallet: %w", err)
	}
	// 竞态路径：双方都越过 exists-check 时，落笔撞唯一约束——就地归一为已发放语义，
	// 消费方在自身事务内也能以 errors.Is(ErrAlreadyGranted) 识别（事务此时已 aborted，须整体回滚）
	if err := writeLedger(ctx, q, p.CustomerID, p.Type, p.Amount, w, p.RefID); err != nil {
		if db.UniqueViolationConstraint(err) == "transactions_type_ref_key" {
			return ErrAlreadyGranted
		}
		return err
	}
	return nil
}
