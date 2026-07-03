-- +goose Up
CREATE TABLE periodic_tasks (
    id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name          text NOT NULL,
    description   text,
    icon_path     text,
    category      task_category NOT NULL,
    period        task_period NOT NULL,
    target        int NOT NULL CHECK (target > 0),
    reward        bigint NOT NULL CHECK (reward >= 0),
    display_order int NOT NULL DEFAULT 0,
    enabled       boolean NOT NULL DEFAULT true,
    deleted_at    timestamptz,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE claim_tasks (
    id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name          text NOT NULL,
    description   text,
    icon_path     text,
    min_vip_level int NOT NULL DEFAULT 0,
    reward        bigint NOT NULL CHECK (reward >= 0),
    requirement   text,
    flow          text,
    link          text,
    display_order int NOT NULL DEFAULT 0,
    enabled       boolean NOT NULL DEFAULT true,
    starts_at     timestamptz,
    ends_at       timestamptz,
    deleted_at    timestamptz,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE periodic_task_progress (
    id                bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    customer_id       bigint NOT NULL REFERENCES customers (id),
    task_id           bigint NOT NULL REFERENCES periodic_tasks (id),
    period_key        text NOT NULL,
    progress          int NOT NULL DEFAULT 0,
    target            int NOT NULL,
    completed_at      timestamptz,
    reward_granted_at timestamptz,
    reward_amount     bigint,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX periodic_task_progress_key
    ON periodic_task_progress (customer_id, task_id, period_key);

CREATE TABLE task_claims (
    id             bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    customer_id    bigint NOT NULL REFERENCES customers (id),
    task_id        bigint NOT NULL REFERENCES claim_tasks (id),
    status         claim_status NOT NULL DEFAULT 'claimed',
    proof_text     text,
    -- 证明图上界恒为 9；下界随状态：claimed/expired（未提交）允许为空，提交后各状态强制 ≥1
    proof_images   text[] NOT NULL DEFAULT '{}' CHECK (
        cardinality(proof_images) <= 9
        AND (status IN ('claimed', 'expired') OR cardinality(proof_images) >= 1)
    ),
    reward_base    bigint NOT NULL,
    reward_granted bigint,
    expires_at     timestamptz,
    claimed_at     timestamptz NOT NULL DEFAULT now(),
    submitted_at   timestamptz,
    reviewed_by    bigint REFERENCES admins (id),
    reviewed_at    timestamptz,
    review_remark  text,
    rewarded_at    timestamptz,
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX task_claims_customer_task_key ON task_claims (customer_id, task_id);
CREATE INDEX task_claims_review_idx ON task_claims (status) WHERE status = 'submitted';
CREATE INDEX task_claims_expire_idx ON task_claims (expires_at)
    WHERE status IN ('claimed', 'submitted');

-- +goose Down
DROP TABLE task_claims;
DROP TABLE periodic_task_progress;
DROP TABLE claim_tasks;
DROP TABLE periodic_tasks;
