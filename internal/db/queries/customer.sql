-- name: CreateCustomer :one
INSERT INTO customers (public_id, username, password_hash, invite_code, inviter_id, ancestors)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetCustomerByInviteCode :one
SELECT * FROM customers WHERE invite_code = $1 AND deleted_at IS NULL;

-- name: IncrementInviteCount :exec
UPDATE customers SET invite_count = invite_count + 1, updated_at = now() WHERE id = $1;
