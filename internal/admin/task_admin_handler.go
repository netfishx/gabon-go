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

// maxVipLevel VIP 档位上限（vip_level_configs level 0–3）；min_vip_level 门槛须落此范围。
const maxVipLevel = 3

// listResponse 后台无游标列表的统一响应形状。
type listResponse[T any] struct {
	Items []T `json:"items"`
}

// validVipLevel 校验 VIP 门槛在 [0, maxVipLevel]。
func validVipLevel(level int32) bool {
	return level >= 0 && level <= maxVipLevel
}

func idParam(w http.ResponseWriter, r *http.Request) (int64, bool) {
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

type periodicTaskAdminItem struct {
	ID           int64  `json:"id"`
	Name         string `json:"name"`
	Category     string `json:"category"`
	Period       string `json:"period"`
	Target       int32  `json:"target"`
	Reward       int64  `json:"reward"`
	DisplayOrder int32  `json:"display_order"`
	Enabled      bool   `json:"enabled"`
}

func (h *Handler) handleListPeriodicTasks(w http.ResponseWriter, r *http.Request) {
	rows, err := h.Tasks.ListPeriodicTasksAdmin(r.Context())
	if err != nil {
		apierr.Write(w, err)
		return
	}
	items := make([]periodicTaskAdminItem, 0, len(rows))
	for _, t := range rows {
		items = append(items, periodicTaskAdminItem{
			ID: t.ID, Name: t.Name, Category: string(t.Category), Period: string(t.Period),
			Target: t.Target, Reward: t.Reward, DisplayOrder: t.DisplayOrder, Enabled: t.Enabled,
		})
	}
	apierr.WriteJSON(w, http.StatusOK, listResponse[periodicTaskAdminItem]{Items: items})
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
	id, ok := idParam(w, r)
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
	if !validVipLevel(req.MinVipLevel) {
		apierr.Write(w, apierr.InvalidArgument("min_vip_level must be between 0 and 3"))
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

type claimTaskAdminItem struct {
	ID           int64  `json:"id"`
	Name         string `json:"name"`
	MinVipLevel  int32  `json:"min_vip_level"`
	Reward       int64  `json:"reward"`
	DisplayOrder int32  `json:"display_order"`
	Enabled      bool   `json:"enabled"`
}

func (h *Handler) handleListClaimTasks(w http.ResponseWriter, r *http.Request) {
	rows, err := h.Tasks.ListClaimTasksAdmin(r.Context())
	if err != nil {
		apierr.Write(w, err)
		return
	}
	items := make([]claimTaskAdminItem, 0, len(rows))
	for _, t := range rows {
		items = append(items, claimTaskAdminItem{
			ID: t.ID, Name: t.Name, MinVipLevel: t.MinVipLevel,
			Reward: t.Reward, DisplayOrder: t.DisplayOrder, Enabled: t.Enabled,
		})
	}
	apierr.WriteJSON(w, http.StatusOK, listResponse[claimTaskAdminItem]{Items: items})
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
	id, ok := idParam(w, r)
	if !ok {
		return
	}
	var req updateClaimTaskRequest
	if !apierr.DecodeJSON(w, r, &req) {
		return
	}
	if req.MinVipLevel != nil && !validVipLevel(*req.MinVipLevel) {
		apierr.Write(w, apierr.InvalidArgument("min_vip_level must be between 0 and 3"))
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
	Enabled *bool `json:"enabled"`
}

func (h *Handler) handleToggleClaimTaskStatus(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r)
	if !ok {
		return
	}
	var req toggleStatusRequest
	if !apierr.DecodeJSON(w, r, &req) {
		return
	}
	// 破坏性 toggle：enabled 缺失（{} 或拼错字段）不得静默下架
	if req.Enabled == nil {
		apierr.Write(w, apierr.InvalidArgument("enabled is required"))
		return
	}
	if err := h.Tasks.SetClaimTaskEnabled(r.Context(), id, *req.Enabled); err != nil {
		apierr.Write(w, err)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (h *Handler) handleDeleteClaimTask(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r)
	if !ok {
		return
	}
	if err := h.Tasks.SoftDeleteClaimTask(r.Context(), id); err != nil {
		apierr.Write(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
