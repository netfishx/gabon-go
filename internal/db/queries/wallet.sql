-- name: CreateWallet :exec
INSERT INTO wallets (customer_id) VALUES ($1);
