package api

import (
	"net/http"

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
