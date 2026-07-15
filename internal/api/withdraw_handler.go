package api

import (
	"net/http"
	"time"
	"unicode/utf8"

	"github.com/netfishx/gabon-go/internal/apierr"
	"github.com/netfishx/gabon-go/internal/db"
	"github.com/netfishx/gabon-go/internal/pagination"
	"github.com/netfishx/gabon-go/internal/withdraw"
)

const (
	withdrawalOrdersDefaultLimit = 20
	withdrawalOrdersMaxLimit     = 100
)

type withdrawalPasswordRequest struct {
	Password string `json:"password"`
}

func (h *Handler) handleWithdrawalPasswordSet(w http.ResponseWriter, r *http.Request) {
	var req withdrawalPasswordRequest
	if !apierr.DecodeJSON(w, r, &req) {
		return
	}
	if req.Password == "" {
		apierr.Write(w, apierr.InvalidArgument("password is required"))
		return
	}
	if utf8.RuneCountInString(req.Password) > 64 {
		apierr.Write(w, apierr.InvalidArgument("password must be at most 64 characters"))
		return
	}
	if err := h.Customers.SetWithdrawalPassword(r.Context(), customerFrom(r.Context()).ID, req.Password); err != nil {
		apierr.Write(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type withdrawalCreateRequest struct {
	Amount             int64  `json:"amount"`
	BankCardID         int64  `json:"bank_card_id"`
	WithdrawalPassword string `json:"withdrawal_password"`
}

type withdrawalCreateResponse struct {
	OrderNo    string    `json:"order_no"`
	Amount     int64     `json:"amount"`
	FiatAmount int64     `json:"fiat_amount"`
	Status     string    `json:"status"`
	CreatedAt  time.Time `json:"created_at"`
}

func (h *Handler) handleWithdrawalCreate(w http.ResponseWriter, r *http.Request) {
	var req withdrawalCreateRequest
	if !apierr.DecodeJSON(w, r, &req) {
		return
	}
	if req.Amount <= 0 {
		apierr.Write(w, apierr.InvalidArgument("amount must be positive"))
		return
	}

	customerID := customerFrom(r.Context()).ID
	if err := h.Customers.CheckWithdrawalPassword(r.Context(), customerID, req.WithdrawalPassword); err != nil {
		apierr.Write(w, err)
		return
	}
	card, err := h.BankCards.GetOwned(r.Context(), customerID, req.BankCardID)
	if err != nil {
		apierr.Write(w, err)
		return
	}
	order, err := h.Withdraws.CreateOrder(r.Context(), customerID, withdraw.CreateParams{
		Amount:     req.Amount,
		BankCardID: card.ID,
		Payee: withdraw.PayeeSnapshot{
			Account: card.CardNo, Name: card.HolderName, Bank: card.BankName,
			BankCode: card.BankCode, Province: card.Province, City: card.City,
		},
	})
	if err != nil {
		apierr.Write(w, err)
		return
	}
	apierr.WriteJSON(w, http.StatusCreated, withdrawalCreateResponse{
		OrderNo: order.OrderNo, Amount: order.Amount, FiatAmount: order.FiatAmount,
		Status: string(order.Status), CreatedAt: order.CreatedAt.Time,
	})
}

type withdrawalOrderItem struct {
	ID           int64     `json:"id"`
	OrderNo      string    `json:"order_no"`
	Amount       int64     `json:"amount"`
	FiatAmount   int64     `json:"fiat_amount"`
	Status       string    `json:"status"`
	RejectReason *string   `json:"reject_reason,omitempty"`
	PayeeAccount string    `json:"payee_account"`
	PayeeName    string    `json:"payee_name"`
	PayeeBank    *string   `json:"payee_bank,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

func (h *Handler) handleWithdrawalList(w http.ResponseWriter, r *http.Request) {
	limit, err := pagination.Limit(r, withdrawalOrdersDefaultLimit, withdrawalOrdersMaxLimit)
	if err != nil {
		apierr.Write(w, err)
		return
	}
	cursor, err := pagination.Cursor(r)
	if err != nil {
		apierr.Write(w, err)
		return
	}
	items, next, err := h.Withdraws.List(r.Context(), customerFrom(r.Context()).ID, cursor, limit)
	if err != nil {
		apierr.Write(w, err)
		return
	}
	out := pagination.Page[withdrawalOrderItem]{Items: make([]withdrawalOrderItem, 0, len(items)), NextCursor: next}
	for _, order := range items {
		out.Items = append(out.Items, toWithdrawalOrderItem(order))
	}
	apierr.WriteJSON(w, http.StatusOK, out)
}

func toWithdrawalOrderItem(order db.WithdrawalOrder) withdrawalOrderItem {
	return withdrawalOrderItem{
		ID: order.ID, OrderNo: order.OrderNo, Amount: order.Amount, FiatAmount: order.FiatAmount,
		Status: string(order.Status), RejectReason: order.RejectReason,
		PayeeAccount: maskBankCardNo(order.PayeeAccount), PayeeName: order.PayeeName,
		PayeeBank: order.PayeeBank, CreatedAt: order.CreatedAt.Time,
	}
}
