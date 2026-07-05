package api

import (
	"net/http"

	"github.com/netfishx/gabon-go/internal/apierr"
	"github.com/netfishx/gabon-go/internal/pagination"
)

const (
	teamDefaultLimit = 20
	teamMaxLimit     = 100
)

type teamMemberItem struct {
	PublicID         string  `json:"public_id"`
	Username         string  `json:"username"`
	Name             *string `json:"name"`
	AvatarPath       *string `json:"avatar_path"`
	Valid            bool    `json:"valid"`
	SubordinateCount int64   `json:"subordinate_count"`
}

func (h *Handler) handleTeamMembers(w http.ResponseWriter, r *http.Request) {
	limit, err := pagination.Limit(r, teamDefaultLimit, teamMaxLimit)
	if err != nil {
		apierr.Write(w, err)
		return
	}
	cursor, err := pagination.Cursor(r)
	if err != nil {
		apierr.Write(w, err)
		return
	}

	items, next, err := h.Customers.ListTeamMembers(
		r.Context(), customerFrom(r.Context()), r.URL.Query().Get("parent"), cursor, limit)
	if err != nil {
		apierr.Write(w, err)
		return
	}
	out := pagination.Page[teamMemberItem]{Items: make([]teamMemberItem, 0, len(items)), NextCursor: next}
	for _, m := range items {
		out.Items = append(out.Items, teamMemberItem{
			PublicID:         m.PublicID,
			Username:         m.Username,
			Name:             m.Name,
			AvatarPath:       m.AvatarPath,
			Valid:            m.Valid,
			SubordinateCount: m.SubordinateCount,
		})
	}
	apierr.WriteJSON(w, http.StatusOK, out)
}

type teamLayerItem struct {
	Depth      int   `json:"depth"`
	Count      int64 `json:"count"`
	ValidCount int64 `json:"valid_count"`
}

type teamSummaryResponse struct {
	Total             int64           `json:"total"`
	TotalValid        int64           `json:"total_valid"`
	InviteRewardTotal int64           `json:"invite_reward_total"`
	Layers            []teamLayerItem `json:"layers"`
}

func (h *Handler) handleTeamSummary(w http.ResponseWriter, r *http.Request) {
	sum, err := h.Customers.GetTeamSummary(r.Context(), customerFrom(r.Context()).ID)
	if err != nil {
		apierr.Write(w, err)
		return
	}
	out := teamSummaryResponse{
		Total:             sum.Total,
		TotalValid:        sum.TotalValid,
		InviteRewardTotal: sum.InviteRewardTotal,
		Layers:            make([]teamLayerItem, 0, len(sum.Layers)),
	}
	for _, l := range sum.Layers {
		out.Layers = append(out.Layers, teamLayerItem(l))
	}
	apierr.WriteJSON(w, http.StatusOK, out)
}
