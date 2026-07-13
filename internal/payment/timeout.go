package payment

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5"

	"github.com/netfishx/gabon-go/internal/db"
)

// sweepBatchSize 单轮清扫上限沿用旧版，避免一次任务长时间占用数据库与渠道连接。
const sweepBatchSize = 200

// CancelExpiredRecharges 清扫过期未支付充值单（先查后取消）。
// 返回本轮实际翻 cancelled 的单数。单笔失败不阻断整轮；列表查询失败时才返回错误。
func (s *Service) CancelExpiredRecharges(ctx context.Context) (int, error) {
	orders, err := s.q.ListExpiredPendingRecharges(ctx, sweepBatchSize)
	if err != nil {
		return 0, fmt.Errorf("list expired pending recharges: %w", err)
	}

	cancelled := 0
	for _, order := range orders {
		if order.Provider == nil {
			slog.WarnContext(ctx, "recharge sweep missing provider", "order_no", order.OrderNo)
			continue
		}
		providerCode := *order.Provider
		prov := s.registry.ByCode(providerCode)
		if prov == nil {
			slog.WarnContext(ctx, "recharge sweep provider not registered", "order_no", order.OrderNo, "provider", providerCode)
			continue
		}

		res, err := prov.Query(ctx, OrderView{
			OrderNo:         order.OrderNo,
			FiatAmount:      order.FiatAmount,
			PaymentMethod:   stringValue(order.PaymentMethod),
			ProviderOrderNo: stringValue(order.ProviderOrderNo),
		})
		if err != nil {
			slog.WarnContext(ctx, "recharge provider query failed", "order_no", order.OrderNo, "provider", providerCode, "error", err)
			s.recordQueryError(ctx, order.OrderNo, providerCode, err)
			continue
		}

		action := "skipped"
		switch res.Outcome {
		case OutcomeSuccess:
			if res.FiatAmount != order.FiatAmount {
				slog.WarnContext(ctx, "recharge query amount mismatch",
					"order_no", order.OrderNo, "query_amount", res.FiatAmount, "order_amount", order.FiatAmount)
				break
			}
			if err := s.settleRecharge(ctx, order, res.ProviderStatus); err != nil {
				slog.WarnContext(ctx, "settle recharge from query failed", "order_no", order.OrderNo, "error", err)
				break
			}
			action = "settled"
		case OutcomeFailed:
			providerStatus := res.ProviderStatus
			if _, err := s.q.MarkRechargeFailed(ctx, db.MarkRechargeFailedParams{
				ProviderStatus: &providerStatus,
				ID:             order.ID,
			}); err == nil {
				action = "failed"
			} else if !errors.Is(err, pgx.ErrNoRows) {
				slog.WarnContext(ctx, "mark failed recharge failed", "order_no", order.OrderNo, "error", err)
			}
		case OutcomePending:
			providerStatus := res.ProviderStatus
			if _, err := s.q.MarkRechargeCancelled(ctx, db.MarkRechargeCancelledParams{
				ProviderStatus: &providerStatus,
				ID:             order.ID,
			}); err == nil {
				action = "cancelled"
				cancelled++
			} else if !errors.Is(err, pgx.ErrNoRows) {
				slog.WarnContext(ctx, "mark recharge cancelled failed", "order_no", order.OrderNo, "error", err)
			}
		}
		s.recordQueryResult(ctx, order.OrderNo, providerCode, action, res)
	}
	return cancelled, nil
}

func (s *Service) recordQueryError(ctx context.Context, orderNo, providerCode string, cause error) {
	payload, _ := json.Marshal(map[string]string{"action": "error", "error": cause.Error()})
	if err := insertEvent(ctx, s.q, orderNo, providerCode, db.PaymentEventDirectionQuery, payload); err != nil {
		slog.ErrorContext(ctx, "record recharge query event failed", "order_no", orderNo, "error", err)
	}
}

func (s *Service) recordQueryResult(ctx context.Context, orderNo, providerCode, action string, res *QueryResult) {
	payload, _ := json.Marshal(struct {
		Action   string          `json:"action"`
		Request  json.RawMessage `json:"request"`
		Response json.RawMessage `json:"response"`
	}{
		Action:   action,
		Request:  json.RawMessage(jsonPayload(res.RawRequest)),
		Response: json.RawMessage(jsonPayload(res.RawResponse)),
	})
	if err := insertEvent(ctx, s.q, orderNo, providerCode, db.PaymentEventDirectionQuery, payload); err != nil {
		slog.ErrorContext(ctx, "record recharge query event failed", "order_no", orderNo, "error", err)
	}
}

func stringValue(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}
