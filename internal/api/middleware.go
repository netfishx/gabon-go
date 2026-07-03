package api

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/netfishx/gabon-go/internal/apierr"
	"github.com/netfishx/gabon-go/internal/auth"
	"github.com/netfishx/gabon-go/internal/db"
)

type ctxKey int

const ctxKeyCustomer ctxKey = iota

func customerFrom(ctx context.Context) *db.Customer {
	c, _ := ctx.Value(ctxKeyCustomer).(*db.Customer)
	return c
}

// requireCustomer 客户面鉴权：共享序列见 auth.SubjectMiddleware。
func (h *Handler) requireCustomer(next http.Handler) http.Handler {
	m := auth.SubjectMiddleware[*db.Customer]{
		Tokens:   h.Tokens,
		Audience: auth.AudienceCustomer,
		Load:     h.Customers.GetByID,
		Stamp: func(c *db.Customer) int64 {
			return auth.PasswordStamp(c.PasswordChangedAt.Time)
		},
		Check: func(c *db.Customer) error {
			if c.Status == db.CustomerStatusBanned {
				return apierr.New(http.StatusForbidden, apierr.CodeCustomerBanned, "account is banned")
			}
			return nil
		},
		Inject: func(ctx context.Context, c *db.Customer) context.Context {
			return context.WithValue(ctx, ctxKeyCustomer, c)
		},
	}
	return m.Middleware(next)
}

// recordActive DAU 打点：独立于鉴权的旁路中间件，失败降级为日志，不阻断请求。
func (h *Handler) recordActive(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if c := customerFrom(r.Context()); c != nil {
			if err := h.Reports.RecordActive(r.Context(), c.ID); err != nil {
				slog.WarnContext(r.Context(), "record daily active failed", "customer_id", c.ID, "error", err)
			}
		}
		next.ServeHTTP(w, r)
	})
}
