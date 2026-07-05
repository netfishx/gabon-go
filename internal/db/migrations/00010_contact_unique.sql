-- +goose Up
-- 联系方式唯一性由数据库兜底（部分唯一索引，仅非 NULL 行）：
-- 旧版仅应用层 count 检查、并发有竞态，不复刻（M4 行为差异）。
CREATE UNIQUE INDEX customers_phone_key ON customers (phone) WHERE phone IS NOT NULL;
CREATE UNIQUE INDEX customers_email_key ON customers (email) WHERE email IS NOT NULL;

-- +goose Down
DROP INDEX customers_email_key;
DROP INDEX customers_phone_key;
