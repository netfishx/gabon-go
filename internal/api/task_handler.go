package api

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/netfishx/gabon-go/internal/apierr"
	"github.com/netfishx/gabon-go/internal/db"
)

// advanceTask 主事件事务提交后推进任务进度：任务域独立事务自管，
// 失败仅记日志、不回传主链路（PRD #45 架构：任务故障不拖垮内容主链路）。
func (h *Handler) advanceTask(ctx context.Context, customerID int64, category db.TaskCategory, refID int64) {
	if err := h.Tasks.Advance(ctx, customerID, category, refID); err != nil {
		slog.WarnContext(ctx, "task advance failed",
			"customer_id", customerID, "category", category, "ref_id", refID, "error", err)
	}
}

type periodicTaskItem struct {
	Name     string  `json:"name"`
	IconURL  *string `json:"icon_url"`
	Category string  `json:"category"`
	Period   string  `json:"period"`
	Target   int32   `json:"target"`
	Progress int32   `json:"progress"`
	Reward   int64   `json:"reward"`
	Granted  bool    `json:"granted"`
}

func (h *Handler) handleTasks(w http.ResponseWriter, r *http.Request) {
	items, err := h.Tasks.ListWithProgress(r.Context(), customerFrom(r.Context()).ID)
	if err != nil {
		apierr.Write(w, err)
		return
	}
	out := struct {
		Items []periodicTaskItem `json:"items"`
	}{Items: make([]periodicTaskItem, 0, len(items))}
	for _, it := range items {
		out.Items = append(out.Items, periodicTaskItem{
			Name:     it.Task.Name,
			IconURL:  h.mediaURL(it.Task.IconPath),
			Category: string(it.Task.Category),
			Period:   string(it.Task.Period),
			Target:   it.Task.Target,
			Progress: it.Progress,
			Reward:   it.Task.Reward,
			Granted:  it.Granted,
		})
	}
	apierr.WriteJSON(w, http.StatusOK, out)
}
