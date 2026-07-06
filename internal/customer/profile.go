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
// AvatarPath 的归属/存在校验由 api 层完成（需要对象存储访问）。
type ProfileUpdate struct {
	Name       *string
	Signature  *string
	Email      *string
	Phone      *string
	AvatarPath *string
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
		q := s.q.WithTx(tx)
		var err error
		c, err = q.UpdateCustomerProfile(ctx, db.UpdateCustomerProfileParams{
			ID:         customerID,
			Name:       p.Name,
			Signature:  p.Signature,
			Email:      p.Email,
			Phone:      p.Phone,
			AvatarPath: p.AvatarPath,
		})
		if err != nil {
			return err
		}
		if p.Email != nil || p.Phone != nil {
			flipped, err := s.MarkValidIfQualifiedTx(ctx, tx, customerID)
			if err != nil {
				return err
			}
			if flipped {
				// 本次写入恰好补齐最后一个条件：重读客户行，响应须反映刚翻转的 valid_at
				c, err = q.GetCustomerByID(ctx, customerID)
				if err != nil {
					return fmt.Errorf("reload customer after flip: %w", err)
				}
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
