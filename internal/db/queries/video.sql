-- name: CreateVideo :one
INSERT INTO videos (public_id, customer_id, title, tags, storage_path)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: CreateTranscodeJob :one
INSERT INTO transcode_jobs (video_id) VALUES ($1)
RETURNING *;
