package payment

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/netfishx/gabon-go/internal/apierr"
	"github.com/netfishx/gabon-go/internal/db"
	"github.com/netfishx/gabon-go/internal/wallet"
)

// ErrUnknownProvider 表示回调路径上的 provider code 未注册（handler 映射 404）。
var ErrUnknownProvider = errors.New("payment: unknown provider")

// errOrderNotFound 回调定位不到订单（内部信号，转 ack 失败）。
var errOrderNotFound = errors.New("payment: order not found")

// errAlreadyTerminal 结算时订单已非 pending_payment（幂等短路信号）。
var errAlreadyTerminal = errors.New("payment: order already terminal")

// Service 现金订单域服务：充值建单 / 回调结算 / 列表。
// 资金变更一律经 wallet 原语同事务注入（ADR-0006）。
type Service struct {
	pool            *pgxpool.Pool
	q               *db.Queries
	wallets         *wallet.Service
	registry        *Registry
	callbackBaseURL string
	rechargeTimeout time.Duration
}

// NewService 构造现金订单域服务。
func NewService(pool *pgxpool.Pool, wallets *wallet.Service, registry *Registry, callbackBaseURL string, rechargeTimeout time.Duration) *Service {
	return &Service{
		pool:            pool,
		q:               db.New(pool),
		wallets:         wallets,
		registry:        registry,
		callbackBaseURL: callbackBaseURL,
		rechargeTimeout: rechargeTimeout,
	}
}

// CreateRechargeOrder 建充值订单：选渠道 → 建单（order_no='R'||id）→ 调 Pay 拿支付链接 → 落 request/response event。
// 钻石按固定汇率算：1 元=100 钻，即 1 分=1 钻，故 amount(钻) == fiatAmount(分)。
func (s *Service) CreateRechargeOrder(ctx context.Context, customerID, fiatAmount int64, method string) (db.RechargeOrder, string, error) {
	prov, err := s.registry.ProviderFor(method)
	if errors.Is(err, ErrNoProviderForMethod) {
		return db.RechargeOrder{}, "", apierr.New(http.StatusBadRequest, apierr.CodeRechargeMethodUnsupported, "unsupported payment method")
	}
	if err != nil {
		return db.RechargeOrder{}, "", err
	}
	providerCode := prov.Code()

	var order db.RechargeOrder
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		id, err := q.InsertRechargeOrder(ctx, db.InsertRechargeOrderParams{
			CustomerID:    customerID,
			Amount:        fiatAmount, // 1 分 = 1 钻
			FiatAmount:    fiatAmount,
			PaymentMethod: &method,
			Provider:      &providerCode,
			ExpiresAt:     pgtype.Timestamptz{Time: time.Now().Add(s.rechargeTimeout), Valid: true},
		})
		if err != nil {
			return fmt.Errorf("insert recharge order: %w", err)
		}
		order, err = q.FinalizeRechargeOrderNo(ctx, id)
		if err != nil {
			return fmt.Errorf("finalize order_no: %w", err)
		}
		return nil
	})
	if err != nil {
		return db.RechargeOrder{}, "", err
	}

	pay, err := prov.Pay(ctx, PayCommand{
		Order:     OrderView{OrderNo: order.OrderNo, FiatAmount: order.FiatAmount, PaymentMethod: method},
		NotifyURL: s.notifyURL(providerCode),
	})
	if err != nil {
		// Pay 失败：订单留 pending_payment，超时取消归 #66；尽力记录已有报文。
		s.recordPayAttempt(ctx, order.OrderNo, providerCode, nil, err)
		return db.RechargeOrder{}, "", fmt.Errorf("provider pay: %w", err)
	}

	if err := s.persistPayResult(ctx, order.ID, order.OrderNo, providerCode, pay); err != nil {
		return db.RechargeOrder{}, "", err
	}
	return order, pay.RedirectURL, nil
}

