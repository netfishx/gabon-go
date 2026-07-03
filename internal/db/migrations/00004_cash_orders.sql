-- +goose Up
CREATE TABLE bank_cards (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    customer_id bigint NOT NULL REFERENCES customers (id),
    card_no     text NOT NULL,
    holder_name text NOT NULL,
    bank_name   text NOT NULL,
    bank_code   text,
    province    text,
    city        text,
    deleted_at  timestamptz,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX bank_cards_customer_idx ON bank_cards (customer_id);

CREATE TABLE recharge_orders (
    id                bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    order_no          text NOT NULL,
    customer_id       bigint NOT NULL REFERENCES customers (id),
    amount            bigint NOT NULL CHECK (amount > 0),
    fiat_amount       bigint NOT NULL CHECK (fiat_amount > 0),
    currency          text NOT NULL DEFAULT 'CNY',
    payment_method    text,
    provider          text,
    provider_order_no text,
    provider_status   text,
    status            recharge_order_status NOT NULL DEFAULT 'pending_payment',
    failure_code      text,
    failure_reason    text,
    expires_at        timestamptz,
    completed_at      timestamptz,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX recharge_orders_order_no_key ON recharge_orders (order_no);
CREATE UNIQUE INDEX recharge_orders_provider_key
    ON recharge_orders (provider, provider_order_no)
    WHERE provider_order_no IS NOT NULL;
CREATE INDEX recharge_orders_customer_idx ON recharge_orders (customer_id, id DESC);
CREATE INDEX recharge_orders_status_idx ON recharge_orders (status) WHERE status = 'pending_payment';

CREATE TABLE withdrawal_orders (
    id                bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    order_no          text NOT NULL,
    customer_id       bigint NOT NULL REFERENCES customers (id),
    amount            bigint NOT NULL CHECK (amount > 0),
    fiat_amount       bigint NOT NULL CHECK (fiat_amount > 0),
    currency          text NOT NULL DEFAULT 'CNY',
    bank_card_id      bigint REFERENCES bank_cards (id),
    payee_account     text NOT NULL,
    payee_name        text NOT NULL,
    payee_bank        text,
    payee_bank_code   text,
    payee_province    text,
    payee_city        text,
    provider          text,
    provider_order_no text,
    provider_status   text,
    status            withdrawal_order_status NOT NULL DEFAULT 'pending_review',
    reviewed_by       bigint REFERENCES admins (id),
    reviewed_at       timestamptz,
    reject_reason     text,
    failure_code      text,
    failure_reason    text,
    completed_at      timestamptz,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX withdrawal_orders_order_no_key ON withdrawal_orders (order_no);
CREATE UNIQUE INDEX withdrawal_orders_provider_key
    ON withdrawal_orders (provider, provider_order_no)
    WHERE provider_order_no IS NOT NULL;
CREATE INDEX withdrawal_orders_customer_idx ON withdrawal_orders (customer_id, id DESC);
CREATE INDEX withdrawal_orders_status_idx ON withdrawal_orders (status)
    WHERE status IN ('pending_review', 'paying');

CREATE TABLE payment_events (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    order_no   text NOT NULL,
    provider   text NOT NULL,
    direction  payment_event_direction NOT NULL,
    payload    jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX payment_events_order_no_idx ON payment_events (order_no);

-- +goose Down
DROP TABLE payment_events;
DROP TABLE withdrawal_orders;
DROP TABLE recharge_orders;
DROP TABLE bank_cards;
