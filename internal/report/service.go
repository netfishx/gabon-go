// Package report 后台报表：DAU 打点与（后续里程碑的）聚合查询。
package report

import (
	"context"
	"fmt"
	"sync"
	"time"

	// 嵌入时区数据：本包在包初始化期即加载 Asia/Shanghai，blank import 建立依赖边，
	// 保证 tzdata 先注册——无系统 zoneinfo 的最小容器与测试二进制都不受影响。
	_ "time/tzdata"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/netfishx/gabon-go/internal/db"
)

// 时区锚点 Asia/Shanghai：活跃按此时区记天。
var shanghai = mustLoadLocation("Asia/Shanghai")

func mustLoadLocation(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		panic(fmt.Sprintf("load location %s: %v", name, err))
	}
	return loc
}

// Service 报表域服务。
type Service struct {
	q *db.Queries

	// 进程内"今日已记"缓存：鉴权中间件每请求打点，避免同客户同日重复 upsert 空转。
	// 跨日翻转时整体清空，容量以单日活跃客户数为界；进程重启后最坏多付一次幂等 insert。
	mu      sync.Mutex
	seenDay string
	seen    map[int64]struct{}
}

// NewService 构造报表域服务。
func NewService(pool *pgxpool.Pool) *Service {
	return &Service{q: db.New(pool), seen: make(map[int64]struct{})}
}

// RecordActive 记录客户当日活跃（幂等：每客户每日一行）。
func (s *Service) RecordActive(ctx context.Context, customerID int64) error {
	today := time.Now().In(shanghai)
	day := today.Format(time.DateOnly)

	s.mu.Lock()
	if s.seenDay != day {
		s.seenDay = day
		clear(s.seen)
	}
	if _, ok := s.seen[customerID]; ok {
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()

	err := s.q.UpsertDailyActive(ctx, db.UpsertDailyActiveParams{
		CustomerID: customerID,
		ActiveDate: pgtype.Date{Time: today, Valid: true},
	})
	if err != nil {
		return err
	}

	s.mu.Lock()
	if s.seenDay == day {
		s.seen[customerID] = struct{}{}
	}
	s.mu.Unlock()
	return nil
}
