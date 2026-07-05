package customer

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// MarkValidIfQualifiedTx 在调用方事务内做有效用户判定：
// 三条件（有作品、有成功邀请、有联系方式）由一条原子条件 UPDATE 完成，
// valid_at 的 CAS 保证只翻转一次、永不回退。返回本次是否翻转。
// 触发点：注册（对邀请人）、资料修改写联系方式（对本人）、视频审核通过（对作者，装配层回调注入）。
func (s *Service) MarkValidIfQualifiedTx(ctx context.Context, tx pgx.Tx, customerID int64) (bool, error) {
	rows, err := s.q.WithTx(tx).MarkCustomerValidIfQualified(ctx, customerID)
	if err != nil {
		return false, fmt.Errorf("mark valid if qualified: %w", err)
	}
	return rows == 1, nil
}

// CountValidInvitees 有效邀请数：该客户名下已翻转为有效用户的被邀请人数量。
// 注意与 customers.invite_count（总邀请数，注册即算）语义区分。
func (s *Service) CountValidInvitees(ctx context.Context, inviterID int64) (int64, error) {
	n, err := s.q.CountValidInvitees(ctx, &inviterID)
	if err != nil {
		return 0, fmt.Errorf("count valid invitees: %w", err)
	}
	return n, nil
}
