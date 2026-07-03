-- name: CreateWallet :exec
INSERT INTO wallets (customer_id) VALUES ($1);

-- name: GetWallet :one
SELECT * FROM wallets WHERE customer_id = $1;
