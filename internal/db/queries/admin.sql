-- name: CreateAdmin :one
INSERT INTO admins (username, password_hash, role)
VALUES ($1, $2, $3)
RETURNING *;

-- name: GetAdminByUsername :one
SELECT * FROM admins WHERE username = $1 AND deleted_at IS NULL;

-- name: GetAdminByID :one
SELECT * FROM admins WHERE id = $1 AND deleted_at IS NULL;

-- name: CountAdmins :one
SELECT count(*) FROM admins WHERE deleted_at IS NULL;

-- name: SetAdminLastLogin :exec
UPDATE admins SET last_login_at = now(), updated_at = now() WHERE id = $1;
