-- name: ClaimTranscodeJob :one
UPDATE transcode_jobs
SET status = 'running', started_at = now(), attempts = attempts + 1
WHERE id = (
    SELECT id FROM transcode_jobs
    WHERE status = 'queued'
    ORDER BY id
    LIMIT 1
    FOR UPDATE SKIP LOCKED
)
RETURNING *;

-- name: SucceedTranscodeJob :exec
UPDATE transcode_jobs SET status = 'succeeded', finished_at = now() WHERE id = $1;

-- name: RequeueTranscodeJob :exec
UPDATE transcode_jobs SET status = 'queued', last_error = $2 WHERE id = $1;

-- name: FailTranscodeJob :exec
UPDATE transcode_jobs SET status = 'failed', finished_at = now(), last_error = $2 WHERE id = $1;

-- name: RequeueStaleTranscodeJobs :execrows
UPDATE transcode_jobs
SET status = 'queued'
WHERE status = 'running'
  AND started_at < now() - make_interval(secs => sqlc.arg(stale_seconds)::int);

-- name: GetVideoForTranscode :one
SELECT * FROM videos WHERE id = $1;

-- name: SetVideoTranscoding :exec
UPDATE videos SET status = 'transcoding', updated_at = now()
WHERE id = $1 AND status = 'pending_transcode';

-- name: SetVideoPendingTranscode :exec
UPDATE videos SET status = 'pending_transcode', updated_at = now()
WHERE id = $1 AND status = 'transcoding';

-- name: SetVideoTranscoded :exec
UPDATE videos
SET status = 'pending_review', hls_path = $2, thumbnail_path = $3,
    duration = $4, width = $5, height = $6, updated_at = now()
WHERE id = $1;

-- name: SetVideoTranscodeFailed :exec
UPDATE videos SET status = 'transcode_failed', updated_at = now() WHERE id = $1;
