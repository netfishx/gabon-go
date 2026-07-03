package app_test

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/netfishx/gabon-go/internal/app"
	"github.com/netfishx/gabon-go/internal/config"
	"github.com/netfishx/gabon-go/internal/db"
)

// E2E 测试基建：包级共享一个 Postgres 18 容器 + 完整应用 httptest server。
// 这是设计基线约定的唯一主测试缝（httptest 完整 router + 真库）。
var (
	testServer *httptest.Server
	testPool   *pgxpool.Pool
)

func TestMain(m *testing.M) {
	code, err := run(m)
	if err != nil {
		log.Printf("e2e setup: %v", err)
		code = 1
	}
	os.Exit(code)
}

func run(m *testing.M) (int, error) {
	ctx := context.Background()

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
		return 0, fmt.Errorf("start postgres container: %w", err)
	}
	defer func() {
		if err := testcontainers.TerminateContainer(container); err != nil {
			log.Printf("terminate container: %v", err)
		}
	}()

	connStr, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		return 0, fmt.Errorf("connection string: %w", err)
	}

	testPool, err = pgxpool.New(ctx, connStr)
	if err != nil {
		return 0, fmt.Errorf("create pool: %w", err)
	}
	defer testPool.Close()

	if err := db.Migrate(ctx, testPool); err != nil {
		return 0, fmt.Errorf("migrate: %w", err)
	}

	cfg := &config.Config{
		DatabaseURL: connStr,
		JWTSecret:   []byte("test-secret-test-secret-test-secret!"),
		HTTPAddr:    ":0",
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	testServer = httptest.NewServer(app.New(cfg, testPool, logger))
	defer testServer.Close()

	return m.Run(), nil
}