// persistPayResult 同事务落 request+response event 并回填渠道单号/状态。
func (s *Service) persistPayResult(ctx context.Context, orderID int64, orderNo, providerCode string, pay *PayResult) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		if err := insertEvent(ctx, q, orderNo, providerCode, db.PaymentEventDirectionRequest, pay.RawRequest); err != nil {
			return err
		}
		if err := insertEvent(ctx, q, orderNo, providerCode, db.PaymentEventDirectionResponse, pay.RawResponse); err != nil {
			return err
		}
		pon, ps := pay.ProviderOrderNo, pay.ProviderStatus
		if err := q.SetRechargeProviderInfo(ctx, db.SetRechargeProviderInfoParams{
			ID: orderID, ProviderOrderNo: &pon, ProviderStatus: &ps,
		}); err != nil {
			return fmt.Errorf("set provider info: %w", err)
		}
		return nil
	})
}

// recordPayAttempt Pay 失败时尽力落一条 request event 便于排查（best-effort）。
func (s *Service) recordPayAttempt(ctx context.Context, orderNo, providerCode string, req []byte, cause error) {
	payload, _ := json.Marshal(map[string]string{"raw": string(req), "error": cause.Error()})
	if err := insertEvent(ctx, s.q, orderNo, providerCode, db.PaymentEventDirectionRequest, payload); err != nil {
		slog.ErrorContext(ctx, "record pay attempt failed", "order_no", orderNo, "error", err)
	}
}

// HandlePayCallback 处理代收回调（无鉴权，身份靠验签）。返回回给渠道的 ack。
// 流程（PRD #63 + 审查修订）：落 callback event → 验签 → 定位订单 → provider 归属校验 → 金额校验 → 同事务结算。
func (s *Service) HandlePayCallback(ctx context.Context, providerCode string, req *CallbackRequest) (*Ack, error) {
	prov := s.registry.ByCode(providerCode)
	if prov == nil {
		return nil, ErrUnknownProvider
	}

	res, err := prov.ParseCallback(req)
	if err != nil {
		// 格式坏到提不出 order_no：无 order_no 可 key payment_events，仅 slog（不落 event）。
		slog.WarnContext(ctx, "payment callback parse failed",
			"provider", providerCode, "error", err, "raw", string(req.Body))
		return &Ack{ContentType: "text/plain; charset=utf-8", Body: []byte("fail")}, nil
	}

	// 已能提取 order_no → 落 callback event（含坏签，取证），独立提交在结算事务之外。
	if err := insertEvent(ctx, s.q, res.OrderNo, providerCode, db.PaymentEventDirectionCallback, res.RawPayload); err != nil {
		slog.ErrorContext(ctx, "record callback event failed", "order_no", res.OrderNo, "error", err)
	}

	if !res.Valid {
		slog.WarnContext(ctx, "payment callback bad signature", "provider", providerCode, "order_no", res.OrderNo)
		return &res.AckFailure, nil
	}

	order, err := s.locateOrder(ctx, providerCode, res)
	if errors.Is(err, errOrderNotFound) {
		slog.WarnContext(ctx, "payment callback order not found",
			"provider", providerCode, "order_no", res.OrderNo, "provider_order_no", res.ProviderOrderNo)
		return &res.AckFailure, nil
	}
	if err != nil {
		return nil, err
	}

	// provider 归属校验：一个渠道路径上的回调不得结算另一渠道的订单。
	if order.Provider == nil || *order.Provider != providerCode {
		slog.WarnContext(ctx, "payment callback provider mismatch",
			"path_provider", providerCode, "order_provider", order.Provider, "order_no", order.OrderNo)
		return &res.AckFailure, nil
	}

	// #64：仅 success 到账；failed/pending 回调已落 event，ack 后不改状态（超时取消归 #66）。
	if res.Outcome != OutcomeSuccess {
		return &res.AckSuccess, nil
	}

	// 金额一致性校验（补旧版缺口）：回调金额须等于订单 fiat_amount。
	if res.FiatAmount != order.FiatAmount {
		slog.WarnContext(ctx, "payment callback amount mismatch",
			"order_no", order.OrderNo, "callback_amount", res.FiatAmount, "order_amount", order.FiatAmount)
		reason := fmt.Sprintf("callback_amount=%d order_amount=%d", res.FiatAmount, order.FiatAmount)
		if _, err := s.q.MarkRechargeAmountMismatch(ctx, db.MarkRechargeAmountMismatchParams{
			Reason: &reason,
			ID:     order.ID,
		}); err != nil {
			slog.WarnContext(ctx, "mark callback amount mismatch failed", "order_no", order.OrderNo, "error", err)
		}
		return &res.AckFailure, nil
	}

	if err := s.settleRecharge(ctx, order, res.ProviderStatus); err != nil {
		return nil, err
	}
	return &res.AckSuccess, nil
}

