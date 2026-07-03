-- +goose Up
CREATE TYPE customer_status AS ENUM ('active', 'banned');
CREATE TYPE admin_role AS ENUM ('admin', 'normal');
CREATE TYPE admin_status AS ENUM ('active', 'disabled');
CREATE TYPE video_status AS ENUM (
    'pending_transcode', 'transcoding', 'pending_review',
    'published', 'rejected', 'transcode_failed'
);
CREATE TYPE transcode_job_status AS ENUM ('queued', 'running', 'succeeded', 'failed');
CREATE TYPE transaction_type AS ENUM (
    'recharge', 'withdrawal', 'watch_reward', 'periodic_task_reward',
    'claim_task_reward', 'sign_in_reward', 'milestone_reward',
    'invite_valid_reward', 'content_reward', 'vip_purchase'
);
CREATE TYPE recharge_order_status AS ENUM ('pending_payment', 'succeeded', 'failed', 'cancelled');
CREATE TYPE withdrawal_order_status AS ENUM ('pending_review', 'rejected', 'paying', 'succeeded', 'failed');
CREATE TYPE payment_event_direction AS ENUM ('request', 'response', 'callback', 'query');
CREATE TYPE ranking_period AS ENUM ('weekly', 'monthly');
CREATE TYPE task_period AS ENUM ('daily', 'weekly', 'monthly');
CREATE TYPE task_category AS ENUM (
    'watch_video', 'upload_video', 'share_video', 'comment',
    'like', 'login', 'invite_friend', 'watch_ad'
);
CREATE TYPE claim_status AS ENUM ('claimed', 'submitted', 'approved', 'rewarded', 'rejected', 'expired');
CREATE TYPE activity_reward_kind AS ENUM ('daily', 'milestone', 'invite_valid');
CREATE TYPE ad_status AS ENUM ('active', 'offline');

-- +goose Down
DROP TYPE ad_status;
DROP TYPE activity_reward_kind;
DROP TYPE claim_status;
DROP TYPE task_category;
DROP TYPE task_period;
DROP TYPE ranking_period;
DROP TYPE payment_event_direction;
DROP TYPE withdrawal_order_status;
DROP TYPE recharge_order_status;
DROP TYPE transaction_type;
DROP TYPE transcode_job_status;
DROP TYPE video_status;
DROP TYPE admin_status;
DROP TYPE admin_role;
DROP TYPE customer_status;
