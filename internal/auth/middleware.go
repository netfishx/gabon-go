package auth

import (
	"context"
	"net/http"
	"time"

	"github.com/netfishx/gabon-go/internal/apierr"
)

// PasswordStamp 把 password_changed_at 归一为 pwd 戳（Unix 微秒）。
// 全库唯一的转换点：签发与校验两侧都经由此函数，保证口径一致。
func PasswordStamp(passwordChangedAt time.Time) int64 {
	return passwordChangedAt.UnixMicro()
}

// SubjectMiddleware 双主体共用的鉴权中间件（docs/skeleton.md："customer/admin 双主体一套实现"）。
// 固定序列：解析 Bearer → 点查主体 → 比对 pwd 戳（改密踢下线）→ 状态校验（封禁/禁用即时生效）→ 注入 ctx。
// 两面只提供各自的主体装载、取戳、状态校验与注入方式。
type SubjectMiddleware[T any] struct {
	Tokens   *TokenIssuer
	Audience string
	Load     func(ctx context.Context, id int64) (T, error) // 点查主体；缺失时返回 apierr
	Stamp    func(subject T) int64                          // 主体当前 pwd 戳
	Check    func(subject T) error                          // 状态校验；拒绝时返回 apierr
	Inject   func(ctx context.Context, subject T) context.Context
}

// Middleware 返回 chi 兼容的中间件。
func (m SubjectMiddleware[T]) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, pwdStamp, err := m.Tokens.FromRequest(r, m.Audience)
		if err != nil {
			apierr.Write(w, apierr.Unauthorized())
			return
		}
		subject, err := m.Load(r.Context(), id)
		if err != nil {
			apierr.Write(w, err)
			return
		}
		if pwdStamp != m.Stamp(subject) {
			apierr.Write(w, apierr.Unauthorized())
			return
		}
		if err := m.Check(subject); err != nil {
			apierr.Write(w, err)
			return
		}
		next.ServeHTTP(w, r.WithContext(m.Inject(r.Context(), subject)))
	})
}
