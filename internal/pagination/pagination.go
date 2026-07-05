// Package pagination 列表端点共用的 limit/cursor 查询参数解析。
package pagination

import (
	"net/http"
	"strconv"

	"github.com/netfishx/gabon-go/internal/apierr"
)

// Limit 解析 ?limit=，缺省用 def，超上限钳制到 max。
func Limit(r *http.Request, def, maxLimit int32) (int32, error) {
	raw := r.URL.Query().Get("limit")
	if raw == "" {
		return def, nil
	}
	n, err := strconv.ParseInt(raw, 10, 32)
	if err != nil || n <= 0 {
		return 0, apierr.InvalidArgument("limit must be a positive integer")
	}
	return min(int32(n), maxLimit), nil
}

// Cursor 解析 ?cursor= 为 int64 游标（0 = 第一页）。
func Cursor(r *http.Request) (int64, error) {
	raw := r.URL.Query().Get("cursor")
	if raw == "" {
		return 0, nil
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n < 0 {
		return 0, apierr.InvalidArgument("cursor must be a non-negative integer")
	}
	return n, nil
}

// Page 类型化分页响应：所有 int64 游标列表端点统一形态。
type Page[T any] struct {
	Items      []T   `json:"items"`
	NextCursor int64 `json:"next_cursor,omitempty"`
}
