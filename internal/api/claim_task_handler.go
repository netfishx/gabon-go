package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/netfishx/gabon-go/internal/apierr"
)

// positiveIDParam 解析路径中的正整数 id 参数。
func positiveIDParam(w http.ResponseWriter, r *http.Request, name string) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, name), 10, 64)
	if err != nil || id <= 0 {
		apierr.Write(w, apierr.InvalidArgument("malformed id"))
		return 0, false
	}
	return id, true
}

func (h *Handler) handleClaimTaskClaim(w http.ResponseWriter, r *http.Request) {
	taskID, ok := positiveIDParam(w, r, "taskID")
	if !ok {
		return
	}
	c := customerFrom(r.Context())
	claimID, err := h.Tasks.Claim(r.Context(), c.ID, c.VipLevel, taskID)
	if err != nil {
		apierr.Write(w, err)
		return
	}
	apierr.WriteJSON(w, http.StatusCreated, map[string]int64{"claim_id": claimID})
}

type submitProofRequest struct {
	ProofText   *string  `json:"proof_text"`
	ProofImages []string `json:"proof_images"`
}

func (h *Handler) handleClaimTaskSubmit(w http.ResponseWriter, r *http.Request) {
	claimID, ok := positiveIDParam(w, r, "claimID")
	if !ok {
		return
	}
	var req submitProofRequest
	if !apierr.DecodeJSON(w, r, &req) {
		return
	}
	c := customerFrom(r.Context())
	// 凭证归属校验：本人 proofs 前缀 + 白名单扩展名 + 对象存在（复用 L 上传三重校验）
	for _, img := range req.ProofImages {
		if err := h.validateImagePath(r, c.ID, imageKinds["proof"], img); err != nil {
			apierr.Write(w, err)
			return
		}
	}
	if err := h.Tasks.Submit(r.Context(), c.ID, claimID, req.ProofText, req.ProofImages); err != nil {
		apierr.Write(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type claimTaskListItem struct {
	ID          int64   `json:"id"`
	Name        string  `json:"name"`
	IconURL     *string `json:"icon_url"`
	MinVipLevel int32   `json:"min_vip_level"`
	Reward      int64   `json:"reward"`
	Group       string  `json:"group"`
}

func (h *Handler) handleClaimTaskList(w http.ResponseWriter, r *http.Request) {
	c := customerFrom(r.Context())
	views, err := h.Tasks.ListClaimTasks(r.Context(), c.ID, c.VipLevel)
	if err != nil {
		apierr.Write(w, err)
		return
	}
	out := struct {
		Items []claimTaskListItem `json:"items"`
	}{Items: make([]claimTaskListItem, 0, len(views))}
	for _, v := range views {
		out.Items = append(out.Items, claimTaskListItem{
			ID: v.Task.ID, Name: v.Task.Name, IconURL: h.mediaURL(v.Task.IconPath),
			MinVipLevel: v.Task.MinVipLevel, Reward: v.Task.Reward, Group: string(v.Group),
		})
	}
	apierr.WriteJSON(w, http.StatusOK, out)
}

type claimTaskDetailResponse struct {
	ID          int64      `json:"id"`
	Name        string     `json:"name"`
	IconURL     *string    `json:"icon_url"`
	MinVipLevel int32      `json:"min_vip_level"`
	Reward      int64      `json:"reward"`
	Requirement *string    `json:"requirement"`
	Flow        *string    `json:"flow"`
	Link        *string    `json:"link"`
	Deadline    *time.Time `json:"deadline"` // 截止时间（US12），null = 永不过期
	ClaimStatus *string    `json:"claim_status"`
}

func (h *Handler) handleClaimTaskDetail(w http.ResponseWriter, r *http.Request) {
	taskID, ok := positiveIDParam(w, r, "taskID")
	if !ok {
		return
	}
	c := customerFrom(r.Context())
	d, err := h.Tasks.GetClaimTaskDetail(r.Context(), c.ID, taskID)
	if err != nil {
		apierr.Write(w, err)
		return
	}
	var status *string
	if d.ClaimStatus.Valid {
		s := string(d.ClaimStatus.ClaimStatus)
		status = &s
	}
	var deadline *time.Time
	if d.EndsAt.Valid {
		deadline = &d.EndsAt.Time
	}
	apierr.WriteJSON(w, http.StatusOK, claimTaskDetailResponse{
		ID: d.ID, Name: d.Name, IconURL: h.mediaURL(d.IconPath),
		MinVipLevel: d.MinVipLevel, Reward: d.Reward,
		Requirement: d.Requirement, Flow: d.Flow, Link: d.Link,
		Deadline: deadline, ClaimStatus: status,
	})
}

type myClaimItem struct {
	ClaimID       int64   `json:"claim_id"`
	TaskName      string  `json:"task_name"`
	Status        string  `json:"status"`
	RewardBase    int64   `json:"reward_base"`
	RewardGranted *int64  `json:"reward_granted"`
	ReviewRemark  *string `json:"review_remark"`
}

func (h *Handler) handleMyClaims(w http.ResponseWriter, r *http.Request) {
	done := r.URL.Query().Get("tab") == "done"
	rows, err := h.Tasks.ListMyClaims(r.Context(), customerFrom(r.Context()).ID, done)
	if err != nil {
		apierr.Write(w, err)
		return
	}
	out := struct {
		Items []myClaimItem `json:"items"`
	}{Items: make([]myClaimItem, 0, len(rows))}
	for _, row := range rows {
		out.Items = append(out.Items, myClaimItem{
			ClaimID: row.ID, TaskName: row.TaskName, Status: string(row.Status),
			RewardBase: row.RewardBase, RewardGranted: row.RewardGranted, ReviewRemark: row.ReviewRemark,
		})
	}
	apierr.WriteJSON(w, http.StatusOK, out)
}
