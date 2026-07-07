package app_test

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

// postForm 向无鉴权回调端点发 application/x-www-form-urlencoded，返回响应与文本 body。
func postForm(t *testing.T, path string, form url.Values) (*http.Response, string) {
	t.Helper()
	resp, err := http.Post(testServer.URL+path, "application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("post form %s: %v", path, err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp, string(raw)
}

// createRechargeOrder 建单并返回 order_no。
func createRechargeOrder(t *testing.T, token string, fiatAmount int64) string {
	t.Helper()
	resp, body := postJSON(t, "/api/v1/recharge/orders",
		map[string]any{"fiat_amount": fiatAmount, "payment_method": "mock"}, token)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create recharge order: status = %d, body = %v", resp.StatusCode, body)
	}
	orderNo, _ := body["order_no"].(string)
	if orderNo == "" {
		t.Fatalf("create recharge order: empty order_no, body = %v", body)
	}
	return orderNo
}

// mockPaySuccess 构造 mock 成功回调表单。
func mockPaySuccess(orderNo string, amount int64) url.Values {
	return url.Values{
		"order_no":          {orderNo},
		"provider_order_no": {"MOCK-" + orderNo},
		"status":            {"success"},
		"amount":            {strconv.FormatInt(amount, 10)},
	}
}

func rechargeTxStats(t *testing.T, customerID int64) (count int, sum int64) {
	t.Helper()
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*), COALESCE(SUM(amount),0) FROM transactions WHERE customer_id=$1 AND type='recharge'`,
		customerID).Scan(&count, &sum); err != nil {
		t.Fatalf("query recharge tx stats: %v", err)
	}
	return count, sum
}

func paymentEventCount(t *testing.T, orderNo, direction string) int {
	t.Helper()
	var n int
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM payment_events WHERE order_no=$1 AND direction=$2::payment_event_direction`,
		orderNo, direction).Scan(&n); err != nil {
		t.Fatalf("query payment_events: %v", err)
	}
	return n
}

func assertAuditIdentity(t *testing.T, customerID int64) {
	t.Helper()
	var ledger, total int64
	if err := testPool.QueryRow(context.Background(),
		`SELECT COALESCE((SELECT SUM(amount) FROM transactions WHERE customer_id=$1),0),
		        (SELECT available+frozen FROM wallets WHERE customer_id=$1)`,
		customerID).Scan(&ledger, &total); err != nil {
		t.Fatalf("query audit identity: %v", err)
	}
	if ledger != total {
		t.Fatalf("audit identity broken: ledger_sum=%d wallet_total=%d", ledger, total)
	}
}

func TestRechargeCallbackCreditsThenIdempotent(t *testing.T) {
	username := uniqueUsername(t)
	registerCustomer(t, username, "")
	token := loginCustomer(t, username, "secret123")
	cid := customerIDOf(t, username)

	const fiat = 10000 // 100 元 = 10000 分 = 10000 钻
	orderNo := createRechargeOrder(t, token, fiat)

	// 首次成功回调 → 到账
	resp, body := postForm(t, "/callback/mock/pay", mockPaySuccess(orderNo, fiat))
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "success") {
		t.Fatalf("callback: status = %d, body = %q", resp.StatusCode, body)
	}
	if got := availableOf(t, token); got != fiat {
		t.Fatalf("available after credit = %d, want %d", got, fiat)
	}
	if count, sum := rechargeTxStats(t, cid); count != 1 || sum != fiat {
		t.Fatalf("recharge tx = (count %d, sum %d), want (1, %d)", count, sum, fiat)
	}
	var status string
	if err := testPool.QueryRow(context.Background(),
		`SELECT status FROM recharge_orders WHERE order_no=$1`, orderNo).Scan(&status); err != nil {
		t.Fatalf("query order status: %v", err)
	}
	if status != "succeeded" {
		t.Fatalf("order status = %q, want succeeded", status)
	}
	assertAuditIdentity(t, cid)

	// payment_events：pay 的 request/response + callback 各 ≥1
	for _, dir := range []string{"request", "response", "callback"} {
		if got := paymentEventCount(t, orderNo, dir); got < 1 {
			t.Errorf("payment_events[%s] = %d, want >= 1", dir, got)
		}
	}

	// 重复回调 → 仍 ack success（ack 由 provider 决定，幂等重放）、只到账一次
	dupResp, dupBody := postForm(t, "/callback/mock/pay", mockPaySuccess(orderNo, fiat))
	if dupResp.StatusCode != http.StatusOK || !strings.Contains(dupBody, "success") {
		t.Fatalf("duplicate callback ack: status = %d, body = %q", dupResp.StatusCode, dupBody)
	}
	if got := availableOf(t, token); got != fiat {
		t.Fatalf("available after duplicate callback = %d, want %d (credit once)", got, fiat)
	}
	if count, _ := rechargeTxStats(t, cid); count != 1 {
		t.Fatalf("recharge tx count after duplicate = %d, want 1", count)
	}
	assertAuditIdentity(t, cid)
}

