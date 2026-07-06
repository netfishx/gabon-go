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
-- 提交证明：claimed/rejected 可提交（驳回重提覆盖凭证回 submitted，清空上轮驳回痕迹）。
UPDATE task_claims
SET status = 'submitted', proof_text = sqlc.narg('proof_text'),
    proof_images = sqlc.arg('proof_images'), submitted_at = now(),
    reviewed_by = NULL, reviewed_at = NULL, review_remark = NULL, updated_at = now()
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
-- 奖励取领取时快照 reward_base，与审核实际发奖口径一致（定义改 reward 不影响在途）。
SELECT tc.id, tc.customer_id, tc.proof_text, tc.proof_images, tc.submitted_at,
       ct.name AS task_name, ct.requirement, tc.reward_base
FROM task_claims tc
JOIN claim_tasks ct ON ct.id = tc.task_id
WHERE tc.status = 'submitted' AND tc.id > sqlc.arg('cursor')
ORDER BY tc.id
LIMIT sqlc.arg('row_limit');

-- name: ExpireClaims :execrows
-- 时间过期作废 {claimed, rejected}：submitted 豁免（用户已尽义务，积压属运营问题）、rewarded 终态。
-- 注意与 RewriteInflightExpiry / VoidInflightClaims 的"未终态"集合 {claimed,submitted,rejected} 有意不同：
-- 时间过期豁免 submitted，而运营动作（改 ends_at 回写、软删作废）纳入 submitted。
UPDATE task_claims
SET status = 'expired', updated_at = now()
WHERE expires_at IS NOT NULL AND expires_at < now()
  AND status IN ('claimed', 'rejected');

-- name: ListClaimTasksForCustomer :many
-- 客户面可领取列表：启用未删的全部限时任务 + 查看者领取状态（三态分组在应用层算）。
SELECT ct.id, ct.name, ct.icon_path, ct.min_vip_level, ct.reward,
       ct.requirement, ct.flow, ct.link, ct.starts_at, ct.ends_at,
       tc.status AS claim_status
FROM claim_tasks ct
LEFT JOIN task_claims tc ON tc.task_id = ct.id AND tc.customer_id = $1
WHERE ct.enabled AND ct.deleted_at IS NULL
ORDER BY ct.display_order, ct.id;

-- name: GetClaimTaskForCustomer :one
-- 任务详情 + 查看者领取状态。可见性：上架未删的公开可见；已下架/软删的仅本人有领取记录时可看历史，
-- 否则无行（404）——不向路人泄露已撤下的任务定义。
SELECT ct.id, ct.name, ct.icon_path, ct.min_vip_level, ct.reward,
       ct.requirement, ct.flow, ct.link, ct.starts_at, ct.ends_at, ct.deleted_at,
       tc.status AS claim_status
FROM claim_tasks ct
LEFT JOIN task_claims tc ON tc.task_id = ct.id AND tc.customer_id = $1
WHERE ct.id = $2
  AND ((ct.enabled AND ct.deleted_at IS NULL) OR tc.id IS NOT NULL);

-- name: ListMyClaims :many
-- 我的领取记录：进行中 {claimed,submitted,rejected} / 已完成 {rewarded,expired}（id 降序）。
SELECT tc.id, tc.status, tc.reward_base, tc.reward_granted, tc.review_remark,
       tc.claimed_at, tc.rewarded_at, ct.name AS task_name
FROM task_claims tc
JOIN claim_tasks ct ON ct.id = tc.task_id
WHERE tc.customer_id = $1 AND tc.status::text = ANY(sqlc.arg('statuses')::text[])
ORDER BY tc.id DESC;

-- name: CreatePeriodicTask :one
INSERT INTO periodic_tasks (name, description, icon_path, category, period, target, reward, display_order, enabled)
VALUES (sqlc.arg('name'), sqlc.narg('description'), sqlc.narg('icon_path'), sqlc.arg('category'),
        sqlc.arg('period'), sqlc.arg('target'), sqlc.arg('reward'), sqlc.arg('display_order'), true)
