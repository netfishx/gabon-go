package customer

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/netfishx/gabon-go/internal/apierr"
	"github.com/netfishx/gabon-go/internal/db"
)

// ProfileUpdate 资料修改参数：nil = 该字段不更新（不支持清除已填联系方式）。
type ProfileUpdate struct {
	Name      *string
	Signature *string
	Email     *string
	Phone     *string
}

// UpdateProfile 更新客户资料。email 写入侧归一为全小写；
// 联系方式唯一性由部分唯一索引兜底，撞约束映射为明确冲突错误码。
// 写入联系方式时在同一事务内对本人做有效用户判定（M4 触发点之一）。
func (s *Service) UpdateProfile(ctx context.Context, customerID int64, p ProfileUpdate) (*db.Customer, error) {
	if p.Email != nil {
		lower := strings.ToLower(*p.Email)
		p.Email = &lower
	}
	var c db.Customer
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var err error
		c, err = s.q.WithTx(tx).UpdateCustomerProfile(ctx, db.UpdateCustomerProfileParams{
			ID:        customerID,
			Name:      p.Name,
			Signature: p.Signature,
			Email:     p.Email,
			Phone:     p.Phone,
		})
		if err != nil {
			return err
		}
		if p.Email != nil || p.Phone != nil {
			if _, err := s.MarkValidIfQualifiedTx(ctx, tx, customerID); err != nil {
				return err
			}
		}
		return nil
	})
	switch db.UniqueViolationConstraint(err) {
	case "customers_phone_key":
		return nil, apierr.New(http.StatusConflict, apierr.CodeCustomerPhoneTaken, "phone already bound to another account")
	case "customers_email_key":
		return nil, apierr.New(http.StatusConflict, apierr.CodeCustomerEmailTaken, "email already bound to another account")
	}
	if err != nil {
		return nil, fmt.Errorf("update profile: %w", err)
	}
	return &c, nil
}
