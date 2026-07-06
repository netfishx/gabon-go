package task

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/netfishx/gabon-go/internal/db"
	"github.com/netfishx/gabon-go/internal/tz"
)

// ClaimGroup 限时任务对查看者的三态分组。
type ClaimGroup string

// 三态分组取值。
const (
	GroupClaimable    ClaimGroup = "claimable"     // 未领取、窗口内、VIP 达标
	GroupClaimed      ClaimGroup = "claimed"       // 已有领取记录（任意状态）
	GroupNotClaimable ClaimGroup = "not_claimable" // 未领取但 VIP 不足或窗口外
)

// ClaimTaskView 客户面限时任务列表项（定义 + 查看者分组）。
type ClaimTaskView struct {
	Task  db.ListClaimTasksForCustomerRow
	Group ClaimGroup
}

// groupFor 依据查看者 VIP、当前时间与领取状态判定分组。
func groupFor(t db.ListClaimTasksForCustomerRow, vipLevel int32, now time.Time) ClaimGroup {
	if t.ClaimStatus.Valid {
		return GroupClaimed
	}
	inWindow := (!t.StartsAt.Valid || !now.Before(t.StartsAt.Time)) &&
		(!t.EndsAt.Valid || now.Before(t.EndsAt.Time))
	if inWindow && vipLevel >= t.MinVipLevel {
		return GroupClaimable
	}
	return GroupNotClaimable
}

// ListClaimTasks 可领取列表：三态分组排序（可领取 → 已领取 → 不可领取，组内 display_order）。
func (s *Service) ListClaimTasks(ctx context.Context, customerID int64, vipLevel int32) ([]ClaimTaskView, error) {
	rows, err := s.q.ListClaimTasksForCustomer(ctx, customerID)
	if err != nil {
		return nil, fmt.Errorf("list claim tasks: %w", err)
	}
	now := time.Now().In(tz.Shanghai)
	order := map[ClaimGroup]int{GroupClaimable: 0, GroupClaimed: 1, GroupNotClaimable: 2}
	var views []ClaimTaskView
	for _, r := range rows {
		views = append(views, ClaimTaskView{Task: r, Group: groupFor(r, vipLevel, now)})
	}
	// 稳定分组：SQL 已按 display_order 排序，此处按组序做稳定二次排序
	for i := 1; i < len(views); i++ {
		for j := i; j > 0 && order[views[j].Group] < order[views[j-1].Group]; j-- {
			views[j], views[j-1] = views[j-1], views[j]
		}
	}
	return views, nil
}

// GetClaimTaskDetail 任务详情 + 查看者领取状态。
func (s *Service) GetClaimTaskDetail(ctx context.Context, customerID, taskID int64) (db.GetClaimTaskForCustomerRow, error) {
	row, err := s.q.GetClaimTaskForCustomer(ctx, db.GetClaimTaskForCustomerParams{CustomerID: customerID, ID: taskID})
	if errors.Is(err, pgx.ErrNoRows) {
		return db.GetClaimTaskForCustomerRow{}, claimNotFound()
	}
	if err != nil {
		return db.GetClaimTaskForCustomerRow{}, fmt.Errorf("get claim task detail: %w", err)
	}
	return row, nil
}

// claimStatusesFor 我的领取记录 tab → 状态集合（text，跨 pgx 枚举数组编码限制）。
func claimStatusesFor(done bool) []string {
	if done {
		return []string{string(db.ClaimStatusRewarded), string(db.ClaimStatusExpired)}
	}
	return []string{string(db.ClaimStatusClaimed), string(db.ClaimStatusSubmitted), string(db.ClaimStatusRejected)}
}

// ListMyClaims 我的领取记录：done=false 进行中 / done=true 已完成。
func (s *Service) ListMyClaims(ctx context.Context, customerID int64, done bool) ([]db.ListMyClaimsRow, error) {
	rows, err := s.q.ListMyClaims(ctx, db.ListMyClaimsParams{
		CustomerID: customerID, Statuses: claimStatusesFor(done),
	})
	if err != nil {
		return nil, fmt.Errorf("list my claims: %w", err)
	}
	return rows, nil
}

// ExpireClaims 过期作废 {claimed, rejected}（cron 每 5 分钟 + 启动即跑一次）。
// 返回作废条数供日志。
func (s *Service) ExpireClaims(ctx context.Context) (int64, error) {
	n, err := s.q.ExpireClaims(ctx)
	if err != nil {
		return 0, fmt.Errorf("expire claims: %w", err)
	}
	return n, nil
}