func TestRechargeCallbackAmountMismatchRejected(t *testing.T) {
	username := uniqueUsername(t)
	registerCustomer(t, username, "")
	token := loginCustomer(t, username, "secret123")
	cid := customerIDOf(t, username)

	orderNo := createRechargeOrder(t, token, 5000)
	// 回调金额与订单 fiat_amount 不符 → 拒
	form := mockPaySuccess(orderNo, 9999)
	resp, body := postForm(t, "/callback/mock/pay", form)
	if resp.StatusCode == http.StatusInternalServerError {
		t.Fatalf("amount mismatch caused 500: body = %q", body)
	}
	if strings.Contains(body, "success") {
		t.Fatalf("amount mismatch acked success: body = %q", body)
	}
	if got := availableOf(t, token); got != 0 {
		t.Fatalf("available after mismatch = %d, want 0 (not credited)", got)
	}
	if count, _ := rechargeTxStats(t, cid); count != 0 {
		t.Fatalf("recharge tx count after mismatch = %d, want 0", count)
	}
}

func TestRechargeCallbackBadSignRejectedButRecorded(t *testing.T) {
	username := uniqueUsername(t)
	registerCustomer(t, username, "")
	token := loginCustomer(t, username, "secret123")
	cid := customerIDOf(t, username)

	orderNo := createRechargeOrder(t, token, 3000)
	form := mockPaySuccess(orderNo, 3000)
	form.Set("valid", "false") // 模拟坏签
	resp, body := postForm(t, "/callback/mock/pay", form)
	if resp.StatusCode == http.StatusInternalServerError {
		t.Fatalf("bad sign caused 500: body = %q", body)
	}
	if strings.Contains(body, "success") {
		t.Fatalf("bad sign acked success: body = %q", body)
	}
	if got := availableOf(t, token); got != 0 {
		t.Fatalf("available after bad sign = %d, want 0 (not credited)", got)
	}
	if count, _ := rechargeTxStats(t, cid); count != 0 {
		t.Fatalf("recharge tx count after bad sign = %d, want 0", count)
	}
	// 坏签仍能提取 order_no → 落 callback event（取证）
	if got := paymentEventCount(t, orderNo, "callback"); got < 1 {
		t.Errorf("bad-sign callback event = %d, want >= 1 (recorded for forensics)", got)
	}
}

func TestRechargeCallbackMissingOrderNoNotServerError(t *testing.T) {
	// 报文提不出 order_no（格式坏）→ 不 500、不到账。
	resp, body := postForm(t, "/callback/mock/pay", url.Values{"status": {"success"}, "amount": {"100"}})
	if resp.StatusCode == http.StatusInternalServerError {
		t.Fatalf("missing order_no caused 500: body = %q", body)
	}
	if strings.Contains(body, "success") {
		t.Fatalf("malformed callback acked success: body = %q", body)
	}
}

func TestRechargeUnknownProviderCallback404(t *testing.T) {
	resp, _ := postForm(t, "/callback/nope/pay", url.Values{"order_no": {"R1"}, "amount": {"1"}, "status": {"success"}})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown provider callback: status = %d, want 404", resp.StatusCode)
	}
}

func TestRechargeOrderListReturnsOwn(t *testing.T) {
	username := uniqueUsername(t)
	registerCustomer(t, username, "")
	token := loginCustomer(t, username, "secret123")

	first := createRechargeOrder(t, token, 1000)
	second := createRechargeOrder(t, token, 2000)

	resp, body := getJSON(t, "/api/v1/recharge/orders", token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list recharge orders: status = %d, body = %v", resp.StatusCode, body)
	}
	items, _ := body["items"].([]any)
	if len(items) < 2 {
		t.Fatalf("recharge orders = %d, want >= 2", len(items))
	}
	// 降序：最新（second）在前
	seen := map[string]bool{}
	for _, it := range items {
		m, _ := it.(map[string]any)
		if on, _ := m["order_no"].(string); on != "" {
			seen[on] = true
		}
	}
	if !seen[first] || !seen[second] {
		t.Fatalf("list missing own orders: seen = %v", seen)
	}
}

func TestRechargeCreateRequiresAuth(t *testing.T) {
	resp, _ := postJSON(t, "/api/v1/recharge/orders",
		map[string]any{"fiat_amount": 1000, "payment_method": "mock"}, "")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("unauthenticated create: status = %d, want 401", resp.StatusCode)
	}
}

func TestRechargeCreateValidations(t *testing.T) {
	username := uniqueUsername(t)
	registerCustomer(t, username, "")
	token := loginCustomer(t, username, "secret123")

	t.Run("non_positive_amount", func(t *testing.T) {
		resp, _ := postJSON(t, "/api/v1/recharge/orders",
			map[string]any{"fiat_amount": 0, "payment_method": "mock"}, token)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("zero amount: status = %d, want 400", resp.StatusCode)
		}
	})
	t.Run("unsupported_method", func(t *testing.T) {
		resp, body := postJSON(t, "/api/v1/recharge/orders",
			map[string]any{"fiat_amount": 1000, "payment_method": "wechat"}, token)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("unsupported method: status = %d, want 400", resp.StatusCode)
		}
		if got := body["code"]; got != "RECHARGE_METHOD_UNSUPPORTED" {
			t.Errorf("code = %v, want RECHARGE_METHOD_UNSUPPORTED", got)
		}
	})
}
