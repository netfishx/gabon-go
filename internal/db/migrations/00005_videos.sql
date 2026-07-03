-- +goose Up
CREATE TABLE videos (
    id               bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    public_id        text NOT NULL,
    customer_id      bigint NOT NULL REFERENCES customers (id),
    title            text NOT NULL,
    tags             text[] NOT NULL DEFAULT '{}' CHECK (cardinality(tags) <= 3),
    storage_path     text NOT NULL,
    hls_path         text,
    thumbnail_path   text,
    duration         int,
    width            int,
    height           int,
    file_size        bigint,
    mime_type        text,
    status           video_status NOT NULL DEFAULT 'pending_transcode',
    reviewed_by      bigint REFERENCES admins (id),
    reviewed_at      timestamptz,
    review_notes     text,
    click_count      bigint NOT NULL DEFAULT 0,
    valid_play_count bigint NOT NULL DEFAULT 0,
    like_count       bigint NOT NULL DEFAULT 0,
    comment_count    bigint NOT NULL DEFAULT 0,
    hot_score        bigint NOT NULL DEFAULT 0,
    deleted_at       timestamptz,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX videos_public_id_key ON videos (public_id);
CREATE INDEX videos_customer_idx ON videos (customer_id, id DESC);
CREATE INDEX videos_feed_idx ON videos (id DESC) WHERE status = 'published' AND deleted_at IS NULL;
CREATE INDEX videos_hot_idx ON videos (hot_score DESC) WHERE status = 'published' AND deleted_at IS NULL;
CREATE INDEX videos_review_idx ON videos (status) WHERE status = 'pending_review';

CREATE TABLE transcode_jobs (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    video_id    bigint NOT NULL REFERENCES videos (id),
    status      transcode_job_status NOT NULL DEFAULT 'queued',
    attempts    int NOT NULL DEFAULT 0,
    last_error  text,
    started_at  timestamptz,
    finished_at timestamptz,
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX transcode_jobs_claim_idx ON transcode_jobs (id) WHERE status = 'queued';
CREATE INDEX transcode_jobs_video_idx ON transcode_jobs (video_id);

CREATE TABLE likes (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    customer_id bigint NOT NULL REFERENCES customers (id),
    video_id    bigint NOT NULL REFERENCES videos (id),
    deleted_at  timestamptz,
    created_at  timestamptz NOT NULL DEFAULT now()
);

-- 不带 WHERE：含软删行，防"取消再赞"刷分（见 docs/schema.md）
CREATE UNIQUE INDEX likes_customer_video_key ON likes (customer_id, video_id);
CREATE INDEX likes_video_idx ON likes (video_id);

CREATE TABLE comments (
    id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    video_id     bigint NOT NULL REFERENCES videos (id),
    customer_id  bigint NOT NULL REFERENCES customers (id),
    content      text NOT NULL,
    comment_date date NOT NULL,
    deleted_at   timestamptz,
    created_at   timestamptz NOT NULL DEFAULT now()
);

-- 不带 WHERE：含软删行，防"删评再评"刷分（见 docs/schema.md）
CREATE UNIQUE INDEX comments_daily_key ON comments (customer_id, video_id, comment_date);
CREATE INDEX comments_video_idx ON comments (video_id, id DESC);

CREATE TABLE plays (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    customer_id bigint NOT NULL REFERENCES customers (id),
    video_id    bigint NOT NULL REFERENCES videos (id),
    played_at   timestamptz NOT NULL DEFAULT now(),
    valid_at    timestamptz
);

CREATE INDEX plays_played_at_idx ON plays (played_at);
CREATE INDEX plays_video_idx ON plays (video_id);
CREATE INDEX plays_customer_idx ON plays (customer_id, played_at);

-- +goose Down
DROP TABLE plays;
DROP TABLE comments;
DROP TABLE likes;
DROP TABLE transcode_jobs;
DROP TABLE videos;
