package api

import (
	"net/http"
	"time"

	"github.com/netfishx/gabon-go/internal/apierr"
	"github.com/netfishx/gabon-go/internal/db"
	"github.com/netfishx/gabon-go/internal/pagination"
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

func (h *Handler) handleWalletTransactions(w http.ResponseWriter, r *http.Request) {
	limit, err := pagination.Limit(r, transactionsDefaultLimit, transactionsMaxLimit)
	if err != nil {
		apierr.Write(w, err)
		return
	}
	cursor, err := pagination.Cursor(r)
	if err != nil {
		apierr.Write(w, err)
		return
	}

	items, next, err := h.Wallets.ListTransactions(r.Context(), customerFrom(r.Context()).ID, cursor, limit)
	if err != nil {
		apierr.Write(w, err)
		return
	}
	out := pagination.Page[transactionItem]{Items: make([]transactionItem, 0, len(items)), NextCursor: next}
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
