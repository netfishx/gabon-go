package customer

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5"

	"github.com/netfishx/gabon-go/internal/apierr"
	"github.com/netfishx/gabon-go/internal/db"
)

// maxParentDepth 下钻 parent 最深为团队内深度 2 的成员——
// 团队 = 向下三级以内（CONTEXT.md），parent 深度 ≤2 保证列出的成员深度 ≤3。
const maxParentDepth = 2

func teamForbidden() *apierr.Error {
	return apierr.New(http.StatusForbidden, apierr.CodeCustomerTeamForbidden, "parent is outside your team scope")
}

// ListTeamMembers 团队下级列表：parentPublicID 为空 = 查看者本人的直接下级；
// 指定时必须是本人或团队内深度 ≤2 的成员，越权返回 403（不复刻旧版静默空列表）。
// parent 已到深度 2 时成员为深度 3，其下级在团队之外——计数归 0，不泄漏界外结构。
func (s *Service) ListTeamMembers(ctx context.Context, viewer *db.Customer, parentPublicID string, cursor int64, limit int32) (items []db.ListTeamMembersRow, next int64, err error) {
	parentID, parentDepth, err := s.resolveTeamParent(ctx, viewer, parentPublicID)
	if err != nil {
		return nil, 0, err
	}
	items, err = s.q.ListTeamMembers(ctx, db.ListTeamMembersParams{
		CountSubordinates: parentDepth < maxParentDepth,
		ParentID:          &parentID,
		Cursor:            cursor,
		RowLimit:          limit + 1,
	})
	if err != nil {
		return nil, 0, fmt.Errorf("list team members: %w", err)
	}
	if len(items) > int(limit) {
		items = items[:limit]
		next = items[len(items)-1].ID
	}
	return items, next, nil
}

// resolveTeamParent 校验并解析下钻 parent 的越权守卫；返回 parent 相对查看者的深度（本人 = 0）。
func (s *Service) resolveTeamParent(ctx context.Context, viewer *db.Customer, parentPublicID string) (int64, int, error) {
	if parentPublicID == "" || parentPublicID == viewer.PublicID {
		return viewer.ID, 0, nil
	}
	parent, err := s.q.GetCustomerByPublicID(ctx, parentPublicID)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, 0, apierr.New(http.StatusNotFound, apierr.CodeCustomerNotFound, "customer not found")
	}
	if err != nil {
		return 0, 0, fmt.Errorf("resolve team parent: %w", err)
	}
	depth := relativeDepth(parent.Ancestors, viewer.ID)
	if depth < 1 || depth > maxParentDepth {
		return 0, 0, teamForbidden()
	}
	return parent.ID, depth, nil
}

// relativeDepth 目标相对 viewer 的邀请深度（自物化祖先路径推算）；
// viewer 不在其祖先路径上返回 0。
func relativeDepth(ancestors []int64, viewerID int64) int {
	for i, id := range ancestors {
		if id == viewerID {
			return len(ancestors) - i
		}
	}
	return 0
}

// teamDepthLevels 团队汇总恒定返回深度 1–3 三层（空层补零）。
const teamDepthLevels = 3

// TeamLayer 团队某一深度的聚合（HTTP 序列化由 api 层 DTO 承担）。
type TeamLayer struct {
	Depth      int
	Count      int64
	ValidCount int64
}

// TeamSummary 团队汇总：各级人数/有效人数、总人数、查看者累计邀请奖励。
type TeamSummary struct {
	Total             int64
	TotalValid        int64
	InviteRewardTotal int64
	Layers            []TeamLayer
}

// GetTeamSummary 聚合查看者团队（3 级以内）与累计邀请奖励（流水现算，无缓存）。
func (s *Service) GetTeamSummary(ctx context.Context, viewerID int64) (*TeamSummary, error) {
	rows, err := s.q.TeamSummaryByDepth(ctx, viewerID)
	if err != nil {
		return nil, fmt.Errorf("team summary by depth: %w", err)
	}
	sum := &TeamSummary{Layers: make([]TeamLayer, teamDepthLevels)}
	for i := range sum.Layers {
		sum.Layers[i].Depth = i + 1
	}
	for _, r := range rows {
		l := &sum.Layers[r.Depth-1]
		l.Count = r.MemberCount
		l.ValidCount = r.ValidCount
		sum.Total += r.MemberCount
		sum.TotalValid += r.ValidCount
	}
	reward, err := s.q.SumInviteRewards(ctx, viewerID)
	if err != nil {
		return nil, fmt.Errorf("sum invite rewards: %w", err)
	}
	sum.InviteRewardTotal = reward
	return sum, nil
}
