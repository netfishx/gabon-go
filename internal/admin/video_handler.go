package admin

import (
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/netfishx/gabon-go/internal/apierr"
	"github.com/netfishx/gabon-go/internal/db"
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

type pendingVideosResponse struct {
	Items      []pendingVideoItem `json:"items"`
	NextCursor int64              `json:"next_cursor,omitempty"`
}

func (h *Handler) handlePendingVideos(w http.ResponseWriter, r *http.Request) {
	limit := int32(pendingDefaultLimit)
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.ParseInt(raw, 10, 32)
		if err != nil || n <= 0 {
			apierr.Write(w, apierr.InvalidArgument("limit must be a positive integer"))
			return
		}
		limit = int32(min(n, pendingMaxLimit))
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

	items, next, err := h.Videos.ListPendingReview(r.Context(), cursor, limit)
	if err != nil {
		apierr.Write(w, err)
		return
	}
	out := pendingVideosResponse{Items: make([]pendingVideoItem, 0, len(items)), NextCursor: next}
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
