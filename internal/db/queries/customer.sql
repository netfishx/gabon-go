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

-- name: LockInviterForGrant :one
-- 发奖前锁邀请人行，串行化同一邀请人的并发 cap 检查——
-- 后到事务重数有效邀请数时必能看见先提交的翻转，杜绝并发超发。
-- FOR NO KEY UPDATE：互相排斥即可，不与注册插入 FK 引用取的 KEY SHARE 冲突。
SELECT vip_level FROM customers
WHERE id = $1 AND deleted_at IS NULL
FOR NO KEY UPDATE;

-- name: ListTeamMembers :many
-- 团队下钻单位：某成员的直接下级，附带各自的直接下级数（id 升序游标分页）。
-- count_subordinates=false 时计数归 0：深度 3 成员的下级在团队之外，不泄漏界外结构。
SELECT c.id, c.public_id, c.username, c.name, c.avatar_path,
       (c.valid_at IS NOT NULL)::bool AS valid,
       (CASE WHEN sqlc.arg('count_subordinates')::bool THEN
            (SELECT COUNT(*) FROM customers s
             WHERE s.inviter_id = c.id AND s.deleted_at IS NULL)
        ELSE 0 END)::bigint AS subordinate_count
FROM customers c
WHERE c.inviter_id = sqlc.arg('parent_id')
  AND c.deleted_at IS NULL
  AND c.id > sqlc.arg('cursor')
ORDER BY c.id
LIMIT sqlc.arg('row_limit');

-- name: TeamSummaryByDepth :many
-- 团队（3 级以内）按深度聚合人数与有效人数：物化祖先路径 && 走 GIN，
-- 深度 = 路径长度 - viewer 在路径中的位置 + 1。
SELECT (cardinality(c.ancestors) - array_position(c.ancestors, @viewer_id::bigint) + 1)::int AS depth,
       COUNT(*)::bigint AS member_count,
       (COUNT(*) FILTER (WHERE c.valid_at IS NOT NULL))::bigint AS valid_count
FROM customers c
WHERE c.ancestors && ARRAY[@viewer_id::bigint]
  AND c.deleted_at IS NULL
  AND cardinality(c.ancestors) - array_position(c.ancestors, @viewer_id::bigint) + 1 <= 3
GROUP BY 1
ORDER BY 1;

-- name: SumInviteRewards :one
-- 查看者累计邀请奖励（流水现算；SUM 空集为 NULL，COALESCE 归 0 是 SQL 语义而非业务兜底）。
SELECT COALESCE(SUM(amount), 0)::bigint FROM transactions
WHERE customer_id = $1 AND type = 'invite_valid_reward';

-- name: UpdateCustomerProfile :one
UPDATE customers
SET name        = COALESCE(sqlc.narg('name'), name),
    signature   = COALESCE(sqlc.narg('signature'), signature),
    email       = COALESCE(sqlc.narg('email'), email),
    phone       = COALESCE(sqlc.narg('phone'), phone),
    avatar_path = COALESCE(sqlc.narg('avatar_path'), avatar_path),
    updated_at  = now()
WHERE id = sqlc.arg('id')
RETURNING *;
