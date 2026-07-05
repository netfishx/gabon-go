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
func (s *Service) ListTeamMembers(ctx context.Context, viewer *db.Customer, parentPublicID string, cursor int64, limit int32) (items []db.ListTeamMembersRow, next int64, err error) {
	parentID, err := s.resolveTeamParent(ctx, viewer, parentPublicID)
	if err != nil {
		return nil, 0, err
	}
	items, err = s.q.ListTeamMembers(ctx, db.ListTeamMembersParams{
		ParentID: &parentID, Cursor: cursor, RowLimit: limit + 1,
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

// resolveTeamParent 校验并解析下钻 parent 的越权守卫。
func (s *Service) resolveTeamParent(ctx context.Context, viewer *db.Customer, parentPublicID string) (int64, error) {
	if parentPublicID == "" || parentPublicID == viewer.PublicID {
		return viewer.ID, nil
	}
	parent, err := s.q.GetCustomerByPublicID(ctx, parentPublicID)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, apierr.New(http.StatusNotFound, apierr.CodeCustomerNotFound, "customer not found")
	}
	if err != nil {
		return 0, fmt.Errorf("resolve team parent: %w", err)
	}
	depth := relativeDepth(parent.Ancestors, viewer.ID)
	if depth < 1 || depth > maxParentDepth {
		return 0, teamForbidden()
	}
	return parent.ID, nil
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
