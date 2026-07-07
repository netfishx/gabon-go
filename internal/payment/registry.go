package payment

import (
	"errors"
	"fmt"
	"slices"
)

// ErrNoProviderForMethod 表示无任何已注册渠道支持该支付方式。
var ErrNoProviderForMethod = errors.New("payment: no provider supports method")

// Registry 支付渠道注册表：按 Code 建不可变索引，构造后只读、并发安全。
// 重复 code 在构造期即 fail-fast（配置错误尽早暴露，PRD #63 用户故事 24）。
type Registry struct {
	ordered []Provider          // 注册顺序（ProviderFor 按此返回首个命中者）
	byCode  map[string]Provider // code → provider
}

// NewRegistry 构造注册表；任一 code 重复即返错（app 装配处 fail-fast）。
func NewRegistry(providers ...Provider) (*Registry, error) {
	r := &Registry{
		ordered: make([]Provider, 0, len(providers)),
		byCode:  make(map[string]Provider, len(providers)),
	}
	for _, p := range providers {
		code := p.Code()
		if _, dup := r.byCode[code]; dup {
			return nil, fmt.Errorf("payment: duplicate provider code %q", code)
		}
		r.byCode[code] = p
		r.ordered = append(r.ordered, p)
	}
	return r, nil
}

// ByCode 按 code 取渠道；未注册返回 nil（回调路由 provider 未知时用）。
func (r *Registry) ByCode(code string) Provider {
	return r.byCode[code]
}

// ProviderFor 按支付方式选渠道：注册顺序首个 SupportedMethods 命中者。
// 契约：多渠道支持同一 method 时取首个（渠道选择策略点留待 #69+）。
func (r *Registry) ProviderFor(method string) (Provider, error) {
	for _, p := range r.ordered {
		if slices.Contains(p.SupportedMethods(), method) {
			return p, nil
		}
	}
	return nil, fmt.Errorf("%w: %q", ErrNoProviderForMethod, method)
}
