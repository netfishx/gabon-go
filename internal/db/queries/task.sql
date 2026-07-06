-- name: ListEnabledPeriodicTasks :many
SELECT * FROM periodic_tasks
WHERE enabled AND deleted_at IS NULL
ORDER BY display_order, id;

-- name: ListEnabledPeriodicTasksByCategory :many
SELECT * FROM periodic_tasks
WHERE category = $1 AND enabled AND deleted_at IS NULL
ORDER BY id;

-- name: UpsertTaskProgress :one
-- 进度 +1：新周期首个事件懒建行（target 快照）；已达标不再累加（无更新即无行，调用方按封顶处理）。
INSERT INTO periodic_task_progress (customer_id, task_id, period_key, progress, target)
VALUES (sqlc.arg('customer_id'), sqlc.arg('task_id'), sqlc.arg('period_key'), 1, sqlc.arg('target'))
ON CONFLICT (customer_id, task_id, period_key) DO UPDATE
SET progress = periodic_task_progress.progress + 1, updated_at = now()
WHERE periodic_task_progress.progress < periodic_task_progress.target
RETURNING *;

-- name: GrantTaskRewardIfDue :execrows
-- 达标发奖翻转（幂等第二层）：reward_granted_at IS NULL 保证只发一次。
UPDATE periodic_task_progress
SET completed_at = now(), reward_granted_at = now(),
    reward_amount = sqlc.arg('reward_amount'), updated_at = now()
WHERE id = sqlc.arg('id') AND progress >= target AND reward_granted_at IS NULL;

-- name: GetCustomerRewardMultiplierBp :one
SELECT v.reward_multiplier_bp FROM customers c
JOIN vip_level_configs v ON v.level = c.vip_level
WHERE c.id = $1;

-- name: IsFirstValidPlayInPeriod :one
-- watch 防刷：周期内该客户×该视频仅 id 最小的有效播放推进——并发上报下也恰好一次。
SELECT (NOT EXISTS (
    SELECT 1 FROM plays p2
    WHERE p2.customer_id = sqlc.arg('customer_id')
      AND p2.video_id = (SELECT p1.video_id FROM plays p1 WHERE p1.id = sqlc.arg('play_id'))
      AND p2.valid_at IS NOT NULL
      AND p2.valid_at >= sqlc.arg('period_start')
      AND p2.id < sqlc.arg('play_id')
))::bool;

-- name: ListTaskProgressForKeys :many
SELECT * FROM periodic_task_progress
WHERE customer_id = $1 AND period_key = ANY(sqlc.arg('period_keys')::text[]);
