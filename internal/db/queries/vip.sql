-- name: ListVipLevelConfigs :many
SELECT * FROM vip_level_configs ORDER BY level;

-- name: GetVipLevelConfig :one
SELECT * FROM vip_level_configs WHERE level = $1;

-- name: UpgradeVipLevel :one
-- 只升级 CAS：锁客户行并取旧档；仅当目标 > 当前才更新，0 行 = 降级/平级/不存在。
-- 返回旧档供 vip_purchases 记录 from_level。
WITH prev AS (
    SELECT vip_level FROM customers WHERE id = sqlc.arg('id') FOR NO KEY UPDATE
)
UPDATE customers SET vip_level = sqlc.arg('to_level'), updated_at = now()
FROM prev
WHERE customers.id = sqlc.arg('id') AND prev.vip_level < sqlc.arg('to_level')
RETURNING prev.vip_level AS from_level;

-- name: InsertVipPurchase :one
INSERT INTO vip_purchases (customer_id, from_level, to_level, price)
VALUES ($1, sqlc.arg('from_level'), sqlc.arg('to_level'), sqlc.arg('price'))
RETURNING *;

-- name: GetUploadLimitForCustomer :one
-- 客户当前 VIP 档的发布作品上限与现存已发布作品数（confirm 拦截用）。
SELECT v.upload_video_limit, c.video_count FROM customers c
JOIN vip_level_configs v ON v.level = c.vip_level
WHERE c.id = $1;
