package payment

import (
	"context"
	"errors"
	"testing"
)

// fakeProvider 单测替身：可配 code 与支持的支付方式。
type fakeProvider struct {
	code    string
	methods []string
}

func (f fakeProvider) Code() string               { return f.code }
func (f fakeProvider) SupportedMethods() []string { return f.methods }
func (fakeProvider) Pay(context.Context, PayCommand) (*PayResult, error) {
	return &PayResult{}, nil
}

func (fakeProvider) Withdraw(context.Context, WithdrawCommand) (*WithdrawResult, error) {
	return &WithdrawResult{}, nil
}

func (fakeProvider) ParseCallback(*CallbackRequest) (*CallbackResult, error) {
	return &CallbackResult{}, nil
}

func (fakeProvider) Query(context.Context, OrderView) (*QueryResult, error) {
	return &QueryResult{}, nil
}

func TestNewRegistryRejectsDuplicateCode(t *testing.T) {
	_, err := NewRegistry(
		fakeProvider{code: "pay126", methods: []string{"WECHAT"}},
		fakeProvider{code: "pay126", methods: []string{"ALIPAY"}},
	)
	if err == nil {
		t.Fatal("expected duplicate code to fail registry construction")
	}
}

func TestRegistryByCode(t *testing.T) {
	reg, err := NewRegistry(
		fakeProvider{code: "mock", methods: []string{"mock"}},
		fakeProvider{code: "pay126", methods: []string{"WECHAT"}},
	)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}

	if p := reg.ByCode("pay126"); p == nil || p.Code() != "pay126" {
		t.Fatalf("ByCode(pay126) = %v, want pay126", p)
	}
	if p := reg.ByCode("nope"); p != nil {
		t.Fatalf("ByCode(nope) = %v, want nil", p)
	}
}

func TestRegistryProviderForMethod(t *testing.T) {
	reg, err := NewRegistry(
		fakeProvider{code: "mock", methods: []string{"mock"}},
		fakeProvider{code: "pay126", methods: []string{"WECHAT", "ALIPAY"}},
	)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}

	p, err := reg.ProviderFor("ALIPAY")
	if err != nil {
		t.Fatalf("ProviderFor(ALIPAY): %v", err)
	}
	if p.Code() != "pay126" {
		t.Fatalf("ProviderFor(ALIPAY) = %s, want pay126", p.Code())
	}

	if _, err := reg.ProviderFor("UNSUPPORTED"); !errors.Is(err, ErrNoProviderForMethod) {
		t.Fatalf("ProviderFor(UNSUPPORTED) err = %v, want ErrNoProviderForMethod", err)
	}
}

func TestRegistryProviderForReturnsFirstRegistered(t *testing.T) {
	// 契约：多渠道支持同一 method 时，ProviderFor 按注册顺序返回首个命中者。
	reg, err := NewRegistry(
		fakeProvider{code: "first", methods: []string{"WECHAT"}},
		fakeProvider{code: "second", methods: []string{"WECHAT"}},
	)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	p, err := reg.ProviderFor("WECHAT")
	if err != nil {
		t.Fatalf("ProviderFor(WECHAT): %v", err)
	}
	if p.Code() != "first" {
		t.Fatalf("ProviderFor(WECHAT) = %s, want first (registration order)", p.Code())
	}
}
