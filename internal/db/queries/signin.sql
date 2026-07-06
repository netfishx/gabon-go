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
