-- name: InsertLike :execrows
INSERT INTO likes (customer_id, video_id) VALUES ($1, $2)
ON CONFLICT (customer_id, video_id) DO NOTHING;

-- name: ReviveLike :execrows
UPDATE likes SET deleted_at = NULL
WHERE customer_id = $1 AND video_id = $2 AND deleted_at IS NOT NULL;

-- name: SoftDeleteLike :execrows
UPDATE likes SET deleted_at = now()
WHERE customer_id = $1 AND video_id = $2 AND deleted_at IS NULL;

-- name: BumpVideoCounters :exec
-- 通用计数与热度原子累计（ADR-0004：计数=原子条件 UPDATE；热度只增不减由调用方保证 delta >= 0）
UPDATE videos
SET click_count      = click_count + sqlc.arg(click_delta),
    valid_play_count = valid_play_count + sqlc.arg(valid_delta),
    like_count       = like_count + sqlc.arg(like_delta),
    comment_count    = comment_count + sqlc.arg(comment_delta),
    hot_score        = hot_score + sqlc.arg(hot_delta),
    updated_at       = now()
WHERE id = $1;

-- name: InsertComment :one
INSERT INTO comments (video_id, customer_id, content, comment_date)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: SoftDeleteComment :one
UPDATE comments SET deleted_at = now()
WHERE id = $1 AND customer_id = $2 AND deleted_at IS NULL
RETURNING video_id;

-- name: ListVideoComments :many
SELECT c.id, c.content, c.created_at,
       cust.public_id AS author_public_id, cust.username AS author_username
FROM comments c
JOIN customers cust ON cust.id = c.customer_id
WHERE c.video_id = $1 AND c.deleted_at IS NULL
  AND (sqlc.arg(cursor)::bigint = 0 OR c.id < sqlc.arg(cursor))
ORDER BY c.id DESC
LIMIT sqlc.arg(row_limit);

-- name: InsertPlay :one
INSERT INTO plays (customer_id, video_id) VALUES ($1, $2)
RETURNING *;

-- name: MarkPlayValid :one
UPDATE plays SET valid_at = now()
WHERE id = $1 AND customer_id = $2 AND valid_at IS NULL
RETURNING video_id;
