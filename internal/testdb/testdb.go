// Package testdb 集成测试共用的 Postgres 容器基建：起 Postgres 18 + 应用全量迁移。
// 仅供 _test 代码使用。
package testdb

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/netfishx/gabon-go/internal/db"
)

// Start 启动一次性 Postgres 18 容器并应用全量迁移，返回连接池与清理函数。
func Start(ctx context.Context) (pool *pgxpool.Pool, cleanup func(), err error) {
	container, err := postgres.Run(
		ctx, "postgres:18-alpine",
		postgres.WithDatabase("gabon_test"),
		postgres.WithUsername("gabon"),
		postgres.WithPassword("gabon"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).WithStartupTimeout(60*time.Second),
		),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("start postgres container: %w", err)
	}
	terminate := func() { _ = testcontainers.TerminateContainer(container) }

	connStr, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		terminate()
		return nil, nil, fmt.Errorf("connection string: %w", err)
	}
	pool, err = pgxpool.New(ctx, connStr)
	if err != nil {
		terminate()
		return nil, nil, fmt.Errorf("create pool: %w", err)
	}
	if err := db.Migrate(ctx, pool); err != nil {
		pool.Close()
		terminate()
		return nil, nil, fmt.Errorf("migrate: %w", err)
	}
	return pool, func() { pool.Close(); terminate() }, nil
}
