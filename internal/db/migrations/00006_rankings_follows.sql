-- +goose Up
CREATE TABLE rankings (
    id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    period       ranking_period NOT NULL,
    period_start date NOT NULL,
    rank         int NOT NULL,
    video_id     bigint NOT NULL REFERENCES videos (id),
    score        bigint NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX rankings_rank_key ON rankings (period, period_start, rank);
CREATE UNIQUE INDEX rankings_video_key ON rankings (period, period_start, video_id);

CREATE TABLE follows (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    follower_id bigint NOT NULL REFERENCES customers (id),
    followee_id bigint NOT NULL REFERENCES customers (id),
    deleted_at  timestamptz,
    created_at  timestamptz NOT NULL DEFAULT now(),
    CHECK (follower_id <> followee_id)
);

-- 不带 WHERE：软删复用行（取关→再关翻转同一行）
CREATE UNIQUE INDEX follows_pair_key ON follows (follower_id, followee_id);
CREATE INDEX follows_followee_idx ON follows (followee_id) WHERE deleted_at IS NULL;

-- +goose Down
DROP TABLE follows;
DROP TABLE rankings;
