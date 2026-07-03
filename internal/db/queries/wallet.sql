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
