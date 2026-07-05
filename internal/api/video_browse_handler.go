package api

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/netfishx/gabon-go/internal/apierr"
	"github.com/netfishx/gabon-go/internal/db"
	"github.com/netfishx/gabon-go/internal/pagination"
)

const (
	browseDefaultLimit = 20
	browseMaxLimit     = 100
)

type feedResponse struct {
	Items      []browseItem `json:"items"`
	Seed       string       `json:"seed"`
	NextCursor string       `json:"next_cursor,omitempty"`
}

type featuredResponse struct {
	Items      []browseItem `json:"items"`
	NextCursor string       `json:"next_cursor,omitempty"`
}

type authorInfo struct {
	PublicID string `json:"public_id"`
	Username string `json:"username"`
}

type browseItem struct {
	PublicID       string     `json:"public_id"`
	Title          string     `json:"title"`
	Tags           []string   `json:"tags"`
	HLSURL         *string    `json:"hls_url"`
	ThumbnailURL   *string    `json:"thumbnail_url"`
	Duration       *int32     `json:"duration"`
	ClickCount     int64      `json:"click_count"`
	ValidPlayCount int64      `json:"valid_play_count"`
	LikeCount      int64      `json:"like_count"`
	CommentCount   int64      `json:"comment_count"`
	Author         authorInfo `json:"author"`
	CreatedAt      time.Time  `json:"created_at"`
}

func (h *Handler) mediaURL(path *string) *string {
	if path == nil {
		return nil
	}
	u := h.CDNBase + "/" + *path
	return &u
}

func (h *Handler) toBrowseItem(v db.Video, authorPublicID, authorUsername string) browseItem {
	return browseItem{
		PublicID:       v.PublicID,
		Title:          v.Title,
		Tags:           v.Tags,
		HLSURL:         h.mediaURL(v.HlsPath),
		ThumbnailURL:   h.mediaURL(v.ThumbnailPath),
		Duration:       v.Duration,
		ClickCount:     v.ClickCount,
		ValidPlayCount: v.ValidPlayCount,
		LikeCount:      v.LikeCount,
		CommentCount:   v.CommentCount,
		Author:         authorInfo{PublicID: authorPublicID, Username: authorUsername},
		CreatedAt:      v.CreatedAt.Time,
	}
}

func (h *Handler) handleFeed(w http.ResponseWriter, r *http.Request) {
	limit, err := pagination.Limit(r, browseDefaultLimit, browseMaxLimit)
	if err != nil {
		apierr.Write(w, err)
		return
	}
	rows, seed, next, err := h.Videos.Feed(r.Context(),
		r.URL.Query().Get("seed"), r.URL.Query().Get("cursor"), limit)
	if err != nil {
		apierr.Write(w, err)
		return
	}
	items := make([]browseItem, 0, len(rows))
	for _, row := range rows {
		items = append(items, h.toBrowseItem(row.Video, row.AuthorPublicID, row.AuthorUsername))
	}
	apierr.WriteJSON(w, http.StatusOK, feedResponse{Items: items, Seed: seed, NextCursor: next})
}

func (h *Handler) handleFeatured(w http.ResponseWriter, r *http.Request) {
	limit, err := pagination.Limit(r, browseDefaultLimit, browseMaxLimit)
	if err != nil {
		apierr.Write(w, err)
		return
	}
	var cursorScore, cursorID int64
	if raw := r.URL.Query().Get("cursor"); raw != "" {
		parts := strings.SplitN(raw, "-", 2)
		var err1, err2 error
		if len(parts) == 2 {
			cursorScore, err1 = strconv.ParseInt(parts[0], 10, 64)
			cursorID, err2 = strconv.ParseInt(parts[1], 10, 64)
		}
		if len(parts) != 2 || err1 != nil || err2 != nil || cursorID <= 0 {
			apierr.Write(w, apierr.InvalidArgument("malformed cursor"))
			return
		}
	}
	rows, nextScore, nextID, err := h.Videos.Featured(r.Context(), cursorScore, cursorID, limit)
	if err != nil {
		apierr.Write(w, err)
		return
	}
	items := make([]browseItem, 0, len(rows))
	for _, row := range rows {
		items = append(items, h.toBrowseItem(row.Video, row.AuthorPublicID, row.AuthorUsername))
	}
	var next string
	if nextID != 0 {
		next = fmt.Sprintf("%d-%d", nextScore, nextID)
	}
	apierr.WriteJSON(w, http.StatusOK, featuredResponse{Items: items, NextCursor: next})
}

func (h *Handler) handleVideoDetail(w http.ResponseWriter, r *http.Request) {
	row, err := h.Videos.DetailPublished(r.Context(), chi.URLParam(r, "publicID"))
	if err != nil {
		apierr.Write(w, err)
		return
	}
	item := h.toBrowseItem(row.Video, row.AuthorPublicID, row.AuthorUsername)
	apierr.WriteJSON(w, http.StatusOK, map[string]any{
		"public_id": item.PublicID, "title": item.Title, "tags": item.Tags,
		"hls_url": item.HLSURL, "thumbnail_url": item.ThumbnailURL,
		"duration": item.Duration, "width": row.Video.Width, "height": row.Video.Height,
		"click_count": item.ClickCount, "valid_play_count": item.ValidPlayCount,
		"like_count": item.LikeCount, "comment_count": item.CommentCount,
		"hot_score": row.Video.HotScore, "author": item.Author, "created_at": item.CreatedAt,
	})
}

func (h *Handler) handleCustomerVideos(w http.ResponseWriter, r *http.Request) {
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
	c, err := h.Customers.GetByPublicID(r.Context(), chi.URLParam(r, "publicID"))
	if err != nil {
		apierr.Write(w, err)
		return
	}
	rows, next, err := h.Videos.PublishedByCustomer(r.Context(), c.ID, cursor, limit)
	if err != nil {
		apierr.Write(w, err)
		return
	}
	items := make([]browseItem, 0, len(rows))
	for _, row := range rows {
		items = append(items, h.toBrowseItem(row.Video, row.AuthorPublicID, row.AuthorUsername))
	}
	apierr.WriteJSON(w, http.StatusOK, pagination.Page[browseItem]{Items: items, NextCursor: next})
}

type myVideoItem struct {
	browseItem
	Status string `json:"status"`
}

func (h *Handler) handleMyVideos(w http.ResponseWriter, r *http.Request) {
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
	c := customerFrom(r.Context())
	videos, next, err := h.Videos.Mine(r.Context(), c.ID, cursor, limit)
	if err != nil {
		apierr.Write(w, err)
		return
	}
	items := make([]myVideoItem, 0, len(videos))
	for _, v := range videos {
		items = append(items, myVideoItem{
			browseItem: h.toBrowseItem(v, c.PublicID, c.Username),
			Status:     string(v.Status),
		})
	}
	apierr.WriteJSON(w, http.StatusOK, pagination.Page[myVideoItem]{Items: items, NextCursor: next})
}

func (h *Handler) handleDeleteVideo(w http.ResponseWriter, r *http.Request) {
	if err := h.Videos.Delete(r.Context(), customerFrom(r.Context()).ID, chi.URLParam(r, "publicID")); err != nil {
		apierr.Write(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
