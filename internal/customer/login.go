package customer

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5"

	"github.com/netfishx/gabon-go/internal/apierr"
	"github.com/netfishx/gabon-go/internal/auth"
	"github.com/netfishx/gabon-go/internal/db"
)

func badCredentials() *apierr.Error {
	// 统一凭证错误：不泄露用户名是否存在
	return apierr.New(http.StatusUnauthorized, apierr.CodeAuthBadCredentials, "invalid username or password")
}

// Login 校验凭证并刷新最后登录时间；封禁客户不可登录。
func (s *Service) Login(ctx context.Context, username, password string) (*db.Customer, error) {
	c, err := s.q.GetCustomerByUsername(ctx, username)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, badCredentials()
	}
	if err != nil {
		return nil, fmt.Errorf("get customer by username: %w", err)
	}
	ok, err := auth.VerifyPassword(c.PasswordHash, password)
	if err != nil {
		return nil, fmt.Errorf("verify password: %w", err)
	}
	if !ok {
		return nil, badCredentials()
	}
	if c.Status == db.CustomerStatusBanned {
		return nil, apierr.New(http.StatusForbidden, apierr.CodeCustomerBanned, "account is banned")
	}
	if err := s.q.SetCustomerLastLogin(ctx, c.ID); err != nil {
		return nil, fmt.Errorf("set last login: %w", err)
	}
	return &c, nil
}

// GetByID 供鉴权中间件与 /me 使用。
func (s *Service) GetByID(ctx context.Context, id int64) (*db.Customer, error) {
	c, err := s.q.GetCustomerByID(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apierr.New(http.StatusUnauthorized, apierr.CodeAuthUnauthorized, "account not found")
	}
	if err != nil {
		return nil, fmt.Errorf("get customer by id: %w", err)
	}
	return &c, nil
}
