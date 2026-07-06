package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/netfishx/gabon-go/internal/apierr"
	"github.com/netfishx/gabon-go/internal/db"
	"github.com/netfishx/gabon-go/internal/pagination"
)

func (h *Handler) handleLike(w http.ResponseWriter, r *http.Request) {
	c := customerFrom(r.Context())
	counted, err := h.Videos.Like(r.Context(), c.ID, chi.URLParam(r, "publicID"))
	if err != nil {
		apierr.Write(w, err)
		return
	}
	if counted {
		h.advanceTask(r.Context(), c.ID, db.TaskCategoryLike, 0)
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) handleUnlike(w http.ResponseWriter, r *http.Request) {
	if err := h.Videos.Unlike(r.Context(), customerFrom(r.Context()).ID, chi.URLParam(r, "publicID")); err != nil {
		apierr.Write(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type commentRequest struct {
	Content string `json:"content"`
}

type commentItem struct {
	ID        int64      `json:"id"`
	Content   string     `json:"content"`
	Author    authorInfo `json:"author"`
	CreatedAt time.Time  `json:"created_at"`
}

func (h *Handler) handleComment(w http.ResponseWriter, r *http.Request) {
	var req commentRequest
	if !apierr.DecodeJSON(w, r, &req) {
		return
	}
	c := customerFrom(r.Context())
	created, err := h.Videos.Comment(r.Context(), c.ID, chi.URLParam(r, "publicID"), req.Content)
	if err != nil {
		apierr.Write(w, err)
		return
	}
	h.advanceTask(r.Context(), c.ID, db.TaskCategoryComment, 0)
	apierr.WriteJSON(w, http.StatusCreated, commentItem{
		ID:        created.ID,
		Content:   created.Content,
		Author:    authorInfo{PublicID: c.PublicID, Username: c.Username},
		CreatedAt: created.CreatedAt.Time,
	})
}

func (h *Handler) handleDeleteComment(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "commentID"), 10, 64)
	if err != nil || id <= 0 {
		apierr.Write(w, apierr.InvalidArgument("malformed comment id"))
		return
	}
	if err := h.Videos.DeleteComment(r.Context(), customerFrom(r.Context()).ID, id); err != nil {
		apierr.Write(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) handleComments(w http.ResponseWriter, r *http.Request) {
	limit, err := pagination.Limit(r, browseDefaultLimit, browseMaxLimit)
	if err != nil {
		apierr.Write(w, err)
		return
	}
	cursor, err := pagination.Cursor(r)
	if err != nil {
		apierr.Write(w, err)
		return
	}
	rows, next, err := h.Videos.Comments(r.Context(), chi.URLParam(r, "publicID"), cursor, limit)
	if err != nil {
		apierr.Write(w, err)
		return
	}
	items := make([]commentItem, 0, len(rows))
	for _, row := range rows {
		items = append(items, commentItem{
			ID:        row.ID,
			Content:   row.Content,
			Author:    authorInfo{PublicID: row.AuthorPublicID, Username: row.AuthorUsername},
			CreatedAt: row.CreatedAt.Time,
		})
	}
	apierr.WriteJSON(w, http.StatusOK, pagination.Page[commentItem]{Items: items, NextCursor: next})
}

func (h *Handler) handlePlay(w http.ResponseWriter, r *http.Request) {
	playID, err := h.Videos.Play(r.Context(), customerFrom(r.Context()).ID, chi.URLParam(r, "publicID"))
	if err != nil {
		apierr.Write(w, err)
		return
	}
	apierr.WriteJSON(w, http.StatusCreated, map[string]int64{"play_id": playID})
}

func (h *Handler) handleValidPlay(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "playID"), 10, 64)
	if err != nil || id <= 0 {
		apierr.Write(w, apierr.InvalidArgument("malformed play id"))
		return
	}
	c := customerFrom(r.Context())
	marked, err := h.Videos.MarkValid(r.Context(), c.ID, id)
	if err != nil {
		apierr.Write(w, err)
		return
	}
	if marked {
		h.advanceTask(r.Context(), c.ID, db.TaskCategoryWatchVideo, id)
	}
	w.WriteHeader(http.StatusNoContent)
}
