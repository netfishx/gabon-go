package app_test

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/netfishx/gabon-go/internal/app"
	"github.com/netfishx/gabon-go/internal/config"
	"github.com/netfishx/gabon-go/internal/testdb"
)

// E2E 测试基建：包级共享一个 Postgres 18 容器（testdb）+ 完整应用 httptest server。
// 这是设计基线约定的唯一主测试缝（httptest 完整 router + 真库）。
var (
	testServer *httptest.Server
	testPool   *pgxpool.Pool
)

const (
	bootstrapAdminUsername = "root"
	bootstrapAdminPassword = "root-secret-1"
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

	pool, cleanup, err := testdb.Start(ctx)
	if err != nil {
		return 0, fmt.Errorf("start testdb: %w", err)
	}
	defer cleanup()
	testPool = pool

	cfg := &config.Config{
		DatabaseURL:   "managed-by-testdb",
		JWTSecret:     []byte("test-secret-test-secret-test-secret!"),
		HTTPAddr:      ":0",
		AdminUsername: bootstrapAdminUsername,
		AdminPassword: bootstrapAdminPassword,
	}
	if err := app.Bootstrap(ctx, cfg, testPool); err != nil {
		return 0, fmt.Errorf("bootstrap: %w", err)
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	testServer = httptest.NewServer(app.New(cfg, testPool, logger))
	defer testServer.Close()

	return m.Run(), nil
}
