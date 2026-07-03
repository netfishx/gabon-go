-- +goose Up
CREATE TABLE customers (
    id                       bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    public_id                text NOT NULL,
    username                 text NOT NULL,
    password_hash            text NOT NULL,
    password_changed_at      timestamptz NOT NULL DEFAULT now(),
    name                     text,
    phone                    text,
    email                    text,
    avatar_path              text,
    signature                text,
    invite_code              text NOT NULL,
    inviter_id               bigint REFERENCES customers (id),
    ancestors                bigint[] NOT NULL DEFAULT '{}',
    valid_at                 timestamptz,
    vip_level                int NOT NULL DEFAULT 0,
    status                   customer_status NOT NULL DEFAULT 'active',
    withdrawal_password_hash text,
    video_count              int NOT NULL DEFAULT 0,
    invite_count             int NOT NULL DEFAULT 0,
    follower_count           int NOT NULL DEFAULT 0,
    following_count          int NOT NULL DEFAULT 0,
    last_login_at            timestamptz,
    deleted_at               timestamptz,
    created_at               timestamptz NOT NULL DEFAULT now(),
    updated_at               timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX customers_public_id_key ON customers (public_id);
CREATE UNIQUE INDEX customers_username_key ON customers (username);
CREATE UNIQUE INDEX customers_invite_code_key ON customers (invite_code);
CREATE INDEX customers_inviter_id_idx ON customers (inviter_id);
CREATE INDEX customers_ancestors_idx ON customers USING gin (ancestors);

CREATE TABLE admins (
    id                  bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    username            text NOT NULL,
    password_hash       text NOT NULL,
    password_changed_at timestamptz NOT NULL DEFAULT now(),
    role                admin_role NOT NULL DEFAULT 'normal',
    status              admin_status NOT NULL DEFAULT 'active',
    last_login_at       timestamptz,
    deleted_at          timestamptz,
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX admins_username_key ON admins (username);

-- +goose Down
DROP TABLE admins;
DROP TABLE customers;
