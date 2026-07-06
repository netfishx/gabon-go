-- +goose Up
-- VIP 档发布作品上限（复刻旧版 upload_video_limit，M5 #38 定稿）：≤0 = 不限。
ALTER TABLE vip_level_configs ADD COLUMN upload_video_limit int NOT NULL DEFAULT 0;

-- 回填四档种子（自旧版：普通 6 / 铜牌 30 / 银牌 50 / 金卡 100）
UPDATE vip_level_configs SET upload_video_limit = 6 WHERE level = 0;
UPDATE vip_level_configs SET upload_video_limit = 30 WHERE level = 1;
UPDATE vip_level_configs SET upload_video_limit = 50 WHERE level = 2;
UPDATE vip_level_configs SET upload_video_limit = 100 WHERE level = 3;

-- +goose Down
ALTER TABLE vip_level_configs DROP COLUMN upload_video_limit;
