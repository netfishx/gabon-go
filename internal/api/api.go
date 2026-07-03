// Package api 客户面 /api/v1 的 handler 与路由。
package api

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/netfishx/gabon-go/internal/apierr"
	"github.com/netfishx/gabon-go/internal/auth"
	"github.com/netfishx/gabon-go/internal/customer"
	"github.com/netfishx/gabon-go/internal/report"
)

type Handler struct {
	Customers *customer.Service
	Tokens    *auth.TokenIssuer
	Reports   *report.Service
}

func (h *Handler) Routes() chi.Router {
	r := chi.NewRouter()
	r.Post("/auth/register", h.handleRegister)
	r.Post("/auth/login", h.handleLogin)

	r.Group(func(r chi.Router) {
		r.Use(h.requireCustomer)
		r.Get("/me", h.handleMe)
		r.Post("/me/password", h.handleChangePassword)
		r.Post("/auth/refresh", h.handleRefresh)
	})
	return r
}

// writeJSON 成功响应：2xx + data 直出（status-first，无 envelope）。
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		apierr.Write(w, apierr.InvalidArgument("malformed JSON body"))
		return false
	}
	return true
}
