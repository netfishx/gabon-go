package task

import (
	"testing"
	"time"

	"github.com/netfishx/gabon-go/internal/db"
	"github.com/netfishx/gabon-go/internal/tz"
)

// periodOf 纯逻辑表驱动：三周期 key 与起点，含 ISO 周跨年与月首边界。
func TestPeriodOf(t *testing.T) {
	at := func(y int, m time.Month, d, hh int) time.Time {
		return time.Date(y, m, d, hh, 30, 0, 0, tz.Shanghai)
	}
	tests := []struct {
		name      string
		period    db.TaskPeriod
		now       time.Time
		wantKey   string
		wantStart time.Time
	}{
		{"daily_plain", db.TaskPeriodDaily, at(2026, 7, 6, 15), "2026-07-06", at(2026, 7, 6, 0).Truncate(0)},
		{"daily_midnight", db.TaskPeriodDaily, time.Date(2026, 7, 6, 0, 0, 0, 0, tz.Shanghai), "2026-07-06", time.Date(2026, 7, 6, 0, 0, 0, 0, tz.Shanghai)},
		{"weekly_monday", db.TaskPeriodWeekly, at(2026, 7, 6, 9), "2026-W28", time.Date(2026, 7, 6, 0, 0, 0, 0, tz.Shanghai)},   // 2026-07-06 是周一
		{"weekly_sunday", db.TaskPeriodWeekly, at(2026, 7, 12, 23), "2026-W28", time.Date(2026, 7, 6, 0, 0, 0, 0, tz.Shanghai)}, // 周日仍属同一 ISO 周
		// ISO 周跨年：2026-12-28（周一）起是 2026-W53，延伸到 2027-01-03
		{"weekly_year_boundary", db.TaskPeriodWeekly, at(2027, 1, 1, 12), "2026-W53", time.Date(2026, 12, 28, 0, 0, 0, 0, tz.Shanghai)},
		// ISO 周跨年反向：2024-12-30（周一）属 2025-W01
		{"weekly_iso_year_ahead", db.TaskPeriodWeekly, at(2024, 12, 31, 8), "2025-W01", time.Date(2024, 12, 30, 0, 0, 0, 0, tz.Shanghai)},
		{"monthly_plain", db.TaskPeriodMonthly, at(2026, 7, 31, 23), "2026-07", time.Date(2026, 7, 1, 0, 0, 0, 0, tz.Shanghai)},
		{"monthly_first_day", db.TaskPeriodMonthly, time.Date(2026, 2, 1, 0, 0, 0, 0, tz.Shanghai), "2026-02", time.Date(2026, 2, 1, 0, 0, 0, 0, tz.Shanghai)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key, start := periodOf(tt.period, tt.now)
			if key != tt.wantKey {
				t.Errorf("key = %s, want %s", key, tt.wantKey)
			}
			wantStart := time.Date(tt.wantStart.Year(), tt.wantStart.Month(), tt.wantStart.Day(), 0, 0, 0, 0, tz.Shanghai)
			if !start.Equal(wantStart) {
				t.Errorf("start = %v, want %v", start, wantStart)
			}
		})
	}
}
