package admin

import (
	"net/http"

	"github.com/netfishx/gabon-go/internal/apierr"
	"github.com/netfishx/gabon-go/internal/db"
)

// ---- 广告商 ----

type createAdvertiserRequest struct {
	Name    string  `json:"name"`
	Contact *string `json:"contact"`
}

func (h *Handler) handleCreateAdvertiser(w http.ResponseWriter, r *http.Request) {
	var req createAdvertiserRequest
	if !apierr.DecodeJSON(w, r, &req) {
		return
	}
	if req.Name == "" {
		apierr.Write(w, apierr.InvalidArgument("name is required"))
		return
	}
	a, err := h.Ads.CreateAdvertiser(r.Context(), req.Name, req.Contact)
	if err != nil {
		apierr.Write(w, err)
		return
	}
	apierr.WriteJSON(w, http.StatusCreated, map[string]int64{"id": a.ID})
}

type advertiserItem struct {
	ID      int64   `json:"id"`
	Name    string  `json:"name"`
	Contact *string `json:"contact"`
	Status  string  `json:"status"`
}

func (h *Handler) handleListAdvertisers(w http.ResponseWriter, r *http.Request) {
	rows, err := h.Ads.ListAdvertisers(r.Context())
	if err != nil {
		apierr.Write(w, err)
		return
	}
	items := make([]advertiserItem, 0, len(rows))
	for _, a := range rows {
		items = append(items, advertiserItem{ID: a.ID, Name: a.Name, Contact: a.Contact, Status: string(a.Status)})
	}
	apierr.WriteJSON(w, http.StatusOK, listResponse[advertiserItem]{Items: items})
}

type updateAdvertiserRequest struct {
	Name    *string `json:"name"`
	Contact *string `json:"contact"`
}

func (h *Handler) handleUpdateAdvertiser(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r)
	if !ok {
		return
	}
	var req updateAdvertiserRequest
	if !apierr.DecodeJSON(w, r, &req) {
		return
	}
	if _, err := h.Ads.UpdateAdvertiser(r.Context(), db.UpdateAdvertiserParams{ID: id, Name: req.Name, Contact: req.Contact}); err != nil {
		apierr.Write(w, err)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (h *Handler) handleSetAdvertiserStatus(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r)
	if !ok {
		return
	}
	var req toggleStatusRequest
	if !apierr.DecodeJSON(w, r, &req) {
		return
	}
	if req.Enabled == nil {
		apierr.Write(w, apierr.InvalidArgument("enabled is required"))
		return
	}
	if err := h.Ads.SetAdvertiserStatus(r.Context(), id, *req.Enabled); err != nil {
		apierr.Write(w, err)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// ---- 广告 ----

type createAdRequest struct {
	AdvertiserID int64   `json:"advertiser_id"`
	Title        string  `json:"title"`
	MediaPath    string  `json:"media_path"`
	Link         *string `json:"link"`
	Stock        int32   `json:"stock"`
	ExpiresAt    *string `json:"expires_at"`
}

func (h *Handler) handleCreateAd(w http.ResponseWriter, r *http.Request) {
	var req createAdRequest
	if !apierr.DecodeJSON(w, r, &req) {
		return
	}
	if req.AdvertiserID <= 0 || req.Title == "" || req.MediaPath == "" || req.Stock < 0 {
		apierr.Write(w, apierr.InvalidArgument("advertiser_id, title, media_path and non-negative stock are required"))
		return
	}
	expiresAt, err := tsFromRFC3339(req.ExpiresAt)
	if err != nil {
		apierr.Write(w, apierr.InvalidArgument("expires_at must be RFC3339"))
		return
	}
	a, err := h.Ads.CreateAd(r.Context(), db.CreateAdParams{
		AdvertiserID: req.AdvertiserID, Title: req.Title, MediaPath: req.MediaPath,
		Link: req.Link, Stock: req.Stock, ExpiresAt: expiresAt,
	})
	if err != nil {
		apierr.Write(w, err)
		return
	}
	apierr.WriteJSON(w, http.StatusCreated, map[string]int64{"id": a.ID})
}

type adItem struct {
	ID             int64  `json:"id"`
	AdvertiserID   int64  `json:"advertiser_id"`
	Title          string `json:"title"`
	StockRemaining int32  `json:"stock_remaining"`
	Status         string `json:"status"`
}

func (h *Handler) handleListAds(w http.ResponseWriter, r *http.Request) {
	rows, err := h.Ads.ListAds(r.Context())
	if err != nil {
		apierr.Write(w, err)
		return
	}
	items := make([]adItem, 0, len(rows))
	for _, a := range rows {
		items = append(items, adItem{
			ID: a.ID, AdvertiserID: a.AdvertiserID, Title: a.Title,
			StockRemaining: a.StockRemaining, Status: string(a.Status),
		})
	}
	apierr.WriteJSON(w, http.StatusOK, listResponse[adItem]{Items: items})
}

type updateAdRequest struct {
	Title     *string `json:"title"`
	MediaPath *string `json:"media_path"`
	Link      *string `json:"link"`
	ExpiresAt *string `json:"expires_at"`
}

func (h *Handler) handleUpdateAd(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r)
	if !ok {
		return
	}
	var req updateAdRequest
	if !apierr.DecodeJSON(w, r, &req) {
		return
	}
	expiresAt, err := tsFromRFC3339(req.ExpiresAt)
	if err != nil {
		apierr.Write(w, apierr.InvalidArgument("expires_at must be RFC3339"))
		return
	}
	if _, err := h.Ads.UpdateAd(r.Context(), db.UpdateAdParams{
		ID: id, Title: req.Title, MediaPath: req.MediaPath, Link: req.Link, ExpiresAt: expiresAt,
	}); err != nil {
		apierr.Write(w, err)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (h *Handler) handleSetAdStatus(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r)
	if !ok {
		return
	}
	var req toggleStatusRequest
	if !apierr.DecodeJSON(w, r, &req) {
		return
	}
	if req.Enabled == nil {
		apierr.Write(w, apierr.InvalidArgument("enabled is required"))
		return
	}
	if err := h.Ads.SetAdStatus(r.Context(), id, *req.Enabled); err != nil {
		apierr.Write(w, err)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (h *Handler) handleDeleteAd(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r)
	if !ok {
		return
	}
	if err := h.Ads.SoftDeleteAd(r.Context(), id); err != nil {
		apierr.Write(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
