-- +goose Up
-- 邀请有效奖励种子：123 钻对齐旧版现网实跑值（旧版无种子行、靠代码硬编码兜底；
-- 新版纯配置驱动，缺行/停用即不发放——M4 行为差异）。
INSERT INTO activity_reward_configs (kind, threshold, reward, enabled)
VALUES ('invite_valid', 0, 123, true);

-- VIP 四档种子，自旧版换算：价格 元→钻（1 元 = 100 钻）、倍率 小数→万分比。
INSERT INTO vip_level_configs (level, name, price, reward_multiplier_bp, invite_reward_cap)
VALUES (0, '普通会员', 0, 10000, 5),
       (1, '铜牌VIP', 39900, 12000, 15),
       (2, '银牌VIP', 99900, 14000, 50),
       (3, '金卡VIP', 299900, 16000, 100);

-- +goose Down
DELETE FROM vip_level_configs WHERE level IN (0, 1, 2, 3);
DELETE FROM activity_reward_configs WHERE kind = 'invite_valid' AND threshold = 0;
