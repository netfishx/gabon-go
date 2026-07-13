-- name: InsertRechargeOrder :one
-- 建单第一步：先插入临时 order_no（uuid，避并发撞唯一索引），返回 IDENTITY id。
-- 第二步 FinalizeRechargeOrderNo 在同事务内把 order_no 派生为确定性的 'R'||id。
INSERT INTO recharge_orders (order_no, customer_id, amount, fiat_amount, payment_method, provider, expires_at)
VALUES (
    gen_random_uuid()::text,
    sqlc.arg(customer_id),
    sqlc.arg(amount),
    sqlc.arg(fiat_amount),
    sqlc.narg(payment_method),
    sqlc.arg(provider),
    sqlc.arg(expires_at)
)
RETURNING id;

-- name: FinalizeRechargeOrderNo :one
-- 建单第二步：确定性派生 order_no = 'R'||id（与 InsertRechargeOrder 同事务）。
UPDATE recharge_orders
SET order_no = 'R' || id, updated_at = now()
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: GetRechargeOrderByOrderNo :one
SELECT * FROM recharge_orders WHERE order_no = sqlc.arg(order_no);

-- name: GetRechargeOrderByProviderRef :one
-- 回调兜底定位：order_no 缺失时按 (provider, provider_order_no) 命中。
SELECT * FROM recharge_orders
WHERE provider = sqlc.arg(provider) AND provider_order_no = sqlc.arg(provider_order_no);

-- name: SetRechargeProviderInfo :exec
-- 建单调 Pay 后回填渠道单号/状态。列级精确处理快速异步回调抢先落终态的竞态（P3）：
--   provider_order_no：COALESCE 补写——仅在缺失时填、绝不覆盖已有值（渠道单号一次分配、不变）；
--   provider_status：仅 pending_payment 时写，终态则保留回调已落的状态、不被过期 pending 盖回。
UPDATE recharge_orders
SET provider_order_no = COALESCE(provider_order_no, sqlc.narg(provider_order_no)),
    provider_status = CASE
        WHEN status = 'pending_payment' THEN sqlc.narg(provider_status)
        ELSE provider_status
    END,
    updated_at = now()
WHERE id = sqlc.arg(id);

-- name: MarkRechargeSucceeded :one
-- 到账 CAS（幂等第二闸）：仅 pending_payment → succeeded；0 行 = 已终态，调用方短路。
UPDATE recharge_orders
SET status = 'succeeded',
    provider_status = sqlc.narg(provider_status),
    completed_at = now(),
    updated_at = now()
WHERE id = sqlc.arg(id) AND status = 'pending_payment'
RETURNING *;

-- name: ListExpiredPendingRecharges :many
SELECT * FROM recharge_orders
WHERE status = 'pending_payment' AND expires_at < now()
ORDER BY id
LIMIT sqlc.arg(row_limit);

-- name: MarkRechargeCancelled :one
UPDATE recharge_orders
SET status = 'cancelled',
    provider_status = sqlc.narg(provider_status),
    completed_at = now(),
    updated_at = now()
WHERE id = sqlc.arg(id) AND status = 'pending_payment'
RETURNING *;

-- name: MarkRechargeFailed :one
UPDATE recharge_orders
SET status = 'failed',
    provider_status = sqlc.narg(provider_status),
    completed_at = now(),
    updated_at = now()
WHERE id = sqlc.arg(id) AND status = 'pending_payment'
RETURNING *;

-- name: ListRechargeOrders :many
SELECT * FROM recharge_orders
WHERE customer_id = sqlc.arg(customer_id)
  AND (sqlc.arg(cursor)::bigint = 0 OR id < sqlc.arg(cursor))
ORDER BY id DESC
LIMIT sqlc.arg(row_limit);

-- name: InsertPaymentEvent :exec
-- append-only 审计：每次 请求/响应/回调/查单 原始报文落库（资金纠纷佐证）。
INSERT INTO payment_events (order_no, provider, direction, payload)
VALUES (sqlc.arg(order_no), sqlc.arg(provider), sqlc.arg(direction), sqlc.arg(payload));
