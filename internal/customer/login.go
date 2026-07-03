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

// ChangePassword 校验旧密码后更新哈希并刷新 password_changed_at——
// pwd 戳随之变化，所有旧 token 立即失效（改密踢下线）。
func (s *Service) ChangePassword(ctx context.Context, c *db.Customer, oldPassword, newPassword string) error {
	ok, err := auth.VerifyPassword(c.PasswordHash, oldPassword)
	if err != nil {
		return fmt.Errorf("verify old password: %w", err)
	}
	if !ok {
		return badCredentials()
	}
	newHash, err := auth.HashPassword(newPassword)
	if err != nil {
		return fmt.Errorf("hash new password: %w", err)
	}
	if err := s.q.UpdateCustomerPassword(ctx, db.UpdateCustomerPasswordParams{
		ID: c.ID, PasswordHash: newHash,
	}); err != nil {
		return fmt.Errorf("update password: %w", err)
	}
	return nil
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
