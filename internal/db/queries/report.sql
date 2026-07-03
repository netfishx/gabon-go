-- name: UpsertDailyActive :exec
INSERT INTO daily_actives (customer_id, active_date)
VALUES ($1, $2)
ON CONFLICT DO NOTHING;
