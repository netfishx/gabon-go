// Package api 客户面 /api/v1 的 handler 与路由。
package api

import (
	"github.com/go-chi/chi/v5"

	"github.com/netfishx/gabon-go/internal/auth"
	"github.com/netfishx/gabon-go/internal/customer"
	"github.com/netfishx/gabon-go/internal/report"
	"github.com/netfishx/gabon-go/internal/video"
	"github.com/netfishx/gabon-go/internal/wallet"
)

// Handler 客户面 /api/v1 的 handler 集。
type Handler struct {
	Customers *customer.Service
	Tokens    *auth.TokenIssuer
	Reports   *report.Service
	Wallets   *wallet.Service
	Videos    *video.Service
}

// Routes 组装客户面路由。
func (h *Handler) Routes() chi.Router {
	r := chi.NewRouter()
	r.Post("/auth/register", h.handleRegister)
	r.Post("/auth/login", h.handleLogin)

	r.Group(func(r chi.Router) {
		r.Use(h.requireCustomer, h.recordActive)
		r.Get("/me", h.handleMe)
		r.Post("/me/password", h.handleChangePassword)
		r.Post("/auth/refresh", h.handleRefresh)
		r.Get("/wallet", h.handleWallet)
		r.Get("/wallet/transactions", h.handleWalletTransactions)
		r.Post("/videos/uploads", h.handleVideoUpload)
		r.Post("/videos", h.handleVideoConfirm)
	})
	return r
}
