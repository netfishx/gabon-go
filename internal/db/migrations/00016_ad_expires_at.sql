-- +goose Up
-- 广告过期维度（M5 #38 定稿）：NULL = 永不过期；在投判定纳入"未过期"。
-- 不复刻旧版"新建默认当日末"隐式缺省——运营显式填，缺省 NULL。
ALTER TABLE ads ADD COLUMN expires_at timestamptz;

-- 在投部分索引重建纳入过期维度（原索引仅 status/deleted/stock 三条件）。
-- expires_at 是时间条件不宜入部分索引谓词（now() 非 immutable），故索引保留三结构条件，
-- 过期条件在查询 WHERE 里判定。
-- +goose Down
ALTER TABLE ads DROP COLUMN expires_at;
