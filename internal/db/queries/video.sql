-- name: CreateVideo :one
INSERT INTO videos (public_id, customer_id, title, tags, storage_path)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: CreateTranscodeJob :one
INSERT INTO transcode_jobs (video_id) VALUES ($1)
RETURNING *;

-- name: GetVideoByPublicID :one
SELECT * FROM videos WHERE public_id = $1 AND deleted_at IS NULL;

-- name: ListPendingReviewVideos :many
SELECT * FROM videos
WHERE status = 'pending_review' AND deleted_at IS NULL
  AND (sqlc.arg(cursor)::bigint = 0 OR id > sqlc.arg(cursor))
ORDER BY id
LIMIT sqlc.arg(row_limit);

-- name: ApproveVideo :execrows
UPDATE videos
SET status = 'published', reviewed_by = $2, reviewed_at = now(), updated_at = now()
WHERE id = $1 AND status = 'pending_review';

-- name: RejectVideo :execrows
UPDATE videos
SET status = 'rejected', reviewed_by = $2, reviewed_at = now(),
    review_notes = $3, updated_at = now()
WHERE id = $1 AND status = 'pending_review';

-- name: IncrementVideoCount :exec
UPDATE customers SET video_count = video_count + 1, updated_at = now() WHERE id = $1;

-- name: ListFeedVideos :many
-- Feed 默认流：md5(id||seed) 伪随机序（行为差异 #3）——seed 固定则序稳定，游标为上页末行 rank
SELECT sqlc.embed(v), c.public_id AS author_public_id, c.username AS author_username,
       md5(v.id::text || sqlc.arg(seed)::text) AS feed_rank
FROM videos v
JOIN customers c ON c.id = v.customer_id
WHERE v.status = 'published' AND v.deleted_at IS NULL
  AND (sqlc.arg(cursor)::text = '' OR md5(v.id::text || sqlc.arg(seed)::text) > sqlc.arg(cursor))
ORDER BY feed_rank
LIMIT sqlc.arg(row_limit);

-- name: ListFeaturedVideos :many
SELECT sqlc.embed(v), c.public_id AS author_public_id, c.username AS author_username
FROM videos v
JOIN customers c ON c.id = v.customer_id
WHERE v.status = 'published' AND v.deleted_at IS NULL
  AND (sqlc.arg(cursor_id)::bigint = 0
       OR (v.hot_score, v.id) < (sqlc.arg(cursor_score)::bigint, sqlc.arg(cursor_id)::bigint))
ORDER BY v.hot_score DESC, v.id DESC
LIMIT sqlc.arg(row_limit);

-- name: GetPublishedVideoByPublicID :one
SELECT sqlc.embed(v), c.public_id AS author_public_id, c.username AS author_username
FROM videos v
JOIN customers c ON c.id = v.customer_id
WHERE v.public_id = $1 AND v.status = 'published' AND v.deleted_at IS NULL;

-- name: ListCustomerPublishedVideos :many
SELECT sqlc.embed(v), c.public_id AS author_public_id, c.username AS author_username
FROM videos v
JOIN customers c ON c.id = v.customer_id
WHERE v.customer_id = $1 AND v.status = 'published' AND v.deleted_at IS NULL
  AND (sqlc.arg(cursor)::bigint = 0 OR v.id < sqlc.arg(cursor))
ORDER BY v.id DESC
LIMIT sqlc.arg(row_limit);

-- name: ListMyVideos :many
SELECT * FROM videos
WHERE customer_id = $1 AND deleted_at IS NULL
  AND (sqlc.arg(cursor)::bigint = 0 OR id < sqlc.arg(cursor))
ORDER BY id DESC
LIMIT sqlc.arg(row_limit);

-- name: SoftDeleteVideo :one
-- RETURNING status：原子拿到删除时状态，published 才回退作者 video_count
--（避免"先读后删"窗口里并发过审导致计数漏减）。
UPDATE videos SET deleted_at = now(), updated_at = now()
WHERE id = $1 AND customer_id = $2 AND deleted_at IS NULL
RETURNING status;

-- name: DecrementVideoCount :exec
-- video_count 语义 = 已发布且未删除的作品数（有效用户判定"有作品"的输入），
-- 删除已发布视频时对称回退；video_count > 0 护栏防数据异常下的负值。
UPDATE customers SET video_count = video_count - 1, updated_at = now()
WHERE id = $1 AND video_count > 0;
