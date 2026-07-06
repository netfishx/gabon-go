// Package customer 客户域：注册、资料、邀请裂变、有效用户判定。
package customer

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
	"github.com/netfishx/gabon-go/internal/wallet"
)

// maxCodeRetries 短码碰撞重试上限；命中即视为随机源异常。
const maxCodeRetries = 5

// Service 客户域服务，直连 sqlc 生成的查询。
type Service struct {
	pool    *pgxpool.Pool
	q       *db.Queries
	wallets *wallet.Service // 邀请有效奖励入账走钱包域原语（依赖方向：功能域 → wallet）

	// 短码生成器：可注入以便测试强制碰撞，默认 crypto/rand 实现
	genPublicID   func() (string, error)
	genInviteCode func() (string, error)
}

// NewService 构造客户域服务。
func NewService(pool *pgxpool.Pool, wallets *wallet.Service) *Service {
	return &Service{
		pool:          pool,
		q:             db.New(pool),
		wallets:       wallets,
		genPublicID:   newPublicID,
		genInviteCode: newInviteCode,
	}
}

// Register 注册客户：同一事务内写入客户（含邀请关系与祖先路径）与零余额钱包，
// 并为邀请人总邀请数 +1（注册即计，不论被邀请人是否有效）。
func (s *Service) Register(ctx context.Context, username, password, inviteCode string) (*db.Customer, error) {
	var inviterID *int64
	ancestors := []int64{}
	if inviteCode != "" {
		inviter, err := s.q.GetCustomerByInviteCode(ctx, inviteCode)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, apierr.New(http.StatusBadRequest, apierr.CodeCustomerInviteCodeInvalid, "invite code does not exist")
		}
		if err != nil {
			return nil, fmt.Errorf("resolve invite code: %w", err)
		}
		inviterID = &inviter.ID
		ancestors = append(inviter.Ancestors, inviter.ID)
	}

	passwordHash, err := auth.HashPassword(password)
	if err != nil {
		return nil, fmt.Errorf("hash password: %w", err)
	}

	for range maxCodeRetries {
		publicID, err := s.genPublicID()
		if err != nil {
			return nil, err
		}
		newCode, err := s.genInviteCode()
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
				InviteCode:   newCode,
				InviterID:    inviterID,
				Ancestors:    ancestors,
			})
			if err != nil {
				return err
			}
			if err := q.CreateWallet(ctx, c.ID); err != nil {
				return err
			}
			if inviterID != nil {
				if err := q.IncrementInviteCount(ctx, *inviterID); err != nil {
					return err
				}
				// 邀请人总邀请数刚 +1，可能因此凑齐有效用户条件
				if _, err := s.MarkValidIfQualifiedTx(ctx, tx, *inviterID); err != nil {
					return err
				}
			}
			created = c
			return nil
		})
		if err == nil {
			return &created, nil
		}

		switch db.UniqueViolationConstraint(err) {
		case "customers_username_key":
			return nil, apierr.New(http.StatusConflict, apierr.CodeCustomerUsernameTaken, "username already taken")
		case "customers_public_id_key", "customers_invite_code_key":
			continue // 短码碰撞，重新生成
		}
		return nil, fmt.Errorf("register customer: %w", err)
	}
	return nil, fmt.Errorf("register customer: exhausted short code retries")
}
