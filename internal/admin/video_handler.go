package admin

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/netfishx/gabon-go/internal/apierr"
	"github.com/netfishx/gabon-go/internal/db"
	"github.com/netfishx/gabon-go/internal/pagination"
)

const (
	pendingDefaultLimit = 20
	pendingMaxLimit     = 100
)

type pendingVideoItem struct {
	PublicID  string    `json:"public_id"`
	Title     string    `json:"title"`
	Tags      []string  `json:"tags"`
	CreatedAt time.Time `json:"created_at"`
}

func (h *Handler) handlePendingVideos(w http.ResponseWriter, r *http.Request) {
	limit, err := pagination.Limit(r, pendingDefaultLimit, pendingMaxLimit)
	if err != nil {
		apierr.Write(w, err)
		return
	}
	cursor, err := pagination.Cursor(r)
	if err != nil {
		apierr.Write(w, err)
		return
	}

	items, next, err := h.Videos.ListPendingReview(r.Context(), cursor, limit)
	if err != nil {
		apierr.Write(w, err)
		return
	}
	out := pagination.Page[pendingVideoItem]{Items: make([]pendingVideoItem, 0, len(items)), NextCursor: next}
	for _, v := range items {
		out.Items = append(out.Items, toPendingVideoItem(v))
	}
	apierr.WriteJSON(w, http.StatusOK, out)
}

func toPendingVideoItem(v db.Video) pendingVideoItem {
	return pendingVideoItem{
		PublicID:  v.PublicID,
		Title:     v.Title,
		Tags:      v.Tags,
		CreatedAt: v.CreatedAt.Time,
	}
}

func (h *Handler) handleApproveVideo(w http.ResponseWriter, r *http.Request) {
	if err := h.Videos.Approve(r.Context(), adminFrom(r.Context()).ID, chi.URLParam(r, "publicID")); err != nil {
		apierr.Write(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type rejectVideoRequest struct {
	Reason string `json:"reason"`
}

func (h *Handler) handleRejectVideo(w http.ResponseWriter, r *http.Request) {
	var req rejectVideoRequest
	if !apierr.DecodeJSON(w, r, &req) {
		return
	}
	if err := h.Videos.Reject(r.Context(), adminFrom(r.Context()).ID, chi.URLParam(r, "publicID"), req.Reason); err != nil {
		apierr.Write(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
