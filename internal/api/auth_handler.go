package api

import (
	"net/http"
	"regexp"
	"time"

	"github.com/netfishx/gabon-go/internal/apierr"
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
	PublicID   string    `json:"public_id"`
	Username   string    `json:"username"`
	InviteCode string    `json:"invite_code"`
	CreatedAt  time.Time `json:"created_at"`
}

func toCustomerResponse(c *db.Customer) customerResponse {
	return customerResponse{
		PublicID:   c.PublicID,
		Username:   c.Username,
		InviteCode: c.InviteCode,
		CreatedAt:  c.CreatedAt.Time,
	}
}

func (h *Handler) handleRegister(w http.ResponseWriter, r *http.Request) {
	var req registerRequest
	if !decodeJSON(w, r, &req) {
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
	writeJSON(w, http.StatusCreated, toCustomerResponse(c))
}
