package api

import (
	"net/http"
	"time"

	"github.com/netfishx/gabon-go/internal/apierr"
	"github.com/netfishx/gabon-go/internal/db"
	"github.com/netfishx/gabon-go/internal/pagination"
)

const (
	rechargeOrdersDefaultLimit = 20
	rechargeOrdersMaxLimit     = 100
)

type rechargeCreateRequest struct {
	FiatAmount    int64  `json:"fiat_amount"` // 法币，分单位
	PaymentMethod string `json:"payment_method"`
}

type rechargeOrderResponse struct {
	OrderNo     string    `json:"order_no"`
	Amount      int64     `json:"amount"`      // 钻石
	FiatAmount  int64     `json:"fiat_amount"` // 分
	Status      string    `json:"status"`
	RedirectURL string    `json:"redirect_url,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

func (h *Handler) handleRechargeCreate(w http.ResponseWriter, r *http.Request) {
	var req rechargeCreateRequest
	if !apierr.DecodeJSON(w, r, &req) {
		return
	}
	if req.FiatAmount <= 0 {
		apierr.Write(w, apierr.InvalidArgument("fiat_amount must be a positive integer (cents)"))
		return
	}
	if req.PaymentMethod == "" {
		apierr.Write(w, apierr.InvalidArgument("payment_method is required"))
		return
	}

	order, redirect, err := h.Payments.CreateRechargeOrder(
		r.Context(), customerFrom(r.Context()).ID, req.FiatAmount, req.PaymentMethod)
	if err != nil {
		apierr.Write(w, err)
		return
	}
	apierr.WriteJSON(w, http.StatusCreated, rechargeOrderResponse{
		OrderNo:     order.OrderNo,
		Amount:      order.Amount,
		FiatAmount:  order.FiatAmount,
		Status:      string(order.Status),
		RedirectURL: redirect,
		CreatedAt:   order.CreatedAt.Time,
	})
}

type rechargeOrderItem struct {
	ID         int64     `json:"id"`
	OrderNo    string    `json:"order_no"`
	Amount     int64     `json:"amount"`
	FiatAmount int64     `json:"fiat_amount"`
	Status     string    `json:"status"`
	CreatedAt  time.Time `json:"created_at"`
}

func (h *Handler) handleRechargeList(w http.ResponseWriter, r *http.Request) {
	limit, err := pagination.Limit(r, rechargeOrdersDefaultLimit, rechargeOrdersMaxLimit)
	if err != nil {
		apierr.Write(w, err)
		return
	}
	cursor, err := pagination.Cursor(r)
	if err != nil {
		apierr.Write(w, err)
		return
	}

	items, next, err := h.Payments.ListRechargeOrders(r.Context(), customerFrom(r.Context()).ID, cursor, limit)
	if err != nil {
		apierr.Write(w, err)
		return
	}
	out := pagination.Page[rechargeOrderItem]{Items: make([]rechargeOrderItem, 0, len(items)), NextCursor: next}
	for _, o := range items {
		out.Items = append(out.Items, toRechargeOrderItem(o))
	}
	apierr.WriteJSON(w, http.StatusOK, out)
}

func toRechargeOrderItem(o db.RechargeOrder) rechargeOrderItem {
	return rechargeOrderItem{
		ID:         o.ID,
		OrderNo:    o.OrderNo,
		Amount:     o.Amount,
		FiatAmount: o.FiatAmount,
		Status:     string(o.Status),
		CreatedAt:  o.CreatedAt.Time,
	}
}
