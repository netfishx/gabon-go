package admin

import (
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/netfishx/gabon-go/internal/apierr"
	"github.com/netfishx/gabon-go/internal/db"
)

func taskIDParam(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		apierr.Write(w, apierr.InvalidArgument("malformed id"))
		return 0, false
	}
	return id, true
}

// tsFromRFC3339 解析可选 RFC3339 时间戳为 pgtype.Timestamptz（nil/空 = 无效/不更新）。
func tsFromRFC3339(s *string) (pgtype.Timestamptz, error) {
	if s == nil || *s == "" {
		return pgtype.Timestamptz{}, nil
	}
	t, err := time.Parse(time.RFC3339, *s)
	if err != nil {
		return pgtype.Timestamptz{}, err
	}
	return pgtype.Timestamptz{Time: t, Valid: true}, nil
}

// ---- 周期任务定义 ----

type createPeriodicTaskRequest struct {
	Name         string  `json:"name"`
	Description  *string `json:"description"`
	IconPath     *string `json:"icon_path"`
	Category     string  `json:"category"`
	Period       string  `json:"period"`
	Target       int32   `json:"target"`
	Reward       int64   `json:"reward"`
	DisplayOrder int32   `json:"display_order"`
}

func (h *Handler) handleCreatePeriodicTask(w http.ResponseWriter, r *http.Request) {
	var req createPeriodicTaskRequest
	if !apierr.DecodeJSON(w, r, &req) {
		return
	}
	if req.Name == "" || req.Target <= 0 || req.Reward < 0 {
		apierr.Write(w, apierr.InvalidArgument("name, positive target and non-negative reward are required"))
		return
	}
	t, err := h.Tasks.CreatePeriodicTask(r.Context(), db.CreatePeriodicTaskParams{
		Name: req.Name, Description: req.Description, IconPath: req.IconPath,
		Category: db.TaskCategory(req.Category), Period: db.TaskPeriod(req.Period),
		Target: req.Target, Reward: req.Reward, DisplayOrder: req.DisplayOrder,
	})
	if err != nil {
		apierr.Write(w, err)
		return
	}
	apierr.WriteJSON(w, http.StatusCreated, map[string]int64{"id": t.ID})
}

