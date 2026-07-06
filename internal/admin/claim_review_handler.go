package admin

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/netfishx/gabon-go/internal/apierr"
	"github.com/netfishx/gabon-go/internal/pagination"
)

func claimIDParam(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "claimID"), 10, 64)
	if err != nil || id <= 0 {
		apierr.Write(w, apierr.InvalidArgument("malformed claim id"))
		return 0, false
	}
	return id, true
}

func (h *Handler) handleApproveClaim(w http.ResponseWriter, r *http.Request) {
	claimID, ok := claimIDParam(w, r)
	if !ok {
		return
	}
	if err := h.Tasks.Approve(r.Context(), adminFrom(r.Context()).ID, claimID); err != nil {
		apierr.Write(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type rejectClaimRequest struct {
	Remark string `json:"remark"`
}

func (h *Handler) handleRejectClaim(w http.ResponseWriter, r *http.Request) {
	claimID, ok := claimIDParam(w, r)
	if !ok {
		return
	}
	var req rejectClaimRequest
	if !apierr.DecodeJSON(w, r, &req) {
		return
	}
	if err := h.Tasks.Reject(r.Context(), adminFrom(r.Context()).ID, claimID, req.Remark); err != nil {
		apierr.Write(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

const (
	reviewDefaultLimit = 20
	reviewMaxLimit     = 100
)

type pendingClaimItem struct {
	ClaimID     int64    `json:"claim_id"`
	TaskName    string   `json:"task_name"`
	Requirement *string  `json:"requirement"`
	Reward      int64    `json:"reward"`
	ProofText   *string  `json:"proof_text"`
	ProofImages []string `json:"proof_images"`
}

func (h *Handler) handlePendingClaims(w http.ResponseWriter, r *http.Request) {
	limit, err := pagination.Limit(r, reviewDefaultLimit, reviewMaxLimit)
	if err != nil {
		apierr.Write(w, err)
		return
	}
	cursor, err := pagination.Cursor(r)
	if err != nil {
		apierr.Write(w, err)
		return
	}
	items, next, err := h.Tasks.ListPendingClaims(r.Context(), cursor, limit)
	if err != nil {
		apierr.Write(w, err)
		return
	}
	out := pagination.Page[pendingClaimItem]{Items: make([]pendingClaimItem, 0, len(items)), NextCursor: next}
	for _, it := range items {
		out.Items = append(out.Items, pendingClaimItem{
			ClaimID: it.ID, TaskName: it.TaskName, Requirement: it.Requirement,
			Reward: it.Reward, ProofText: it.ProofText, ProofImages: it.ProofImages,
		})
	}
	apierr.WriteJSON(w, http.StatusOK, out)
}
