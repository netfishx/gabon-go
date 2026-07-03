-- +goose Up
CREATE TABLE wallets (
    customer_id bigint PRIMARY KEY REFERENCES customers (id),
    available   bigint NOT NULL DEFAULT 0 CHECK (available >= 0),
    frozen      bigint NOT NULL DEFAULT 0 CHECK (frozen >= 0),
    updated_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE transactions (
    id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    customer_id   bigint NOT NULL REFERENCES customers (id),
    type          transaction_type NOT NULL,
    amount        bigint NOT NULL CHECK (amount <> 0),
    balance_after bigint NOT NULL,
    ref_id        bigint,
    created_at    timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX transactions_type_ref_key
    ON transactions (type, ref_id) WHERE ref_id IS NOT NULL;
CREATE INDEX transactions_customer_idx ON transactions (customer_id, id DESC);

-- +goose Down
DROP TABLE transactions;
DROP TABLE wallets;
