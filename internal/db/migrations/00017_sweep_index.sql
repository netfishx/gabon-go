-- +goose Up
DROP INDEX recharge_orders_status_idx;
CREATE INDEX recharge_orders_pending_expires_idx
    ON recharge_orders (expires_at)
    WHERE status = 'pending_payment';

-- +goose Down
DROP INDEX recharge_orders_pending_expires_idx;
CREATE INDEX recharge_orders_status_idx
    ON recharge_orders (status)
    WHERE status = 'pending_payment';
