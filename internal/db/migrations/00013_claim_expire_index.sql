-- +goose Up
-- 过期扫描状态集合调整为 {claimed, rejected}（M5 #37 定稿）：
-- 待审核(submitted)豁免——用户已尽义务，积压是运营问题；已发奖(rewarded)本就终态。
DROP INDEX task_claims_expire_idx;
CREATE INDEX task_claims_expire_idx ON task_claims (expires_at)
    WHERE status IN ('claimed', 'rejected');

-- +goose Down
DROP INDEX task_claims_expire_idx;
CREATE INDEX task_claims_expire_idx ON task_claims (expires_at)
    WHERE status IN ('claimed', 'submitted');
