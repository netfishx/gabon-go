package api

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/netfishx/gabon-go/internal/apierr"
	"github.com/netfishx/gabon-go/internal/shortcode"
)

// 图片预签名上传（feature-checklist L）：路径 {kind复数}/{customer_id}/{random}.{ext}，
// 不建 uploads 表——归属校验凭路径前缀，消费点另查对象存在。
const imagePresignExpiry = 15 * time.Minute

// imageKinds 用途白名单 → 路径段（复数，与 videos/ 一致）。
var imageKinds = map[string]string{
	"avatar": "avatars",
	"proof":  "proofs",
}

// imageExts 扩展名白名单（与旧版一致）。
var imageExts = map[string]bool{"jpg": true, "jpeg": true, "png": true, "gif": true, "webp": true}

type imageUploadRequest struct {
	Kind string `json:"kind"`
	Ext  string `json:"ext"`
}

type imageUploadResponse struct {
	StoragePath string `json:"storage_path"`
	UploadURL   string `json:"upload_url"`
}

func (h *Handler) handleImageUpload(w http.ResponseWriter, r *http.Request) {
	var req imageUploadRequest
	if !apierr.DecodeJSON(w, r, &req) {
		return
	}
	kindDir, ok := imageKinds[req.Kind]
	if !ok {
		apierr.Write(w, apierr.InvalidArgument("kind must be avatar or proof"))
		return
	}
	ext := req.Ext
	if ext == "" {
		ext = "jpg"
	}
	if !imageExts[ext] {
		apierr.Write(w, apierr.InvalidArgument("unsupported ext, allowed: jpg/jpeg/png/gif/webp"))
		return
	}

	name, err := shortcode.New(shortcode.Base58, 16)
	if err != nil {
		apierr.Write(w, err)
		return
	}
	storagePath := fmt.Sprintf("%s/%d/%s.%s", kindDir, customerFrom(r.Context()).ID, name, ext)
	uploadURL, err := h.Store.PresignPut(r.Context(), storagePath, imagePresignExpiry)
	if err != nil {
		apierr.Write(w, err)
		return
	}
	apierr.WriteJSON(w, http.StatusCreated, imageUploadResponse{StoragePath: storagePath, UploadURL: uploadURL})
}

// validateAvatarPath 头像消费校验：本人 avatars 前缀 + 白名单扩展名 + 对象真实存在。
func (h *Handler) validateAvatarPath(r *http.Request, customerID int64, path string) error {
	if !strings.HasPrefix(path, fmt.Sprintf("avatars/%d/", customerID)) {
		return apierr.New(http.StatusForbidden, apierr.CodeUploadPathForbidden, "avatar path does not belong to you")
	}
	dot := strings.LastIndex(path, ".")
	if dot < 0 || !imageExts[path[dot+1:]] {
		return apierr.InvalidArgument("avatar path has unsupported ext")
	}
	exists, err := h.Store.Exists(r.Context(), path)
	if err != nil {
		return err
	}
	if !exists {
		return apierr.New(http.StatusBadRequest, apierr.CodeUploadObjectMissing, "avatar object not found")
	}
	return nil
}
