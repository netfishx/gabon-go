package api

import (
	"net/http"
	"strconv"

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
