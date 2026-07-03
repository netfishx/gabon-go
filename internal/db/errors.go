package db

import (
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
)

const pgUniqueViolation = "23505"

// UniqueViolationConstraint 返回唯一约束冲突命中的约束名；非唯一冲突返回空串。
// 各域据此把数据库约束映射为业务错误（如用户名占用）或触发重试（短码碰撞）。
func UniqueViolationConstraint(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolation {
		return pgErr.ConstraintName
	}
	return ""
}
