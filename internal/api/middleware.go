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

// requireCustomer 解析 Bearer token 并点查客户状态：
// 封禁即时生效；pwd 戳与当前 password_changed_at 不一致（已改密）即失效。
func (h *Handler) requireCustomer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, pwdStamp, err := h.Tokens.FromRequest(r, auth.AudienceCustomer)
		if err != nil {
			apierr.Write(w, apierr.Unauthorized())
			return
		}
		c, err := h.Customers.GetByID(r.Context(), id)
		if err != nil {
			apierr.Write(w, err)
			return
		}
		if pwdStamp != c.PasswordChangedAt.Time.UnixMicro() {
			apierr.Write(w, apierr.Unauthorized())
			return
		}
		if c.Status == db.CustomerStatusBanned {
			apierr.Write(w, apierr.New(http.StatusForbidden, apierr.CodeCustomerBanned, "account is banned"))
			return
		}
		// DAU 打点：旁路数据，失败降级为日志，不阻断请求
		if err := h.Reports.RecordActive(r.Context(), c.ID); err != nil {
			slog.WarnContext(r.Context(), "record daily active failed", "customer_id", c.ID, "error", err)
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKeyCustomer, c)))
	})
}
