package api

import (
	"net/http"

	"github.com/netfishx/gabon-go/internal/apierr"
)

func (h *Handler) handleSignIn(w http.ResponseWriter, r *http.Request) {
	if err := h.SignIns.SignIn(r.Context(), customerFrom(r.Context()).ID); err != nil {
		apierr.Write(w, err)
		return
	}
	w.WriteHeader(http.StatusCreated)
}

type signInStatusResponse struct {
	SignedDays    int64  `json:"signed_days"`
	TodaySigned   bool   `json:"today_signed"`
	NextMilestone *int32 `json:"next_milestone"` // 下一里程碑档位（null = 无更高档位）
}

func (h *Handler) handleSignInStatus(w http.ResponseWriter, r *http.Request) {
	st, err := h.SignIns.Status(r.Context(), customerFrom(r.Context()).ID)
	if err != nil {
		apierr.Write(w, err)
		return
	}
	apierr.WriteJSON(w, http.StatusOK, signInStatusResponse{
		SignedDays: st.SignedDays, TodaySigned: st.TodaySigned, NextMilestone: st.NextMilestone,
	})
}
