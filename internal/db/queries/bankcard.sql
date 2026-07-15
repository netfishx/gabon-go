-- name: InsertBankCard :one
INSERT INTO bank_cards (customer_id, card_no, holder_name, bank_name, bank_code, province, city)
VALUES (sqlc.arg(customer_id), sqlc.arg(card_no), sqlc.arg(holder_name), sqlc.arg(bank_name),
        sqlc.narg(bank_code), sqlc.narg(province), sqlc.narg(city))
RETURNING *;

-- name: ListBankCards :many
SELECT * FROM bank_cards
WHERE customer_id = sqlc.arg(customer_id) AND deleted_at IS NULL
ORDER BY id DESC;

-- name: GetOwnedBankCard :one
SELECT * FROM bank_cards
WHERE id = sqlc.arg(id) AND customer_id = sqlc.arg(customer_id) AND deleted_at IS NULL;

-- name: SoftDeleteBankCard :execrows
UPDATE bank_cards SET deleted_at = now(), updated_at = now()
WHERE bank_cards.id = sqlc.arg(id)
  AND bank_cards.customer_id = sqlc.arg(customer_id)
  AND bank_cards.deleted_at IS NULL
  AND NOT EXISTS (
      SELECT 1 FROM withdrawal_orders w
      WHERE w.bank_card_id = bank_cards.id AND w.status IN ('pending_review', 'paying')
  );
