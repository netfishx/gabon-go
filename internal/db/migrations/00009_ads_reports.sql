-- +goose Up
CREATE TABLE advertisers (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name       text NOT NULL,
    contact    text,
    status     ad_status NOT NULL DEFAULT 'active',
    deleted_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE ads (
    id              bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    advertiser_id   bigint NOT NULL REFERENCES advertisers (id),
    title           text NOT NULL,
    media_path      text NOT NULL,
    link            text,
    stock_total     int NOT NULL DEFAULT 0,
    stock_remaining int NOT NULL DEFAULT 0 CHECK (stock_remaining >= 0),
    status          ad_status NOT NULL DEFAULT 'active',
    deleted_at      timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX ads_serving_idx ON ads (id)
    WHERE status = 'active' AND deleted_at IS NULL AND stock_remaining > 0;
CREATE INDEX ads_advertiser_idx ON ads (advertiser_id);

CREATE TABLE ad_watches (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    customer_id bigint NOT NULL REFERENCES customers (id),
    ad_id       bigint NOT NULL REFERENCES ads (id),
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX ad_watches_created_idx ON ad_watches (created_at);
CREATE INDEX ad_watches_customer_idx ON ad_watches (customer_id, created_at);

CREATE TABLE daily_actives (
    customer_id bigint NOT NULL REFERENCES customers (id),
    active_date date NOT NULL,
    PRIMARY KEY (customer_id, active_date)
);

CREATE INDEX daily_actives_date_idx ON daily_actives (active_date);

-- +goose Down
DROP TABLE daily_actives;
DROP TABLE ad_watches;
DROP TABLE ads;
DROP TABLE advertisers;