RETURNING *;

-- name: ListPeriodicTasksAdmin :many
SELECT * FROM periodic_tasks WHERE deleted_at IS NULL ORDER BY display_order, id;

-- name: UpdatePeriodicTask :one
-- 部分更新：nil 字段不动。
UPDATE periodic_tasks
SET name          = COALESCE(sqlc.narg('name'), name),
    description   = COALESCE(sqlc.narg('description'), description),
    icon_path     = COALESCE(sqlc.narg('icon_path'), icon_path),
    target        = COALESCE(sqlc.narg('target'), target),
    reward        = COALESCE(sqlc.narg('reward'), reward),
    display_order = COALESCE(sqlc.narg('display_order'), display_order),
    enabled       = COALESCE(sqlc.narg('enabled'), enabled),
    updated_at    = now()
WHERE id = sqlc.arg('id') AND deleted_at IS NULL
RETURNING *;

-- name: CreateClaimTask :one
INSERT INTO claim_tasks (name, description, icon_path, min_vip_level, reward,
                         requirement, flow, link, display_order, starts_at, ends_at, enabled)
VALUES (sqlc.arg('name'), sqlc.narg('description'), sqlc.narg('icon_path'),
        sqlc.arg('min_vip_level'), sqlc.arg('reward'),
        sqlc.narg('requirement'), sqlc.narg('flow'), sqlc.narg('link'),
        sqlc.arg('display_order'), sqlc.narg('starts_at'), sqlc.narg('ends_at'), true)
RETURNING *;

-- name: ListClaimTasksAdmin :many
SELECT * FROM claim_tasks WHERE deleted_at IS NULL ORDER BY display_order, id;

-- name: UpdateClaimTask :one
UPDATE claim_tasks
SET name          = COALESCE(sqlc.narg('name'), name),
    description   = COALESCE(sqlc.narg('description'), description),
    icon_path     = COALESCE(sqlc.narg('icon_path'), icon_path),
    min_vip_level = COALESCE(sqlc.narg('min_vip_level'), min_vip_level),
    reward        = COALESCE(sqlc.narg('reward'), reward),
    requirement   = COALESCE(sqlc.narg('requirement'), requirement),
    flow          = COALESCE(sqlc.narg('flow'), flow),
    link          = COALESCE(sqlc.narg('link'), link),
    display_order = COALESCE(sqlc.narg('display_order'), display_order),
    starts_at     = COALESCE(sqlc.narg('starts_at'), starts_at),
    ends_at       = COALESCE(sqlc.narg('ends_at'), ends_at),
    updated_at    = now()
WHERE id = sqlc.arg('id') AND deleted_at IS NULL
RETURNING *;

-- name: RewriteInflightExpiry :exec
-- 编辑 ends_at 时把未终态在途记录的 expires_at 同步回写（运营语义）。
-- 未终态 = {claimed,submitted,rejected}（含 submitted，与 ExpireClaims 的时间过期集合有意不同）。
UPDATE task_claims SET expires_at = sqlc.arg('expires_at'), updated_at = now()
WHERE task_id = sqlc.arg('task_id') AND status IN ('claimed', 'submitted', 'rejected');

-- name: SetClaimTaskEnabled :execrows
UPDATE claim_tasks SET enabled = sqlc.arg('enabled'), updated_at = now()
WHERE id = sqlc.arg('id') AND deleted_at IS NULL;

-- name: SoftDeleteClaimTask :execrows
UPDATE claim_tasks SET deleted_at = now(), updated_at = now()
WHERE id = $1 AND deleted_at IS NULL;

-- name: VoidInflightClaims :exec
-- 软删定义时作废全部未终态在途记录（已发奖终态不动）。
-- 未终态 = {claimed,submitted,rejected}（含 submitted，与 ExpireClaims 的时间过期集合有意不同）。
UPDATE task_claims SET status = 'expired', updated_at = now()
WHERE task_id = $1 AND status IN ('claimed', 'submitted', 'rejected');
