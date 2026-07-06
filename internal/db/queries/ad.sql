-- name: PickServingAd :one
-- 随机取一条在投广告：四条件（广告上架 ∧ 广告商上架 ∧ 库存>0 ∧ 未过期）+ 均匀随机。
SELECT a.id, a.title, a.media_path, a.link FROM ads a
JOIN advertisers adv ON adv.id = a.advertiser_id
WHERE a.status = 'active' AND a.deleted_at IS NULL AND a.stock_remaining > 0
  AND (a.expires_at IS NULL OR a.expires_at > now())
  AND adv.status = 'active' AND adv.deleted_at IS NULL
ORDER BY random()
LIMIT 1;

-- name: DecrementAdStock :execrows
-- 原子扣库存（条件 UPDATE 杜绝并发超投）；0 行 = 恰好被并发抢空，调用方重取/返空。
UPDATE ads SET stock_remaining = stock_remaining - 1, updated_at = now()
WHERE id = $1 AND stock_remaining > 0;

-- name: InsertAdWatch :exec
INSERT INTO ad_watches (customer_id, ad_id) VALUES ($1, $2);

-- name: CreateAdvertiser :one
INSERT INTO advertisers (name, contact) VALUES ($1, sqlc.narg('contact')) RETURNING *;

-- name: ListAdvertisers :many
SELECT * FROM advertisers WHERE deleted_at IS NULL ORDER BY id;

-- name: UpdateAdvertiser :one
UPDATE advertisers
SET name = COALESCE(sqlc.narg('name'), name),
    contact = COALESCE(sqlc.narg('contact'), contact),
    updated_at = now()
WHERE id = sqlc.arg('id') AND deleted_at IS NULL
RETURNING *;

-- name: SetAdvertiserStatus :execrows
UPDATE advertisers SET status = sqlc.arg('status'), updated_at = now()
WHERE id = sqlc.arg('id') AND deleted_at IS NULL;

-- name: CascadeOfflineAds :exec
-- 广告商下架单向写级联：名下未删广告一并下架（重开不反向恢复）。
UPDATE ads SET status = 'offline', updated_at = now()
WHERE advertiser_id = $1 AND deleted_at IS NULL;

-- name: CreateAd :one
INSERT INTO ads (advertiser_id, title, media_path, link, stock_total, stock_remaining, expires_at)
VALUES (sqlc.arg('advertiser_id'), sqlc.arg('title'), sqlc.arg('media_path'), sqlc.narg('link'),
        sqlc.arg('stock'), sqlc.arg('stock'), sqlc.narg('expires_at'))
RETURNING *;

-- name: ListAds :many
SELECT * FROM ads WHERE deleted_at IS NULL ORDER BY id;

-- name: UpdateAd :one
UPDATE ads
SET title = COALESCE(sqlc.narg('title'), title),
    media_path = COALESCE(sqlc.narg('media_path'), media_path),
    link = COALESCE(sqlc.narg('link'), link),
    expires_at = COALESCE(sqlc.narg('expires_at'), expires_at),
    updated_at = now()
WHERE id = sqlc.arg('id') AND deleted_at IS NULL
RETURNING *;

-- name: SetAdStatus :execrows
UPDATE ads SET status = sqlc.arg('status'), updated_at = now()
WHERE id = sqlc.arg('id') AND deleted_at IS NULL;

-- name: SoftDeleteAd :execrows
UPDATE ads SET deleted_at = now(), updated_at = now()
WHERE id = $1 AND deleted_at IS NULL;
