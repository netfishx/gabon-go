// Package report 后台报表：DAU 打点与（后续里程碑的）聚合查询。
package report

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/netfishx/gabon-go/internal/db"
)

// 时区锚点 Asia/Shanghai：活跃按此时区记天（main 已嵌入 tzdata 兜底）。
var shanghai = mustLoadLocation("Asia/Shanghai")

func mustLoadLocation(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		panic(fmt.Sprintf("load location %s: %v", name, err))
	}
	return loc
}

type Service struct {
	q *db.Queries
}

func NewService(pool *pgxpool.Pool) *Service {
	return &Service{q: db.New(pool)}
}

// RecordActive 记录客户当日活跃（幂等：每客户每日一行）。
func (s *Service) RecordActive(ctx context.Context, customerID int64) error {
	today := time.Now().In(shanghai)
	return s.q.UpsertDailyActive(ctx, db.UpsertDailyActiveParams{
		CustomerID: customerID,
		ActiveDate: pgtype.Date{Time: today, Valid: true},
	})
}
