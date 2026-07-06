-- +goose Up
-- 周期任务种子：四类活跃 × 每日，基础奖励 58 钻对齐现网实跑（M5 #37 定稿）。
-- watch_ad 行照常启用，事件源随广告域接入（此前进度恒 0 无害）。
INSERT INTO periodic_tasks (name, category, period, target, reward, display_order)
VALUES ('每日看视频', 'watch_video', 'daily', 5, 58, 1),
       ('每日看广告', 'watch_ad', 'daily', 3, 58, 2),
       ('每日点赞', 'like', 'daily', 20, 58, 3),
       ('每日评论', 'comment', 'daily', 5, 58, 4);

-- watch 防刷标记：每客户×视频×周期唯一，推进事务内 INSERT ON CONFLICT DO NOTHING——
-- 唯一约束仲裁并发上报，恰好一次推进（"读后写"判定在并发下会双计，PR #50 review P1）。
CREATE TABLE watch_progress_marks (
    customer_id bigint NOT NULL REFERENCES customers (id),
    video_id    bigint NOT NULL REFERENCES videos (id),
    period_key  text NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (customer_id, video_id, period_key)
);

-- +goose Down
DROP TABLE watch_progress_marks;
DELETE FROM periodic_tasks
WHERE name IN ('每日看视频', '每日看广告', '每日点赞', '每日评论');
