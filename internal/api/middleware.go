package api

import (
	"context"
	"net/http"
	"strings"

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

func unauthorized(w http.ResponseWriter) {
	apierr.Write(w, apierr.New(http.StatusUnauthorized, apierr.CodeAuthUnauthorized, "authentication required"))
}

// requireCustomer 解析 Bearer token 并点查客户状态：
// 封禁即时生效；pwd 戳与当前 password_changed_at 不一致（已改密）即失效。
func (h *Handler) requireCustomer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || raw == "" {
			unauthorized(w)
			return
		}
		id, pwdStamp, err := h.Tokens.Parse(raw, auth.AudienceCustomer)
		if err != nil {
			unauthorized(w)
			return
		}
		c, err := h.Customers.GetByID(r.Context(), id)
		if err != nil {
			apierr.Write(w, err)
			return
		}
		if pwdStamp != c.PasswordChangedAt.Time.UnixMicro() {
			unauthorized(w)
			return
		}
		if c.Status == db.CustomerStatusBanned {
			apierr.Write(w, apierr.New(http.StatusForbidden, apierr.CodeCustomerBanned, "account is banned"))
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKeyCustomer, c)))
	})
}
