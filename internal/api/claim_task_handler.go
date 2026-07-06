package api

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/netfishx/gabon-go/internal/apierr"
)

func claimIDParam(w http.ResponseWriter, r *http.Request, name string) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, name), 10, 64)
	if err != nil || id <= 0 {
		apierr.Write(w, apierr.InvalidArgument("malformed id"))
		return 0, false
	}
	return id, true
}

func (h *Handler) handleClaimTaskClaim(w http.ResponseWriter, r *http.Request) {
	taskID, ok := claimIDParam(w, r, "taskID")
	if !ok {
		return
	}
	c := customerFrom(r.Context())
	if err := h.Tasks.Claim(r.Context(), int(c.ID), int(c.VipLevel), taskID); err != nil {
		apierr.Write(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type submitProofRequest struct {
	ProofText   *string  `json:"proof_text"`
	ProofImages []string `json:"proof_images"`
}

func (h *Handler) handleClaimTaskSubmit(w http.ResponseWriter, r *http.Request) {
	claimID, ok := claimIDParam(w, r, "claimID")
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
		if err := h.validateProofPath(r, c.ID, img); err != nil {
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

// validateProofPath 任务证明消费校验：与头像同款三重校验，前缀为 proofs/{本人id}/。
func (h *Handler) validateProofPath(r *http.Request, customerID int64, path string) error {
	if !strings.HasPrefix(path, fmt.Sprintf("proofs/%d/", customerID)) {
		return apierr.New(http.StatusForbidden, apierr.CodeUploadPathForbidden, "proof path does not belong to you")
	}
	dot := strings.LastIndex(path, ".")
	if dot < 0 || !imageExts[path[dot+1:]] {
		return apierr.InvalidArgument("proof path has unsupported ext")
	}
	exists, err := h.Store.Exists(r.Context(), path)
	if err != nil {
		return err
	}
	if !exists {
		return apierr.New(http.StatusBadRequest, apierr.CodeUploadObjectMissing, "proof object not found")
	}
	return nil
}
