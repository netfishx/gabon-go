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
