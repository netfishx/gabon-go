package api

import (
	"net/http"

	"github.com/netfishx/gabon-go/internal/apierr"
)

type vipPurchaseRequest struct {
	Level int32 `json:"level"`
}

func (h *Handler) handleVipPurchase(w http.ResponseWriter, r *http.Request) {
	var req vipPurchaseRequest
	if !apierr.DecodeJSON(w, r, &req) {
		return
	}
	if err := h.Vips.Purchase(r.Context(), customerFrom(r.Context()).ID, req.Level); err != nil {
		apierr.Write(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type vipLevelItem struct {
	Level              int32  `json:"level"`
	Name               string `json:"name"`
	Price              int64  `json:"price"`
	RewardMultiplierBp int32  `json:"reward_multiplier_bp"`
	UploadVideoLimit   int32  `json:"upload_video_limit"`
	InviteRewardCap    int64  `json:"invite_reward_cap"`
}

func (h *Handler) handleVipLevels(w http.ResponseWriter, r *http.Request) {
	rows, err := h.Vips.Levels(r.Context())
	if err != nil {
		apierr.Write(w, err)
		return
	}
	items := make([]vipLevelItem, 0, len(rows))
	for _, v := range rows {
		items = append(items, vipLevelItem{
			Level: v.Level, Name: v.Name, Price: v.Price,
			RewardMultiplierBp: v.RewardMultiplierBp,
			UploadVideoLimit:   v.UploadVideoLimit, InviteRewardCap: v.InviteRewardCap,
		})
	}
	apierr.WriteJSON(w, http.StatusOK, struct {
		Items []vipLevelItem `json:"items"`
	}{Items: items})
}
