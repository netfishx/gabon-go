package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/netfishx/gabon-go/internal/apierr"
	"github.com/netfishx/gabon-go/internal/auth"
	"github.com/netfishx/gabon-go/internal/db"
)

type Handler struct {
	Admins *Service
	Tokens *auth.TokenIssuer
}

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

func (h *Handler) requireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || raw == "" {
			apierr.Write(w, apierr.New(http.StatusUnauthorized, apierr.CodeAuthUnauthorized, "authentication required"))
			return
		}
		id, pwdStamp, err := h.Tokens.Parse(raw, auth.AudienceAdmin)
		if err != nil {
			apierr.Write(w, apierr.New(http.StatusUnauthorized, apierr.CodeAuthUnauthorized, "authentication required"))
			return
		}
		a, err := h.Admins.GetByID(r.Context(), id)
		if err != nil {
			apierr.Write(w, err)
			return
		}
		if pwdStamp != a.PasswordChangedAt.Time.UnixMicro() {
			apierr.Write(w, apierr.New(http.StatusUnauthorized, apierr.CodeAuthUnauthorized, "authentication required"))
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

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (h *Handler) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		apierr.Write(w, apierr.InvalidArgument("malformed JSON body"))
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
	writeJSON(w, http.StatusOK, map[string]any{"token": token, "admin": toAdminResponse(a)})
}

func (h *Handler) handleMe(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, toAdminResponse(adminFrom(r.Context())))
}
