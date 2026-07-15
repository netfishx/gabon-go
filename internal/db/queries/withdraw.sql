-- name: InsertWithdrawalOrder :one
-- 建单第一步：先插入临时 order_no（uuid，避并发撞唯一索引），返回 IDENTITY id。
-- 第二步 FinalizeWithdrawalOrderNo 在同事务内把 order_no 派生为确定性的 'W'||id。
INSERT INTO withdrawal_orders (
    order_no, customer_id, amount, fiat_amount, bank_card_id,
    payee_account, payee_name, payee_bank, payee_bank_code, payee_province, payee_city
)
VALUES (
    gen_random_uuid()::text,
    sqlc.arg(customer_id),
    sqlc.arg(amount),
    sqlc.arg(fiat_amount),
    sqlc.arg(bank_card_id),
    sqlc.arg(payee_account),
    sqlc.arg(payee_name),
    sqlc.narg(payee_bank),
    sqlc.narg(payee_bank_code),
    sqlc.narg(payee_province),
    sqlc.narg(payee_city)
)
RETURNING id;

-- name: FinalizeWithdrawalOrderNo :one
-- 建单第二步：确定性派生 order_no = 'W'||id（与 InsertWithdrawalOrder 同事务）。
UPDATE withdrawal_orders
SET order_no = 'W' || id, updated_at = now()
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: ListWithdrawalOrders :many
SELECT * FROM withdrawal_orders
WHERE customer_id = sqlc.arg(customer_id)
  AND (sqlc.arg(cursor)::bigint = 0 OR id < sqlc.arg(cursor))
ORDER BY id DESC
LIMIT sqlc.arg(row_limit);

-- name: LockBankCardForWithdrawal :one
-- 建单事务内对卡行加锁并复核未删：与删卡 UPDATE 的行锁互斥，
-- 保证「守卫看到在途单」与「建单看到未删卡」二者必居其一。
SELECT id FROM bank_cards
WHERE id = sqlc.arg(id) AND customer_id = sqlc.arg(customer_id) AND deleted_at IS NULL
FOR UPDATE;
