package task

import (
	"fmt"
	"time"

	"github.com/netfishx/gabon-go/internal/db"
	"github.com/netfishx/gabon-go/internal/tz"
)

// periodOf 返回某周期在 now（须为 Asia/Shanghai 时间）所在期的 key 与起点。
// key 形如 2026-07-06 / 2026-W28 / 2026-07；新周期无预生成、无重置 cron，
// 首个事件按 key 懒建进度行（与旧版行为等价，基线已定）。
func periodOf(p db.TaskPeriod, now time.Time) (key string, start time.Time) {
	switch p {
	case db.TaskPeriodDaily:
		start = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, tz.Shanghai)
		return now.Format("2006-01-02"), start
	case db.TaskPeriodWeekly:
		year, week := now.ISOWeek()
		// ISO 周从周一起：回退到本周一 00:00
		back := (int(now.Weekday()) + 6) % 7
		monday := now.AddDate(0, 0, -back)
		start = time.Date(monday.Year(), monday.Month(), monday.Day(), 0, 0, 0, 0, tz.Shanghai)
		return fmt.Sprintf("%d-W%02d", year, week), start
	case db.TaskPeriodMonthly:
		start = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, tz.Shanghai)
		return now.Format("2006-01"), start
	default:
		panic(fmt.Sprintf("unknown task period %q", p))
	}
}
