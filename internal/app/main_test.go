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

	"github.com/netfishx/gabon-go/internal/app"
	"github.com/netfishx/gabon-go/internal/config"
	"github.com/netfishx/gabon-go/internal/storage"
	"github.com/netfishx/gabon-go/internal/testdb"
)

// E2E 测试基建：包级共享一个 Postgres 18 容器（testdb）+ 完整应用 httptest server。
// 这是设计基线约定的唯一主测试缝（httptest 完整 router + 真库）。
var (
	testServer *httptest.Server
	testPool   *pgxpool.Pool
	testStore  *storage.Store
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

	mio, cleanupMinio, err := testdb.StartMinIO(ctx)
	if err != nil {
		return 0, fmt.Errorf("start minio: %w", err)
	}
	defer cleanupMinio()

	cfg := &config.Config{
		DatabaseURL:   "managed-by-testdb",
		JWTSecret:     []byte("test-secret-test-secret-test-secret!"),
		HTTPAddr:      ":0",
		AdminUsername: bootstrapAdminUsername,
		AdminPassword: bootstrapAdminPassword,
		S3Endpoint:    mio.Endpoint,
		S3AccessKey:   mio.AccessKey,
		S3SecretKey:   mio.SecretKey,
		S3Bucket:      "gabon-test",
		CDNBaseURL:    "http://" + mio.Endpoint + "/gabon-test",

		PaymentEnableMock: true, // E2E 全链路需要 mock 渠道；生产默认 false
		RechargeTimeout:   10 * time.Minute,
		RechargeSweepSpec: "*/5 * * * *",

		TranscodeWorkers: 2,
		TranscodeTimeout: 60 * time.Second,
	}
	store, err := storage.New(storage.Config{
		Endpoint: cfg.S3Endpoint, AccessKey: cfg.S3AccessKey,
		SecretKey: cfg.S3SecretKey, Bucket: cfg.S3Bucket,
	})
	if err != nil {
		return 0, fmt.Errorf("storage client: %w", err)
	}
	if err := store.EnsureBucket(ctx); err != nil {
		return 0, fmt.Errorf("ensure bucket: %w", err)
	}
	testStore = store

	if err := app.Bootstrap(ctx, cfg, testPool); err != nil {
		return 0, fmt.Errorf("bootstrap: %w", err)
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	a, err := app.New(cfg, testPool, logger)
	if err != nil {
		return 0, fmt.Errorf("assemble app: %w", err)
	}
	if err := a.Start(ctx); err != nil {
		return 0, fmt.Errorf("start app: %w", err)
	}
	defer a.Stop()
	testServer = httptest.NewServer(a.Handler)
	defer testServer.Close()

	return m.Run(), nil
}
