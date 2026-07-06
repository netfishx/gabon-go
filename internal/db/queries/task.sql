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

-- name: MarkWatchProgress :execrows
-- watch 防刷标记（推进事务内执行）：唯一约束仲裁并发，0 行 = 本周期该视频已计过。
INSERT INTO watch_progress_marks (customer_id, video_id, period_key)
VALUES ($1, $2, $3)
ON CONFLICT DO NOTHING;

-- name: ListTaskProgressForKeys :many
SELECT * FROM periodic_task_progress
WHERE customer_id = $1 AND period_key = ANY(sqlc.arg('period_keys')::text[]);

-- name: GetClaimTask :one
SELECT * FROM claim_tasks WHERE id = $1 AND deleted_at IS NULL;

-- name: InsertTaskClaim :one
-- 领取：一人一次由 (customer_id, task_id) 唯一约束保证；reward_base 与 expires_at 领取时快照。
INSERT INTO task_claims (customer_id, task_id, status, reward_base, expires_at)
VALUES ($1, $2, 'claimed', sqlc.arg('reward_base'), sqlc.arg('expires_at'))
RETURNING *;

-- name: GetTaskClaim :one
SELECT * FROM task_claims WHERE id = $1;

-- name: SubmitTaskClaim :execrows
-- 提交证明：claimed/rejected 可提交（驳回重提覆盖凭证回 submitted）。
UPDATE task_claims
SET status = 'submitted', proof_text = sqlc.narg('proof_text'),
    proof_images = sqlc.arg('proof_images'), submitted_at = now(), updated_at = now()
WHERE id = sqlc.arg('id') AND customer_id = sqlc.arg('customer_id')
  AND status IN ('claimed', 'rejected');

-- name: RejectTaskClaim :execrows
UPDATE task_claims
SET status = 'rejected', reviewed_by = sqlc.arg('reviewed_by'), reviewed_at = now(),
    review_remark = sqlc.arg('review_remark'), updated_at = now()
WHERE id = sqlc.arg('id') AND status = 'submitted';

-- name: ApproveTaskClaim :one
-- 审核即发奖一步：submitted→rewarded 单次条件 UPDATE，reviewed_*/rewarded_* 同落。
-- 返回 customer_id 与实发额供同事务入账。
UPDATE task_claims
SET status = 'rewarded', reviewed_by = sqlc.arg('reviewed_by'), reviewed_at = now(),
    reward_granted = sqlc.arg('reward_granted'), rewarded_at = now(), updated_at = now()
WHERE id = sqlc.arg('id') AND status = 'submitted'
RETURNING customer_id;

-- name: ListPendingClaims :many
-- 待审核队列（先进先出，id 升序游标）：附任务要求与凭证供管理员参考。
SELECT tc.id, tc.customer_id, tc.proof_text, tc.proof_images, tc.submitted_at,
       ct.name AS task_name, ct.requirement, ct.reward
FROM task_claims tc
JOIN claim_tasks ct ON ct.id = tc.task_id
WHERE tc.status = 'submitted' AND tc.id > sqlc.arg('cursor')
ORDER BY tc.id
LIMIT sqlc.arg('row_limit');
