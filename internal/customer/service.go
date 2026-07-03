// Package customer 客户域：注册、资料、邀请裂变、有效用户判定。
package customer

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/netfishx/gabon-go/internal/apierr"
	"github.com/netfishx/gabon-go/internal/auth"
	"github.com/netfishx/gabon-go/internal/db"
)

const (
	pgUniqueViolation = "23505"
	// 短码碰撞重试上限；命中即视为随机源异常
	maxCodeRetries = 5
)

type Service struct {
	pool *pgxpool.Pool
	q    *db.Queries
}

func NewService(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool, q: db.New(pool)}
}

// Register 注册客户：同一事务内写入客户与零余额钱包。
func (s *Service) Register(ctx context.Context, username, password string) (*db.Customer, error) {
	passwordHash, err := auth.HashPassword(password)
	if err != nil {
		return nil, fmt.Errorf("hash password: %w", err)
	}

	for range maxCodeRetries {
		publicID, err := newPublicID()
		if err != nil {
			return nil, err
		}
		inviteCode, err := newInviteCode()
		if err != nil {
			return nil, err
		}

		var created db.Customer
		err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
			q := s.q.WithTx(tx)
			c, err := q.CreateCustomer(ctx, db.CreateCustomerParams{
				PublicID:     publicID,
				Username:     username,
				PasswordHash: passwordHash,
				InviteCode:   inviteCode,
				Ancestors:    []int64{},
			})
			if err != nil {
				return err
			}
			if err := q.CreateWallet(ctx, c.ID); err != nil {
				return err
			}
			created = c
			return nil
		})
		if err == nil {
			return &created, nil
		}

		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolation {
			switch pgErr.ConstraintName {
			case "customers_username_key":
				return nil, apierr.New(http.StatusConflict, apierr.CodeCustomerUsernameTaken, "username already taken")
			case "customers_public_id_key", "customers_invite_code_key":
				continue // 短码碰撞，重新生成
			}
		}
		return nil, fmt.Errorf("register customer: %w", err)
	}
	return nil, fmt.Errorf("register customer: exhausted short code retries")
}
