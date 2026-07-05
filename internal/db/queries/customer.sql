-- name: CreateCustomer :one
INSERT INTO customers (public_id, username, password_hash, invite_code, inviter_id, ancestors)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetCustomerByInviteCode :one
SELECT * FROM customers WHERE invite_code = $1 AND deleted_at IS NULL;

-- name: GetCustomerByUsername :one
SELECT * FROM customers WHERE username = $1 AND deleted_at IS NULL;

-- name: GetCustomerByID :one
SELECT * FROM customers WHERE id = $1 AND deleted_at IS NULL;

-- name: GetCustomerByPublicID :one
SELECT * FROM customers WHERE public_id = $1 AND deleted_at IS NULL;

-- name: SetCustomerLastLogin :exec
UPDATE customers SET last_login_at = now(), updated_at = now() WHERE id = $1;

-- name: UpdateCustomerPassword :exec
UPDATE customers
SET password_hash = $2, password_changed_at = now(), updated_at = now()
WHERE id = $1;

-- name: IncrementInviteCount :exec
UPDATE customers SET invite_count = invite_count + 1, updated_at = now() WHERE id = $1;

-- name: MarkCustomerValidIfQualified :one
-- 有效用户判定（CAS）：三条件全下沉 SQL 原子完成，valid_at IS NULL 保证只翻转一次、永不回退。
-- 返回 inviter_id 供翻转事务内给邀请人发奖；未翻转返回 ErrNoRows。
UPDATE customers
SET valid_at = now(), updated_at = now()
WHERE id = $1 AND deleted_at IS NULL
  AND valid_at IS NULL
  AND video_count > 0
  AND invite_count > 0
  AND (phone IS NOT NULL OR email IS NOT NULL)
RETURNING inviter_id;

-- name: CountValidInvitees :one
SELECT COUNT(*) FROM customers
WHERE inviter_id = $1 AND valid_at IS NOT NULL AND deleted_at IS NULL;

-- name: ListTeamMembers :many
-- 团队下钻单位：某成员的直接下级，附带各自的直接下级数（id 升序游标分页）。
SELECT c.id, c.public_id, c.username, c.name, c.avatar_path,
       (c.valid_at IS NOT NULL)::bool AS valid,
       (SELECT COUNT(*) FROM customers s
        WHERE s.inviter_id = c.id AND s.deleted_at IS NULL) AS subordinate_count
FROM customers c
WHERE c.inviter_id = sqlc.arg('parent_id')
  AND c.deleted_at IS NULL
  AND c.id > sqlc.arg('cursor')
ORDER BY c.id
LIMIT sqlc.arg('row_limit');

-- name: UpdateCustomerProfile :one
UPDATE customers
SET name       = COALESCE(sqlc.narg('name'), name),
    signature  = COALESCE(sqlc.narg('signature'), signature),
    email      = COALESCE(sqlc.narg('email'), email),
    phone      = COALESCE(sqlc.narg('phone'), phone),
    updated_at = now()
WHERE id = sqlc.arg('id')
RETURNING *;
