// Package app 装配整个 HTTP 服务：中间件栈 + /api/v1 与 /admin/v1 两个面。
// main 与 httptest E2E 共用这一个入口。
package app

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/netfishx/gabon-go/internal/admin"
	"github.com/netfishx/gabon-go/internal/api"
	"github.com/netfishx/gabon-go/internal/auth"
	"github.com/netfishx/gabon-go/internal/config"
	"github.com/netfishx/gabon-go/internal/customer"
	"github.com/netfishx/gabon-go/internal/report"
	"github.com/netfishx/gabon-go/internal/wallet"
)

// Bootstrap 一次性启动动作：admins 表为空时创建初始管理员。迁移之后、服务启动之前调用。
func Bootstrap(ctx context.Context, cfg *config.Config, pool *pgxpool.Pool) error {
	return admin.NewService(pool).Bootstrap(ctx, cfg.AdminUsername, cfg.AdminPassword)
}

// New 装配完整 HTTP handler；main 与 httptest E2E 共用。
func New(cfg *config.Config, pool *pgxpool.Pool, logger *slog.Logger) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(requestLogger(logger))
	r.Use(middleware.Recoverer)

	tokens := auth.NewTokenIssuer(cfg.JWTSecret)

	apiHandler := &api.Handler{
		Customers: customer.NewService(pool),
		Tokens:    tokens,
		Reports:   report.NewService(pool),
		Wallets:   wallet.NewService(pool),
	}
	r.Mount("/api/v1", apiHandler.Routes())

	adminHandler := &admin.Handler{
		Admins: admin.NewService(pool),
		Tokens: tokens,
	}
	r.Mount("/admin/v1", adminHandler.Routes())

	return r
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
