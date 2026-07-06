package api

import (
	"net/http"
	"net/mail"
	"regexp"
	"unicode/utf8"

	"github.com/netfishx/gabon-go/internal/apierr"
	"github.com/netfishx/gabon-go/internal/customer"
)

// phonePattern 覆盖 13x–19x 全部现行号段（修正旧版遗漏 16x/19x 的正则，M4 行为差异）。
var phonePattern = regexp.MustCompile(`^1[3-9]\d{9}$`)

const (
	nameMaxLen      = 50
	signatureMaxLen = 255
	emailMaxLen     = 254
)

// updateProfileRequest 字段缺省（null）= 不更新；提交空串按参数错误处理。
type updateProfileRequest struct {
	Name       *string `json:"name"`
	Signature  *string `json:"signature"`
	Email      *string `json:"email"`
	Phone      *string `json:"phone"`
	AvatarPath *string `json:"avatar_path"`
}

func (h *Handler) handleUpdateProfile(w http.ResponseWriter, r *http.Request) {
	var req updateProfileRequest
	if !apierr.DecodeJSON(w, r, &req) {
		return
	}
	if req.Name == nil && req.Signature == nil && req.Email == nil && req.Phone == nil && req.AvatarPath == nil {
		apierr.Write(w, apierr.InvalidArgument("no fields to update"))
		return
	}
	if req.Name != nil {
		if n := utf8.RuneCountInString(*req.Name); n < 1 || n > nameMaxLen {
			apierr.Write(w, apierr.InvalidArgument("name must be 1-50 chars"))
			return
		}
	}
	if req.Signature != nil && utf8.RuneCountInString(*req.Signature) > signatureMaxLen {
		apierr.Write(w, apierr.InvalidArgument("signature must be at most 255 chars"))
		return
	}
	if req.Email != nil && !validEmail(*req.Email) {
		apierr.Write(w, apierr.InvalidArgument("email is not a valid address"))
		return
	}
	if req.Phone != nil && !phonePattern.MatchString(*req.Phone) {
		apierr.Write(w, apierr.InvalidArgument("phone must be a valid CN mobile number"))
		return
	}
	me := customerFrom(r.Context())
	if req.AvatarPath != nil {
		if err := h.validateImagePath(r, me.ID, imageKinds["avatar"], *req.AvatarPath); err != nil {
			apierr.Write(w, err)
			return
		}
	}

	c, err := h.Customers.UpdateProfile(r.Context(), me.ID, customer.ProfileUpdate{
		Name:       req.Name,
		Signature:  req.Signature,
		Email:      req.Email,
		Phone:      req.Phone,
		AvatarPath: req.AvatarPath,
	})
	if err != nil {
		apierr.Write(w, err)
		return
	}
	apierr.WriteJSON(w, http.StatusOK, h.toCustomerResponse(c))
}

// validEmail 要求裸地址（无显示名）且长度合规。
func validEmail(s string) bool {
	if s == "" || len(s) > emailMaxLen {
		return false
	}
	addr, err := mail.ParseAddress(s)
	return err == nil && addr.Address == s
}
