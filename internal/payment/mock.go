package payment

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
)

// MockProviderCode 是内置 mock 渠道的 code（dev/test 注册，支撑本地 E2E 全链路）。
const MockProviderCode = "mock"

// mockProvider 内置假渠道：Pay/Withdraw 同步返回 pending + 伪单号；
// ParseCallback 不做真实验签，按表单字段解析，读显式 valid=false 模拟坏签。
type mockProvider struct{}

// NewMockProvider 构造内置 mock 渠道。
func NewMockProvider() Provider { return mockProvider{} }

func (mockProvider) Code() string { return MockProviderCode }

func (mockProvider) SupportedMethods() []string { return []string{"mock"} }

func (mockProvider) Pay(_ context.Context, cmd PayCommand) (*PayResult, error) {
	pon := "MOCK-" + cmd.Order.OrderNo
	redirect := "https://mock.pay.local/checkout/" + cmd.Order.OrderNo
	req, _ := json.Marshal(map[string]any{
		"order_no":   cmd.Order.OrderNo,
		"amount":     cmd.Order.FiatAmount,
		"method":     cmd.Order.PaymentMethod,
		"notify_url": cmd.NotifyURL,
	})
	resp, _ := json.Marshal(map[string]any{
		"provider_order_no": pon,
		"redirect_url":      redirect,
		"status":            "pending",
	})
	return &PayResult{
		ProviderOrderNo: pon,
		RedirectURL:     redirect,
		ProviderStatus:  "pending",
		RawRequest:      req,
		RawResponse:     resp,
	}, nil
}

func (mockProvider) Withdraw(_ context.Context, cmd WithdrawCommand) (*WithdrawResult, error) {
	pon := "MOCKW-" + cmd.Order.OrderNo
	req, _ := json.Marshal(map[string]any{"order_no": cmd.Order.OrderNo, "amount": cmd.Order.FiatAmount})
	resp, _ := json.Marshal(map[string]any{"provider_order_no": pon, "status": "pending"})
	return &WithdrawResult{ProviderOrderNo: pon, ProviderStatus: "pending", Accepted: true, RawRequest: req, RawResponse: resp}, nil
}

// ParseCallback 解析 mock 回调表单：order_no / provider_order_no / status / amount / valid。
//   - 缺 order_no → 返回 error（格式坏，Service 不落 event）。
//   - valid=false → Valid=false（模拟坏签，Service 落 event 后 ack fail）。
//   - status: success / failed / 其它=pending。
func (mockProvider) ParseCallback(req *CallbackRequest) (*CallbackResult, error) {
	// 兼容表单渠道（POST body → Form）与 GET/query 渠道（→ Query）：Form 优先，Query 兜底。
	get := func(k string) string {
		if v := req.Form.Get(k); v != "" {
			return v
		}
		return req.Query.Get(k)
	}

	orderNo := get("order_no")
	if orderNo == "" {
		return nil, fmt.Errorf("mock callback: missing order_no")
	}
	amount, err := strconv.ParseInt(get("amount"), 10, 64)
	if err != nil {
		return nil, fmt.Errorf("mock callback: bad amount %q: %w", get("amount"), err)
	}

	outcome := OutcomePending
	switch get("status") {
	case "success":
		outcome = OutcomeSuccess
	case "failed":
		outcome = OutcomeFailed
	}

	// 落库用收到的原始报文（Service.jsonPayload 归一为合法 jsonb），忠实留痕作纠纷佐证；
	// 直连（无 HTTP body）时退化为 Form 或 Query 编码。
	raw := req.Body
	if len(raw) == 0 {
		if enc := req.Form.Encode(); enc != "" {
			raw = []byte(enc)
		} else {
			raw = []byte(req.Query.Encode())
		}
	}

	return &CallbackResult{
		Valid:           get("valid") != "false",
		OrderNo:         orderNo,
		ProviderOrderNo: get("provider_order_no"),
		FiatAmount:      amount,
		Outcome:         outcome,
		ProviderStatus:  get("status"),
		AckSuccess:      Ack{ContentType: "text/plain; charset=utf-8", Body: []byte("success")},
		AckFailure:      Ack{ContentType: "text/plain; charset=utf-8", Body: []byte("fail")},
		RawPayload:      raw,
	}, nil
}

func (mockProvider) Query(_ context.Context, order OrderView) (*QueryResult, error) {
	return &QueryResult{Outcome: OutcomePending, ProviderStatus: "pending", FiatAmount: order.FiatAmount}, nil
}
