package api

import (
	"net/http"
	"regexp"
	"time"

	"github.com/netfishx/gabon-go/internal/apierr"
	"github.com/netfishx/gabon-go/internal/auth"
	"github.com/netfishx/gabon-go/internal/db"
)

var usernamePattern = regexp.MustCompile(`^[a-zA-Z0-9_]{3,32}$`)

const (
	passwordMinLen = 6
	passwordMaxLen = 72
)

type registerRequest struct {
	Username   string `json:"username"`
	Password   string `json:"password"`
	InviteCode string `json:"invite_code"`
}

type customerResponse struct {
	PublicID    string    `json:"public_id"`
	Username    string    `json:"username"`
	Name        *string   `json:"name"`
	Signature   *string   `json:"signature"`
	Email       *string   `json:"email"`
	Phone       *string   `json:"phone"`
	InviteCode  string    `json:"invite_code"`
	Valid       bool      `json:"valid"`
	InviteCount int32     `json:"invite_count"` // 总邀请数（注册即算），区别于 /me 的 valid_invite_count
	CreatedAt   time.Time `json:"created_at"`
}

func toCustomerResponse(c *db.Customer) customerResponse {
	return customerResponse{
		PublicID:    c.PublicID,
		Username:    c.Username,
		Name:        c.Name,
		Signature:   c.Signature,
		Email:       c.Email,
		Phone:       c.Phone,
		InviteCode:  c.InviteCode,
		Valid:       c.ValidAt.Valid,
		InviteCount: c.InviteCount,
		CreatedAt:   c.CreatedAt.Time,
	}
}

func (h *Handler) handleRegister(w http.ResponseWriter, r *http.Request) {
	var req registerRequest
	if !apierr.DecodeJSON(w, r, &req) {
		return
	}
	if !usernamePattern.MatchString(req.Username) {
		apierr.Write(w, apierr.InvalidArgument("username must be 3-32 chars of [a-zA-Z0-9_]"))
		return
	}
	if len(req.Password) < passwordMinLen || len(req.Password) > passwordMaxLen {
		apierr.Write(w, apierr.InvalidArgument("password must be 6-72 chars"))
		return
	}

	c, err := h.Customers.Register(r.Context(), req.Username, req.Password, req.InviteCode)
	if err != nil {
		apierr.Write(w, err)
		return
	}
	apierr.WriteJSON(w, http.StatusCreated, toCustomerResponse(c))
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type loginResponse struct {
	Token    string           `json:"token"`
	Customer customerResponse `json:"customer"`
}

func (h *Handler) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if !apierr.DecodeJSON(w, r, &req) {
		return
	}
	c, err := h.Customers.Login(r.Context(), req.Username, req.Password)
	if err != nil {
		apierr.Write(w, err)
		return
	}
	token, err := h.Tokens.Issue(c.ID, auth.AudienceCustomer, c.PasswordChangedAt.Time)
	if err != nil {
		apierr.Write(w, err)
		return
	}
	apierr.WriteJSON(w, http.StatusOK, loginResponse{Token: token, Customer: toCustomerResponse(c)})
}

// meResponse /me 在通用客户信息外附带有效邀请数（现算计数，仅本端点提供）。
type meResponse struct {
	customerResponse
	ValidInviteCount int64 `json:"valid_invite_count"`
}

func (h *Handler) handleMe(w http.ResponseWriter, r *http.Request) {
	c := customerFrom(r.Context())
	validInvites, err := h.Customers.CountValidInvitees(r.Context(), c.ID)
	if err != nil {
		apierr.Write(w, err)
		return
	}
	apierr.WriteJSON(w, http.StatusOK, meResponse{
		customerResponse: toCustomerResponse(c),
		ValidInviteCount: validInvites,
	})
}

type changePasswordRequest struct {
	OldPassword string `json:"old_password"`
	NewPassword string `json:"new_password"`
}

func (h *Handler) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	var req changePasswordRequest
	if !apierr.DecodeJSON(w, r, &req) {
		return
	}
	if len(req.NewPassword) < passwordMinLen || len(req.NewPassword) > passwordMaxLen {
		apierr.Write(w, apierr.InvalidArgument("new_password must be 6-72 chars"))
		return
	}
	if err := h.Customers.ChangePassword(r.Context(), customerFrom(r.Context()), req.OldPassword, req.NewPassword); err != nil {
		apierr.Write(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleRefresh 用仍有效的 token 换取新 token；pwd 戳取当前值。
func (h *Handler) handleRefresh(w http.ResponseWriter, r *http.Request) {
	c := customerFrom(r.Context())
	token, err := h.Tokens.Issue(c.ID, auth.AudienceCustomer, c.PasswordChangedAt.Time)
	if err != nil {
		apierr.Write(w, err)
		return
	}
	apierr.WriteJSON(w, http.StatusOK, map[string]string{"token": token})
}
