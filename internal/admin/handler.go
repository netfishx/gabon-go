package admin

import (
	"context"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/netfishx/gabon-go/internal/apierr"
	"github.com/netfishx/gabon-go/internal/auth"
	"github.com/netfishx/gabon-go/internal/db"
)

// Handler 后台面 /admin/v1 的 handler 集。
type Handler struct {
	Admins *Service
	Tokens *auth.TokenIssuer
}

// Routes 组装后台面路由。
func (h *Handler) Routes() chi.Router {
	r := chi.NewRouter()
	r.Post("/auth/login", h.handleLogin)

	r.Group(func(r chi.Router) {
		r.Use(h.requireAdmin)
		r.Get("/me", h.handleMe)
	})
	return r
}

type ctxKey int

const ctxKeyAdmin ctxKey = iota

func adminFrom(ctx context.Context) *db.Admin {
	a, _ := ctx.Value(ctxKeyAdmin).(*db.Admin)
	return a
}

// requireAdmin 与客户面同构：Bearer 解析（共用 TokenIssuer.FromRequest）+ 主体状态点查 + pwd 戳校验。
func (h *Handler) requireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, pwdStamp, err := h.Tokens.FromRequest(r, auth.AudienceAdmin)
		if err != nil {
			apierr.Write(w, apierr.Unauthorized())
			return
		}
		a, err := h.Admins.GetByID(r.Context(), id)
		if err != nil {
			apierr.Write(w, err)
			return
		}
		if pwdStamp != a.PasswordChangedAt.Time.UnixMicro() {
			apierr.Write(w, apierr.Unauthorized())
			return
		}
		if a.Status == db.AdminStatusDisabled {
			apierr.Write(w, apierr.New(http.StatusForbidden, apierr.CodeAdminDisabled, "admin account is disabled"))
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKeyAdmin, a)))
	})
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type adminResponse struct {
	Username  string    `json:"username"`
	Role      string    `json:"role"`
	CreatedAt time.Time `json:"created_at"`
}

func toAdminResponse(a *db.Admin) adminResponse {
	return adminResponse{Username: a.Username, Role: string(a.Role), CreatedAt: a.CreatedAt.Time}
}

func (h *Handler) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if !apierr.DecodeJSON(w, r, &req) {
		return
	}
	a, err := h.Admins.Login(r.Context(), req.Username, req.Password)
	if err != nil {
		apierr.Write(w, err)
		return
	}
	token, err := h.Tokens.Issue(a.ID, auth.AudienceAdmin, a.PasswordChangedAt.Time)
	if err != nil {
		apierr.Write(w, err)
		return
	}
	apierr.WriteJSON(w, http.StatusOK, map[string]any{"token": token, "admin": toAdminResponse(a)})
}

func (h *Handler) handleMe(w http.ResponseWriter, r *http.Request) {
	apierr.WriteJSON(w, http.StatusOK, toAdminResponse(adminFrom(r.Context())))
}
