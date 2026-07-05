// Package tz 时区锚点 Asia/Shanghai（CLAUDE.md：周期任务重置、榜单结算、按日语义依赖它）。
// blank import 嵌入 tzdata：任何使用方（含测试二进制）都不依赖宿主 zoneinfo。
package tz

import (
	"fmt"
	"time"

	// 嵌入时区数据：包初始化即加载 Asia/Shanghai，依赖边保证 tzdata 先注册，
	// 无系统 zoneinfo 的最小容器与测试二进制均不受影响
	_ "time/tzdata"
)

// Shanghai 全局时区锚点。
var Shanghai = mustLoad("Asia/Shanghai")

func mustLoad(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		panic(fmt.Sprintf("load location %s: %v", name, err))
	}
	return loc
}

// Today 当前 Asia/Shanghai 日期（按日语义统一入口）。
func Today() time.Time {
	return time.Now().In(Shanghai)
}
