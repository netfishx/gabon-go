package api

import (
	"net/http"

	"github.com/netfishx/gabon-go/internal/apierr"
	"github.com/netfishx/gabon-go/internal/db"
)

type watchAdResponse struct {
	Ad *servedAd `json:"ad"` // null = 当前无在投广告
}

type servedAd struct {
	ID       int64   `json:"id"`
	Title    string  `json:"title"`
	MediaURL *string `json:"media_url"`
	Link     *string `json:"link"`
}

func (h *Handler) handleWatchAd(w http.ResponseWriter, r *http.Request) {
	c := customerFrom(r.Context())
	served, err := h.Ads.Watch(r.Context(), c.ID)
	if err != nil {
		apierr.Write(w, err)
		return
	}
	var out watchAdResponse
	if served.Ad != nil {
		out.Ad = &servedAd{
			ID:       served.Ad.ID,
			Title:    served.Ad.Title,
			MediaURL: h.mediaURL(&served.Ad.MediaPath),
			Link:     served.Ad.Link,
		}
		// 看广告推进"看广告"周期任务进度（主事件提交后独立事务，失败仅记日志）
		h.advanceTask(r.Context(), c.ID, db.TaskCategoryWatchAd, 0)
	}
	apierr.WriteJSON(w, http.StatusOK, out)
}
