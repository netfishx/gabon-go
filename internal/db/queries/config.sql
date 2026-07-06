-- name: GetInviteValidReward :one
-- 邀请有效奖励金额：缺行或停用返回 ErrNoRows，调用方按"不发放"处理。
SELECT reward FROM activity_reward_configs
WHERE kind = 'invite_valid' AND threshold = 0 AND enabled;

-- name: GetVipInviteRewardCap :one
-- VIP 档邀请奖励上限；四档为种子数据，缺行即数据异常。
SELECT invite_reward_cap FROM vip_level_configs WHERE level = $1;
