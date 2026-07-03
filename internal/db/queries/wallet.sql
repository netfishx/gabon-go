-- name: CreateWallet :exec
INSERT INTO wallets (customer_id) VALUES ($1);

-- name: GetWallet :one
SELECT * FROM wallets WHERE customer_id = $1;

-- name: CreditWallet :one
UPDATE wallets
SET available = available + $2, updated_at = now()
WHERE customer_id = $1
RETURNING *;

-- name: InsertTransaction :one
INSERT INTO transactions (customer_id, type, amount, balance_after, ref_id)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: TransactionRefExists :one
SELECT EXISTS (
    SELECT 1 FROM transactions WHERE type = $1 AND ref_id = $2
);

-- name: DebitWallet :one
UPDATE wallets
SET available = available - $2, updated_at = now()
WHERE customer_id = $1 AND available >= $2
RETURNING *;

-- name: FreezeWallet :execrows
UPDATE wallets
SET available = available - $2, frozen = frozen + $2, updated_at = now()
WHERE customer_id = $1 AND available >= $2;

-- name: UnfreezeWallet :execrows
UPDATE wallets
SET available = available + $2, frozen = frozen - $2, updated_at = now()
WHERE customer_id = $1 AND frozen >= $2;

-- name: SettleFrozenWallet :one
UPDATE wallets
SET frozen = frozen - $2, updated_at = now()
WHERE customer_id = $1 AND frozen >= $2
RETURNING *;

-- name: AuditCustomerLedger :one
-- 对账恒等式（ADR-0006）：客户全部流水之和必须等于钱包总额（可用+冻结）。
SELECT
    COALESCE((SELECT SUM(t.amount) FROM transactions t WHERE t.customer_id = w.customer_id), 0)::bigint AS ledger_sum,
    (w.available + w.frozen)::bigint AS wallet_total
FROM wallets w
WHERE w.customer_id = $1;

-- name: ListTransactions :many
SELECT * FROM transactions
WHERE customer_id = sqlc.arg(customer_id)
  AND (sqlc.arg(cursor)::bigint = 0 OR id < sqlc.arg(cursor))
ORDER BY id DESC
LIMIT sqlc.arg(row_limit);
