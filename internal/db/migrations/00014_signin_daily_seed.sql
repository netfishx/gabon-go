-- +goose Up
-- 日签基础奖励种子：1 钻对齐旧版代码兜底（旧版无种子行、缺行时代码默认 1）。
-- 里程碑档位无种子（缺行不发，同 invite_valid 机制）——档位为现网运营数据。
INSERT INTO activity_reward_configs (kind, threshold, reward, enabled)
VALUES ('daily', 0, 1, true);

-- +goose Down
DELETE FROM activity_reward_configs WHERE kind = 'daily' AND threshold = 0;
