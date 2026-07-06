package admin

import (
	"context"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/netfishx/gabon-go/internal/apierr"
	"github.com/netfishx/gabon-go/internal/auth"
	"github.com/netfishx/gabon-go/internal/db"
	"github.com/netfishx/gabon-go/internal/task"
	"github.com/netfishx/gabon-go/internal/video"
)

// Handler 后台面 /admin/v1 的 handler 集。
type Handler struct {
	Admins *Service
	Tokens *auth.TokenIssuer
	Videos *video.Service
	Tasks  *task.Service
}

// Routes 组装后台面路由。
func (h *Handler) Routes() chi.Router {
	r := chi.NewRouter()
	r.Post("/auth/login", h.handleLogin)

	r.Group(func(r chi.Router) {
		r.Use(h.requireAdmin)
		r.Get("/me", h.handleMe)
		r.Get("/videos/pending", h.handlePendingVideos)
		r.Post("/videos/{publicID}/approve", h.handleApproveVideo)
		r.Post("/videos/{publicID}/reject", h.handleRejectVideo)

		// 敏感操作仅 ADMIN（checklist M）：任务审核与定义管理挂角色门禁
		r.Group(func(r chi.Router) {
			r.Use(requireRole(db.AdminRoleAdmin))
			r.Get("/claim-tasks/reviews", h.handlePendingClaims)
			r.Post("/claim-tasks/claims/{claimID}/approve", h.handleApproveClaim)
			r.Post("/claim-tasks/claims/{claimID}/reject", h.handleRejectClaim)

			r.Get("/periodic-tasks", h.handleListPeriodicTasks)
			r.Post("/periodic-tasks", h.handleCreatePeriodicTask)
			r.Patch("/periodic-tasks/{id}", h.handleUpdatePeriodicTask)

			r.Get("/claim-tasks", h.handleListClaimTasks)
			r.Post("/claim-tasks", h.handleCreateClaimTask)
			r.Patch("/claim-tasks/{id}", h.handleUpdateClaimTask)
			r.Patch("/claim-tasks/{id}/status", h.handleToggleClaimTaskStatus)
			r.Delete("/claim-tasks/{id}", h.handleDeleteClaimTask)
		})
	})
	return r
}

// requireRole 角色门禁：置于 requireAdmin 之后，主体已注入 context。
func requireRole(role db.AdminRole) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if adminFrom(r.Context()).Role != role {
				apierr.Write(w, apierr.New(http.StatusForbidden, apierr.CodeAdminForbidden, "insufficient admin role"))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

type ctxKey int

const ctxKeyAdmin ctxKey = iota

func adminFrom(ctx context.Context) *db.Admin {
	a, _ := ctx.Value(ctxKeyAdmin).(*db.Admin)
	return a
}

// requireAdmin 后台面鉴权：共享序列见 auth.SubjectMiddleware。
func (h *Handler) requireAdmin(next http.Handler) http.Handler {
	m := auth.SubjectMiddleware[*db.Admin]{
		Tokens:   h.Tokens,
		Audience: auth.AudienceAdmin,
		Load:     h.Admins.GetByID,
		Stamp: func(a *db.Admin) int64 {
			return auth.PasswordStamp(a.PasswordChangedAt.Time)
		},
		Check: func(a *db.Admin) error {
			if a.Status == db.AdminStatusDisabled {
				return apierr.New(http.StatusForbidden, apierr.CodeAdminDisabled, "admin account is disabled")
			}
			return nil
		},
		Inject: func(ctx context.Context, a *db.Admin) context.Context {
			return context.WithValue(ctx, ctxKeyAdmin, a)
		},
	}
	return m.Middleware(next)
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
