package api

import (
	"net/http"
	"time"

	"github.com/netfishx/gabon-go/internal/apierr"
	"github.com/netfishx/gabon-go/internal/db"
)

type uploadResponse struct {
	StoragePath string `json:"storage_path"`
	UploadURL   string `json:"upload_url"`
}

func (h *Handler) handleVideoUpload(w http.ResponseWriter, r *http.Request) {
	path, url, err := h.Videos.CreateUpload(r.Context(), customerFrom(r.Context()).ID)
	if err != nil {
		apierr.Write(w, err)
		return
	}
	apierr.WriteJSON(w, http.StatusCreated, uploadResponse{StoragePath: path, UploadURL: url})
}

type confirmVideoRequest struct {
	StoragePath string   `json:"storage_path"`
	Title       string   `json:"title"`
	Tags        []string `json:"tags"`
}

type videoResponse struct {
	PublicID  string    `json:"public_id"`
	Title     string    `json:"title"`
	Tags      []string  `json:"tags"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

func toVideoResponse(v *db.Video) videoResponse {
	return videoResponse{
		PublicID:  v.PublicID,
		Title:     v.Title,
		Tags:      v.Tags,
		Status:    string(v.Status),
		CreatedAt: v.CreatedAt.Time,
	}
}

func (h *Handler) handleVideoConfirm(w http.ResponseWriter, r *http.Request) {
	var req confirmVideoRequest
	if !apierr.DecodeJSON(w, r, &req) {
		return
	}
	v, err := h.Videos.Confirm(r.Context(), customerFrom(r.Context()).ID, req.StoragePath, req.Title, req.Tags)
	if err != nil {
		apierr.Write(w, err)
		return
	}
	apierr.WriteJSON(w, http.StatusCreated, toVideoResponse(v))
}
