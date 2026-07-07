// Package payment 现金订单 + 支付渠道 Provider SPI/注册表/实现。
// Provider 只做 协议交互 + 签名 + 状态映射 + 验签，禁碰 DB、禁业务校验；
// 订单状态/钱包/流水编排全在 Service 层（镜像旧版 SPI 边界，见 PRD #63）。
package payment

import (
	"context"
	"net/url"
)

// CallbackOutcome 是渠道回调映射后的资金语义（与具体渠道状态码解耦）。
type CallbackOutcome string

// 回调资金语义常量。
const (
	OutcomeSuccess CallbackOutcome = "success" // 代收成功 → 到账
	OutcomeFailed  CallbackOutcome = "failed"  // 明确失败
	OutcomePending CallbackOutcome = "pending" // 仍在途，非终态
)

// OrderView 传给 Provider 的订单只读投影：不含任何 DB 类型，使 Provider 与数据层解耦。
type OrderView struct {
	OrderNo         string
	FiatAmount      int64 // 法币，分单位
	PaymentMethod   string
	ProviderOrderNo string // 查单/代付回填用；建单时为空
}

// PayCommand 发起代收（充值）的入参。
type PayCommand struct {
	Order     OrderView
	NotifyURL string // 渠道异步回调地址（服务端拼 base + /callback/{code}/pay）
}

// PayResult 代收受理结果。Raw* 为渠道原始报文，供 Service 落 payment_events。
type PayResult struct {
	ProviderOrderNo string
	RedirectURL     string // 支付跳转链接 / 二维码内容
	ProviderStatus  string
	RawRequest      []byte
	RawResponse     []byte
}

// PayeeView 代付收款目标快照（不含 DB 类型）。#68 提现代付使用。
type PayeeView struct {
	Account  string
	Name     string
	Bank     string
	BankCode string
}

// WithdrawCommand 发起代付（提现）的入参。#68 使用。
type WithdrawCommand struct {
	Order OrderView
	Payee PayeeView
}

// WithdrawResult 代付受理结果。#68 使用。
type WithdrawResult struct {
	ProviderOrderNo string
	ProviderStatus  string
	Accepted        bool
	RawRequest      []byte
	RawResponse     []byte
}

// CallbackRequest 是回调 HTTP 请求交给 Provider 的原始视图：
// 不同渠道分别读 JSON Body（pay126）/ 表单 Form（merchant/mock）/ Query（stpay GET）。
type CallbackRequest struct {
	Body  []byte
	Form  url.Values
	Query url.Values
}

// Ack 回给渠道的应答报文（渠道自定义格式：pay126 JSON、stpay/merchant 文本）。
type Ack struct {
	ContentType string
	Body        []byte
}

// CallbackResult 回调解析结果。
//
// Provider.ParseCallback 的两类失败语义须区分（PRD #63 审查修订）：
//   - 返回 error：报文格式坏到提取不出 order_no —— Service 无法落 payment_events（order_no NOT NULL），仅 slog。
//   - 返回 Valid=false：能提取 order_no、仅验签失败 —— Service 落 callback event 后 ack 失败、不结算。
type CallbackResult struct {
	Valid           bool
	OrderNo         string
	ProviderOrderNo string
	FiatAmount      int64 // 分，用于金额一致性校验
	Outcome         CallbackOutcome
	ProviderStatus  string
	AckSuccess      Ack // Service 接受（结算/幂等短路）时回写
	AckFailure      Ack // Service 拒绝（坏签/金额不符/渠道不符）时回写
	RawPayload      []byte
}

// QueryResult 主动查单结果。#66 超时先查后取消使用。
type QueryResult struct {
	Outcome        CallbackOutcome
	ProviderStatus string
	FiatAmount     int64
	RawRequest     []byte
	RawResponse    []byte
}

// Provider 统一支付渠道 SPI。实现须无状态、并发安全。
type Provider interface {
	// Code 渠道唯一标识（注册表索引键，落 order.provider）。
	Code() string
	// SupportedMethods 本渠道支持的支付方式集合（建单选渠道的二次校验）。
	SupportedMethods() []string
	// Pay 发起代收（充值）。
	Pay(ctx context.Context, cmd PayCommand) (*PayResult, error)
	// Withdraw 发起代付（提现）。#68 使用。
	Withdraw(ctx context.Context, cmd WithdrawCommand) (*WithdrawResult, error)
	// ParseCallback 解析并验签代收回调（验签在此完成）。语义见 CallbackResult。
	ParseCallback(req *CallbackRequest) (*CallbackResult, error)
	// Query 主动查单。#66 使用。
	Query(ctx context.Context, order OrderView) (*QueryResult, error)
}