func (h *Handler) handleListPeriodicTasks(w http.ResponseWriter, r *http.Request) {
	rows, err := h.Tasks.ListPeriodicTasksAdmin(r.Context())
	if err != nil {
		apierr.Write(w, err)
		return
	}
	items := make([]map[string]any, 0, len(rows))
	for _, t := range rows {
		items = append(items, map[string]any{
			"id": t.ID, "name": t.Name, "category": string(t.Category), "period": string(t.Period),
			"target": t.Target, "reward": t.Reward, "display_order": t.DisplayOrder, "enabled": t.Enabled,
		})
	}
	apierr.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

type updatePeriodicTaskRequest struct {
	Name         *string `json:"name"`
	Description  *string `json:"description"`
	IconPath     *string `json:"icon_path"`
	Target       *int32  `json:"target"`
	Reward       *int64  `json:"reward"`
	DisplayOrder *int32  `json:"display_order"`
	Enabled      *bool   `json:"enabled"`
}

func (h *Handler) handleUpdatePeriodicTask(w http.ResponseWriter, r *http.Request) {
	id, ok := taskIDParam(w, r)
	if !ok {
		return
	}
	var req updatePeriodicTaskRequest
	if !apierr.DecodeJSON(w, r, &req) {
		return
	}
	if _, err := h.Tasks.UpdatePeriodicTask(r.Context(), db.UpdatePeriodicTaskParams{
		ID: id, Name: req.Name, Description: req.Description, IconPath: req.IconPath,
		Target: req.Target, Reward: req.Reward, DisplayOrder: req.DisplayOrder, Enabled: req.Enabled,
	}); err != nil {
		apierr.Write(w, err)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// ---- 限时任务定义 ----

type createClaimTaskRequest struct {
	Name         string  `json:"name"`
	Description  *string `json:"description"`
	IconPath     *string `json:"icon_path"`
	MinVipLevel  int32   `json:"min_vip_level"`
	Reward       int64   `json:"reward"`
	Requirement  *string `json:"requirement"`
	Flow         *string `json:"flow"`
	Link         *string `json:"link"`
	DisplayOrder int32   `json:"display_order"`
	StartsAt     *string `json:"starts_at"`
	EndsAt       *string `json:"ends_at"`
}

func (h *Handler) handleCreateClaimTask(w http.ResponseWriter, r *http.Request) {
	var req createClaimTaskRequest
	if !apierr.DecodeJSON(w, r, &req) {
		return
	}
	if req.Name == "" || req.Reward < 0 {
		apierr.Write(w, apierr.InvalidArgument("name and non-negative reward are required"))
		return
	}
	startsAt, err := tsFromRFC3339(req.StartsAt)
	if err != nil {
		apierr.Write(w, apierr.InvalidArgument("starts_at must be RFC3339"))
		return
	}
	endsAt, err := tsFromRFC3339(req.EndsAt)
	if err != nil {
		apierr.Write(w, apierr.InvalidArgument("ends_at must be RFC3339"))
		return
	}
	t, err := h.Tasks.CreateClaimTask(r.Context(), db.CreateClaimTaskParams{
		Name: req.Name, Description: req.Description, IconPath: req.IconPath,
		MinVipLevel: req.MinVipLevel, Reward: req.Reward,
		Requirement: req.Requirement, Flow: req.Flow, Link: req.Link,
		DisplayOrder: req.DisplayOrder, StartsAt: startsAt, EndsAt: endsAt,
	})
	if err != nil {
		apierr.Write(w, err)
		return
	}
	apierr.WriteJSON(w, http.StatusCreated, map[string]int64{"id": t.ID})
}

func (h *Handler) handleListClaimTasks(w http.ResponseWriter, r *http.Request) {
	rows, err := h.Tasks.ListClaimTasksAdmin(r.Context())
	if err != nil {
		apierr.Write(w, err)
		return
	}
	items := make([]map[string]any, 0, len(rows))
	for _, t := range rows {
		items = append(items, map[string]any{
			"id": t.ID, "name": t.Name, "min_vip_level": t.MinVipLevel,
			"reward": t.Reward, "display_order": t.DisplayOrder, "enabled": t.Enabled,
		})
	}
	apierr.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

type updateClaimTaskRequest struct {
	Name         *string `json:"name"`
	Description  *string `json:"description"`
	IconPath     *string `json:"icon_path"`
	MinVipLevel  *int32  `json:"min_vip_level"`
	Reward       *int64  `json:"reward"`
	Requirement  *string `json:"requirement"`
	Flow         *string `json:"flow"`
	Link         *string `json:"link"`
	DisplayOrder *int32  `json:"display_order"`
	StartsAt     *string `json:"starts_at"`
	EndsAt       *string `json:"ends_at"`
}

func (h *Handler) handleUpdateClaimTask(w http.ResponseWriter, r *http.Request) {
	id, ok := taskIDParam(w, r)
	if !ok {
		return
	}
	var req updateClaimTaskRequest
	if !apierr.DecodeJSON(w, r, &req) {
		return
	}
	startsAt, err := tsFromRFC3339(req.StartsAt)
	if err != nil {
		apierr.Write(w, apierr.InvalidArgument("starts_at must be RFC3339"))
		return
	}
	endsAt, err := tsFromRFC3339(req.EndsAt)
	if err != nil {
		apierr.Write(w, apierr.InvalidArgument("ends_at must be RFC3339"))
		return
	}
	if _, err := h.Tasks.UpdateClaimTask(r.Context(), db.UpdateClaimTaskParams{
		ID: id, Name: req.Name, Description: req.Description, IconPath: req.IconPath,
		MinVipLevel: req.MinVipLevel, Reward: req.Reward,
		Requirement: req.Requirement, Flow: req.Flow, Link: req.Link,
		DisplayOrder: req.DisplayOrder, StartsAt: startsAt, EndsAt: endsAt,
	}); err != nil {
		apierr.Write(w, err)
		return
	}
	w.WriteHeader(http.StatusOK)
}

type toggleStatusRequest struct {
	Enabled bool `json:"enabled"`
}

func (h *Handler) handleToggleClaimTaskStatus(w http.ResponseWriter, r *http.Request) {
	id, ok := taskIDParam(w, r)
	if !ok {
		return
	}
	var req toggleStatusRequest
	if !apierr.DecodeJSON(w, r, &req) {
		return
	}
	if err := h.Tasks.SetClaimTaskEnabled(r.Context(), id, req.Enabled); err != nil {
		apierr.Write(w, err)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (h *Handler) handleDeleteClaimTask(w http.ResponseWriter, r *http.Request) {
	id, ok := taskIDParam(w, r)
	if !ok {
		return
	}
	if err := h.Tasks.SoftDeleteClaimTask(r.Context(), id); err != nil {
		apierr.Write(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
