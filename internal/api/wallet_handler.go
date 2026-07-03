package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/netfishx/gabon-go/internal/apierr"
	"github.com/netfishx/gabon-go/internal/db"
)

type walletResponse struct {
	Available int64 `json:"available"`
	Frozen    int64 `json:"frozen"`
}

func toWalletResponse(w *db.Wallet) walletResponse {
	return walletResponse{Available: w.Available, Frozen: w.Frozen}
}

func (h *Handler) handleWallet(w http.ResponseWriter, r *http.Request) {
	wal, err := h.Wallets.Get(r.Context(), customerFrom(r.Context()).ID)
	if err != nil {
		apierr.Write(w, err)
		return
	}
	apierr.WriteJSON(w, http.StatusOK, toWalletResponse(wal))
}

const (
	transactionsDefaultLimit = 20
	transactionsMaxLimit     = 100
)

type transactionItem struct {
	ID           int64     `json:"id"`
	Type         string    `json:"type"`
	Amount       int64     `json:"amount"`
	BalanceAfter int64     `json:"balance_after"`
	CreatedAt    time.Time `json:"created_at"`
}

type transactionsResponse struct {
	Items      []transactionItem `json:"items"`
	NextCursor int64             `json:"next_cursor,omitempty"`
}

func (h *Handler) handleWalletTransactions(w http.ResponseWriter, r *http.Request) {
	limit := int32(transactionsDefaultLimit)
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.ParseInt(raw, 10, 32)
		if err != nil || n <= 0 {
			apierr.Write(w, apierr.InvalidArgument("limit must be a positive integer"))
			return
		}
		limit = int32(min(n, transactionsMaxLimit))
	}
	var cursor int64
	if raw := r.URL.Query().Get("cursor"); raw != "" {
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || n < 0 {
			apierr.Write(w, apierr.InvalidArgument("cursor must be a non-negative integer"))
			return
		}
		cursor = n
	}

	items, next, err := h.Wallets.ListTransactions(r.Context(), customerFrom(r.Context()).ID, cursor, limit)
	if err != nil {
		apierr.Write(w, err)
		return
	}
	out := transactionsResponse{Items: make([]transactionItem, 0, len(items)), NextCursor: next}
	for _, tx := range items {
		out.Items = append(out.Items, transactionItem{
			ID:           tx.ID,
			Type:         string(tx.Type),
			Amount:       tx.Amount,
			BalanceAfter: tx.BalanceAfter,
			CreatedAt:    tx.CreatedAt.Time,
		})
	}
	apierr.WriteJSON(w, http.StatusOK, out)
}
