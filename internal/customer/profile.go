package customer

import (
	"context"
	"fmt"
	"net/http"
	"strings"

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
func (s *Service) UpdateProfile(ctx context.Context, customerID int64, p ProfileUpdate) (*db.Customer, error) {
	if p.Email != nil {
		lower := strings.ToLower(*p.Email)
		p.Email = &lower
	}
	c, err := s.q.UpdateCustomerProfile(ctx, db.UpdateCustomerProfileParams{
		ID:        customerID,
		Name:      p.Name,
		Signature: p.Signature,
		Email:     p.Email,
		Phone:     p.Phone,
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
