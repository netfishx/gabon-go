-- name: InsertSignIn :one
-- 日签：唯一约束 (customer_id, sign_date) 防重签；撞约束由调用方识别为"今日已签"。
INSERT INTO sign_ins (customer_id, sign_date, reward_amount)
VALUES ($1, $2, sqlc.arg('reward_amount'))
RETURNING *;

-- name: CountSignInsInMonth :one
-- 当月累计签到天数（自然月，date_trunc）。
SELECT COUNT(*) FROM sign_ins
WHERE customer_id = $1 AND date_trunc('month', sign_date) = date_trunc('month', sqlc.arg('any_day')::date);

-- name: TodaySigned :one
SELECT EXISTS (SELECT 1 FROM sign_ins WHERE customer_id = $1 AND sign_date = $2)::bool;

-- name: GetDailySignInReward :one
-- 日签基础奖励：缺行或停用返回 ErrNoRows，调用方按"不发放"处理。
SELECT reward FROM activity_reward_configs
WHERE kind = 'daily' AND threshold = 0 AND enabled;

-- name: GetMilestoneReward :one
-- 里程碑档位奖励：threshold = 当月累计天数；缺行或停用返回 ErrNoRows（该天数不是里程碑）。
SELECT reward FROM activity_reward_configs
WHERE kind = 'milestone' AND threshold = $1 AND enabled;

-- name: InsertMilestoneAward :one
-- 里程碑发放：唯一约束 (customer_id, month, threshold) 幂等（同月同档只发一次）。
INSERT INTO milestone_awards (customer_id, month, threshold, reward_amount)
VALUES ($1, sqlc.arg('month')::date, sqlc.arg('threshold'), sqlc.arg('reward_amount'))
RETURNING *;

-- name: NextMilestoneThreshold :one
-- 大于当前累计天数的最近里程碑档位（供状态端点显示"还差几天"）；无更高档位返回 ErrNoRows。
SELECT threshold FROM activity_reward_configs
WHERE kind = 'milestone' AND enabled AND threshold > $1
ORDER BY threshold
LIMIT 1;

-- name: LockCustomerForSignIn :one
-- 签到事务开头锁客户行并取 VIP 倍率：串行化同一客户的签到事务，
-- 保证相邻日跨午夜并发时 CountSignInsInMonth 读到一致视图（PR #58 review P2）。
-- FOR NO KEY UPDATE OF c 只锁 customers 行，不与注册插入的 FK KEY SHARE 冲突。
SELECT v.reward_multiplier_bp FROM customers c
JOIN vip_level_configs v ON v.level = c.vip_level
WHERE c.id = $1
FOR NO KEY UPDATE OF c;
