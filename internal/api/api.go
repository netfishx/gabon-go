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
	CDNBase   string
}

// Routes 组装客户面路由。
func (h *Handler) Routes() chi.Router {
	r := chi.NewRouter()
	r.Post("/auth/register", h.handleRegister)
	r.Post("/auth/login", h.handleLogin)

	// 公开浏览面：Feed/精选/详情/他人主页
	r.Get("/feed", h.handleFeed)
	r.Get("/featured", h.handleFeatured)
	r.Get("/videos/{publicID}", h.handleVideoDetail)
	r.Get("/videos/{publicID}/comments", h.handleComments)
	r.Get("/customers/{publicID}/videos", h.handleCustomerVideos)

	r.Group(func(r chi.Router) {
		r.Use(h.requireCustomer, h.recordActive)
		r.Get("/me", h.handleMe)
		r.Patch("/me/profile", h.handleUpdateProfile)
		r.Post("/me/password", h.handleChangePassword)
		r.Post("/auth/refresh", h.handleRefresh)
		r.Get("/wallet", h.handleWallet)
		r.Get("/wallet/transactions", h.handleWalletTransactions)
		r.Post("/videos/uploads", h.handleVideoUpload)
		r.Post("/videos", h.handleVideoConfirm)
		r.Get("/me/videos", h.handleMyVideos)
		r.Delete("/videos/{publicID}", h.handleDeleteVideo)
		r.Post("/videos/{publicID}/like", h.handleLike)
		r.Delete("/videos/{publicID}/like", h.handleUnlike)
		r.Post("/videos/{publicID}/comments", h.handleComment)
		r.Delete("/comments/{commentID}", h.handleDeleteComment)
		r.Post("/videos/{publicID}/plays", h.handlePlay)
		r.Post("/plays/{playID}/valid", h.handleValidPlay)
	})
	return r
}
