// Package admin 后台面：管理员账号、登录与（后续里程碑的）运营功能。
package admin

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/netfishx/gabon-go/internal/apierr"
	"github.com/netfishx/gabon-go/internal/auth"
	"github.com/netfishx/gabon-go/internal/db"
)

// Service 后台域服务。
type Service struct {
	q *db.Queries
}

// NewService 构造后台域服务。
func NewService(pool *pgxpool.Pool) *Service {
	return &Service{q: db.New(pool)}
}

// Bootstrap 在 admins 表为空时用配置凭据创建首个管理员（ADMIN 角色）。
func (s *Service) Bootstrap(ctx context.Context, username, password string) error {
	if username == "" || password == "" {
		return nil
	}
	n, err := s.q.CountAdmins(ctx)
	if err != nil {
		return fmt.Errorf("count admins: %w", err)
	}
	if n > 0 {
		return nil
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		return fmt.Errorf("hash bootstrap password: %w", err)
	}
	if _, err := s.q.CreateAdmin(ctx, db.CreateAdminParams{
		Username: username, PasswordHash: hash, Role: db.AdminRoleAdmin,
	}); err != nil {
		// count-then-create 与并发启动的实例竞态时，用户名冲突即"对方已完成引导"，幂等成功
		if db.UniqueViolationConstraint(err) == "admins_username_key" {
			return nil
		}
		return fmt.Errorf("create bootstrap admin: %w", err)
	}
	return nil
}

// Login 校验管理员凭证；禁用账号不可登录。
func (s *Service) Login(ctx context.Context, username, password string) (*db.Admin, error) {
	a, err := s.q.GetAdminByUsername(ctx, username)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apierr.New(http.StatusUnauthorized, apierr.CodeAuthBadCredentials, "invalid username or password")
	}
	if err != nil {
		return nil, fmt.Errorf("get admin by username: %w", err)
	}
	ok, err := auth.VerifyPassword(a.PasswordHash, password)
	if err != nil {
		return nil, fmt.Errorf("verify password: %w", err)
	}
	if !ok {
		return nil, apierr.New(http.StatusUnauthorized, apierr.CodeAuthBadCredentials, "invalid username or password")
	}
	if a.Status == db.AdminStatusDisabled {
		return nil, apierr.New(http.StatusForbidden, apierr.CodeAdminDisabled, "admin account is disabled")
	}
	if err := s.q.SetAdminLastLogin(ctx, a.ID); err != nil {
		return nil, fmt.Errorf("set last login: %w", err)
	}
	return &a, nil
}

// GetByID 供后台鉴权中间件与 /me 使用。
func (s *Service) GetByID(ctx context.Context, id int64) (*db.Admin, error) {
	a, err := s.q.GetAdminByID(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apierr.New(http.StatusUnauthorized, apierr.CodeAuthUnauthorized, "account not found")
	}
	if err != nil {
		return nil, fmt.Errorf("get admin by id: %w", err)
	}
	return &a, nil
}
