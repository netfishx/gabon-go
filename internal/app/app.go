// Package app 装配整个 HTTP 服务：中间件栈 + /api/v1 与 /admin/v1 两个面。
// main 与 httptest E2E 共用这一个入口。
package app

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/netfishx/gabon-go/internal/admin"
	"github.com/netfishx/gabon-go/internal/api"
	"github.com/netfishx/gabon-go/internal/auth"
	"github.com/netfishx/gabon-go/internal/config"
	"github.com/netfishx/gabon-go/internal/cron"
	"github.com/netfishx/gabon-go/internal/customer"
	"github.com/netfishx/gabon-go/internal/report"
	"github.com/netfishx/gabon-go/internal/storage"
	"github.com/netfishx/gabon-go/internal/task"
	"github.com/netfishx/gabon-go/internal/transcode"
	"github.com/netfishx/gabon-go/internal/video"
	"github.com/netfishx/gabon-go/internal/wallet"
)

// Bootstrap 一次性启动动作：admins 表为空时创建初始管理员。迁移之后、服务启动之前调用。
func Bootstrap(ctx context.Context, cfg *config.Config, pool *pgxpool.Pool) error {
	return admin.NewService(pool).Bootstrap(ctx, cfg.AdminUsername, cfg.AdminPassword)
}

// App 装配完成的应用：HTTP handler 与后台组件。
type App struct {
	Handler http.Handler

	transcoder *transcode.Worker
	scheduler  *cron.Scheduler
}

// Start 启动后台组件（转码 worker 池 + cron 调度器；后者启动即跑一次做幂等 catch-up）。
func (a *App) Start(ctx context.Context) error {
	if err := a.transcoder.Start(ctx); err != nil {
		return err
	}
	a.scheduler.Start(ctx)
	return nil
}

// Stop 停止后台组件并等待在途任务退出（逆序：先停调度再停 worker）。
func (a *App) Stop() {
	a.scheduler.Stop()
	a.transcoder.Stop()
}

// New 装配完整应用；main 与 httptest E2E 共用。
func New(cfg *config.Config, pool *pgxpool.Pool, logger *slog.Logger) (*App, error) {
	store, err := storage.New(storage.Config{
		Endpoint:  cfg.S3Endpoint,
		AccessKey: cfg.S3AccessKey,
		SecretKey: cfg.S3SecretKey,
		Bucket:    cfg.S3Bucket,
		UseSSL:    cfg.S3UseSSL,
	})
	if err != nil {
		return nil, err
	}

	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(requestLogger(logger))
	r.Use(middleware.Recoverer)

	tokens := auth.NewTokenIssuer(cfg.JWTSecret)

	wallets := wallet.NewService(pool)
	customers := customer.NewService(pool, wallets)
	tasks := task.NewService(pool, wallets)
	videoSvc := video.NewService(pool, store)
	// 有效用户判定挂视频审核通过处（同事务）；依赖方向约束（video ↛ customer）以回调解耦
	videoSvc.OnApproved = func(ctx context.Context, tx pgx.Tx, authorID int64) error {
		_, err := customers.MarkValidIfQualifiedTx(ctx, tx, authorID)
		return err
	}

	apiHandler := &api.Handler{
		Customers: customers,
		Tokens:    tokens,
		Reports:   report.NewService(pool),
		Wallets:   wallets,
		Videos:    videoSvc,
		Tasks:     tasks,
		Store:     store,
		CDNBase:   cfg.CDNBaseURL,
	}
	r.Mount("/api/v1", apiHandler.Routes())

	adminHandler := &admin.Handler{
		Admins: admin.NewService(pool),
		Tokens: tokens,
		Videos: videoSvc,
		Tasks:  tasks,
	}
	r.Mount("/admin/v1", adminHandler.Routes())

	worker := transcode.NewWorker(pool, transcode.Options{
		Transcode:   transcode.NewFFmpeg(store),
		Concurrency: cfg.TranscodeWorkers,
		MaxAttempts: 3,
		JobTimeout:  cfg.TranscodeTimeout,
	})

	scheduler := cron.New(logger)
	// 限时任务过期每 5 分钟（M5 唯一 job；M7 周/月榜复用同基建）
	if err := scheduler.Register(cron.Job{
		Name: "expire_claims", Spec: "*/5 * * * *",
		Run: func(ctx context.Context) error {
			n, err := tasks.ExpireClaims(ctx)
			if err == nil && n > 0 {
				logger.Info("expired claim tasks", "count", n)
			}
			return err
		},
	}); err != nil {
		return nil, err
	}

	return &App{Handler: r, transcoder: worker, scheduler: scheduler}, nil
}

func requestLogger(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ww := middleware.NewWrapResponseWriter(w, req.ProtoMajor)
			next.ServeHTTP(ww, req)
			logger.Info(
				"http_request",
				"method", req.Method,
				"path", req.URL.Path,
				"status", ww.Status(),
				"bytes", ww.BytesWritten(),
				"request_id", middleware.GetReqID(req.Context()),
			)
		})
	}
}
