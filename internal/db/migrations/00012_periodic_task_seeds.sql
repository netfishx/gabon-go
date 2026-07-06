-- +goose Up
-- 周期任务种子：四类活跃 × 每日，基础奖励 58 钻对齐现网实跑（M5 #37 定稿）。
-- watch_ad 行照常启用，事件源随广告域接入（此前进度恒 0 无害）。
INSERT INTO periodic_tasks (name, category, period, target, reward, display_order)
VALUES ('每日看视频', 'watch_video', 'daily', 5, 58, 1),
       ('每日看广告', 'watch_ad', 'daily', 3, 58, 2),
       ('每日点赞', 'like', 'daily', 20, 58, 3),
       ('每日评论', 'comment', 'daily', 5, 58, 4);

-- watch 防刷（每客户×视频×周期仅首次推进）的 EXISTS 判定走此索引
CREATE INDEX plays_watch_dedup_idx ON plays (customer_id, video_id)
    WHERE valid_at IS NOT NULL;

-- +goose Down
DROP INDEX plays_watch_dedup_idx;
DELETE FROM periodic_tasks
WHERE period = 'daily'
  AND category IN ('watch_video', 'watch_ad', 'like', 'comment');
