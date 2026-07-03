-- +goose Up
CREATE TABLE sign_ins (
    id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    customer_id   bigint NOT NULL REFERENCES customers (id),
    sign_date     date NOT NULL,
    reward_amount bigint NOT NULL DEFAULT 0,
    created_at    timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX sign_ins_daily_key ON sign_ins (customer_id, sign_date);

CREATE TABLE milestone_awards (
    id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    customer_id   bigint NOT NULL REFERENCES customers (id),
    month         date NOT NULL,
    threshold     int NOT NULL,
    reward_amount bigint NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX milestone_awards_key ON milestone_awards (customer_id, month, threshold);

CREATE TABLE activity_reward_configs (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    kind       activity_reward_kind NOT NULL,
    threshold  int NOT NULL DEFAULT 0,
    reward     bigint NOT NULL CHECK (reward >= 0),
    enabled    boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX activity_reward_configs_key ON activity_reward_configs (kind, threshold);

CREATE TABLE vip_level_configs (
    level                 int PRIMARY KEY CHECK (level BETWEEN 0 AND 3),
    name                  text NOT NULL,
    price                 bigint NOT NULL CHECK (price >= 0),
    reward_multiplier_bp  int NOT NULL DEFAULT 10000 CHECK (reward_multiplier_bp >= 10000),
    invite_reward_cap     bigint NOT NULL DEFAULT 0,
    updated_at            timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE vip_purchases (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    customer_id bigint NOT NULL REFERENCES customers (id),
    from_level  int NOT NULL,
    to_level    int NOT NULL,
    price       bigint NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX vip_purchases_customer_idx ON vip_purchases (customer_id);

-- +goose Down
DROP TABLE vip_purchases;
DROP TABLE vip_level_configs;
DROP TABLE activity_reward_configs;
DROP TABLE milestone_awards;
DROP TABLE sign_ins;