// settleRecharge 到账结算：CAS 翻 succeeded + 同事务入账（三层幂等）。
// 已终态短路 / 流水唯一约束撞击 → 幂等成功（重复回调只到账一次）。
func (s *Service) settleRecharge(ctx context.Context, order db.RechargeOrder, providerStatus string) error {
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		ps := providerStatus
		if _, err := q.MarkRechargeSucceeded(ctx, db.MarkRechargeSucceededParams{
			ID: order.ID, ProviderStatus: &ps,
		}); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return errAlreadyTerminal // 已非 pending_payment，幂等短路（整事务回滚）
			}
			return fmt.Errorf("mark recharge succeeded: %w", err)
		}
		refID := order.ID
		return s.wallets.CreditTx(ctx, tx, wallet.CreditParams{
			CustomerID: order.CustomerID,
			Type:       db.TransactionTypeRecharge,
			Amount:     order.Amount,
			RefID:      &refID,
		})
	})
	if errors.Is(err, errAlreadyTerminal) || errors.Is(err, wallet.ErrAlreadyGranted) {
		return nil // 幂等成功：此前已到账
	}
	return err
}

// locateOrder 按 order_no 定位订单，缺失时用 (provider, provider_order_no) 兜底。
func (s *Service) locateOrder(ctx context.Context, providerCode string, res *CallbackResult) (db.RechargeOrder, error) {
	order, err := s.q.GetRechargeOrderByOrderNo(ctx, res.OrderNo)
	if err == nil {
		return order, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return db.RechargeOrder{}, fmt.Errorf("get order by order_no: %w", err)
	}
	if res.ProviderOrderNo == "" {
		return db.RechargeOrder{}, errOrderNotFound
	}
	pc, pon := providerCode, res.ProviderOrderNo
	order, err = s.q.GetRechargeOrderByProviderRef(ctx, db.GetRechargeOrderByProviderRefParams{
		Provider: &pc, ProviderOrderNo: &pon,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return db.RechargeOrder{}, errOrderNotFound
	}
	if err != nil {
		return db.RechargeOrder{}, fmt.Errorf("get order by provider ref: %w", err)
	}
	return order, nil
}

// ListRechargeOrders 按订单号降序游标分页返回本人充值订单。
func (s *Service) ListRechargeOrders(ctx context.Context, customerID, cursor int64, limit int32) (items []db.RechargeOrder, next int64, err error) {
	items, err = s.q.ListRechargeOrders(ctx, db.ListRechargeOrdersParams{
		CustomerID: customerID, Cursor: cursor, RowLimit: limit + 1,
	})
	if err != nil {
		return nil, 0, fmt.Errorf("list recharge orders: %w", err)
	}
	if len(items) > int(limit) {
		items = items[:limit]
		next = items[len(items)-1].ID
	}
	return items, next, nil
}

// notifyURL 拼渠道异步回调地址；base 未配时退化为相对路径（mock 不依赖）。
func (s *Service) notifyURL(providerCode string) string {
	return s.callbackBaseURL + "/callback/" + providerCode + "/pay"
}

// insertEvent 落一条 payment_events；payload 归一为合法 jsonb。
func insertEvent(ctx context.Context, q *db.Queries, orderNo, providerCode string, dir db.PaymentEventDirection, payload []byte) error {
	if err := q.InsertPaymentEvent(ctx, db.InsertPaymentEventParams{
		OrderNo:   orderNo,
		Provider:  providerCode,
		Direction: dir,
		Payload:   jsonPayload(payload),
	}); err != nil {
		return fmt.Errorf("insert payment event: %w", err)
	}
	return nil
}

// jsonPayload 把渠道原始报文归一为合法 JSON（payment_events.payload 为 jsonb）：
// 本就是合法 JSON 直存，否则包一层 {"raw": ...}（表单/文本渠道）。
func jsonPayload(raw []byte) []byte {
	if len(raw) > 0 && json.Valid(raw) {
		return raw
	}
	b, _ := json.Marshal(map[string]string{"raw": string(raw)})
	return b
}
