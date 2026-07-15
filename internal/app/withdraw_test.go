package app_test

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

func fundForWithdrawal(t *testing.T, token string, amount int64) {
	t.Helper()
	orderNo := createRechargeOrder(t, token, amount)
	resp, body := postForm(t, "/callback/mock/pay", mockPaySuccess(orderNo, amount))
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "success") {
		t.Fatalf("recharge callback: status = %d, body = %q", resp.StatusCode, body)
	}
}

func setWithdrawalPassword(t *testing.T, token, password string) (*http.Response, map[string]any) {
	t.Helper()
	return doJSON(t, http.MethodPut, "/api/v1/withdrawal-password", token, map[string]any{"password": password})
}

func addWithdrawalCard(t *testing.T, token, cardNo string) int64 {
	t.Helper()
	resp, body := postJSON(t, "/api/v1/bank-cards", map[string]any{
		"card_no": cardNo, "holder_name": "张三", "bank_name": "中国工商银行",
		"bank_code": "ICBC", "province": "广东省", "city": "深圳市",
	}, token)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("add bank card: status = %d, body = %v", resp.StatusCode, body)
	}
	return int64(body["id"].(float64))
}

func createWithdrawal(t *testing.T, token string, amount, cardID int64, password string) (*http.Response, map[string]any) {
	t.Helper()
	return postJSON(t, "/api/v1/withdrawal/orders", map[string]any{
		"amount": amount, "bank_card_id": cardID, "withdrawal_password": password,
	}, token)
}

func walletBalances(t *testing.T, customerID int64) (available, frozen int64) {
	t.Helper()
	if err := testPool.QueryRow(context.Background(),
		`SELECT available, frozen FROM wallets WHERE customer_id=$1`, customerID,
	).Scan(&available, &frozen); err != nil {
		t.Fatalf("query wallet: %v", err)
	}
	return available, frozen
}

func withdrawalOrderCount(t *testing.T, customerID int64) int {
	t.Helper()
	var count int
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM withdrawal_orders WHERE customer_id=$1`, customerID,
	).Scan(&count); err != nil {
		t.Fatalf("count withdrawal orders: %v", err)
	}
	return count
}

func TestWithdrawalCreateFreezesBalanceAndSnapshotsPayee(t *testing.T) {
	username := uniqueUsername(t)
	token := registerAndLogin(t, username)
	customerID := customerIDOf(t, username)

	const balance = int64(10_000)
	fundForWithdrawal(t, token, balance)

	resp, body := setWithdrawalPassword(t, token, "withdraw-secret")
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("set withdrawal password: status = %d, body = %v", resp.StatusCode, body)
	}

	resp, card := postJSON(t, "/api/v1/bank-cards", map[string]any{
		"card_no":     "6222020202020202",
		"holder_name": "张三",
		"bank_name":   "中国工商银行",
		"bank_code":   "ICBC",
		"province":    "广东省",
		"city":        "深圳市",
	}, token)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("add bank card: status = %d, body = %v", resp.StatusCode, card)
	}
	cardID := int64(card["id"].(float64))

	const amount = int64(2_500)
	resp, body = postJSON(t, "/api/v1/withdrawal/orders", map[string]any{
		"amount":              amount,
		"bank_card_id":        cardID,
		"withdrawal_password": "withdraw-secret",
	}, token)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create withdrawal: status = %d, body = %v", resp.StatusCode, body)
	}
	if body["amount"] != float64(amount) || body["fiat_amount"] != float64(amount) || body["status"] != "pending_review" {
		t.Errorf("withdrawal response = %v, want amount/fiat_amount=%d status=pending_review", body, amount)
	}
	if body["created_at"] == nil {
		t.Errorf("withdrawal response missing created_at: %v", body)
	}
	if got, _ := body["order_no"].(string); !strings.HasPrefix(got, "W") {
		t.Errorf("order_no = %q, want W prefix", got)
	}

	available, frozen := walletBalances(t, customerID)
	if available != balance-amount || frozen != amount {
		t.Errorf("wallet = (available %d, frozen %d), want (%d, %d)", available, frozen, balance-amount, amount)
	}

	var passwordHash, status, account, name, bank, bankCode, province, city string
	if err := testPool.QueryRow(context.Background(),
		`SELECT c.withdrawal_password_hash, w.status, w.payee_account, w.payee_name, w.payee_bank,
		        w.payee_bank_code, w.payee_province, w.payee_city
		 FROM customers c JOIN withdrawal_orders w ON w.customer_id=c.id
		 WHERE c.id=$1 ORDER BY w.id DESC LIMIT 1`, customerID,
	).Scan(&passwordHash, &status, &account, &name, &bank, &bankCode, &province, &city); err != nil {
		t.Fatalf("query withdrawal snapshot: %v", err)
	}
	if !strings.HasPrefix(passwordHash, "$argon2id$") || passwordHash == "withdraw-secret" {
		t.Errorf("withdrawal_password_hash = %q, want argon2id hash", passwordHash)
	}
	if status != "pending_review" || account != "6222020202020202" || name != "张三" || bank != "中国工商银行" {
		t.Errorf("stored withdrawal = (%q, %q, %q, %q), want pending_review and payee snapshot", status, account, name, bank)
	}
	if bankCode != "ICBC" || province != "广东省" || city != "深圳市" {
		t.Errorf("stored optional payee snapshot = (%q, %q, %q), want (ICBC, 广东省, 深圳市)", bankCode, province, city)
	}

	var transactions int
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM transactions WHERE customer_id=$1`, customerID).Scan(&transactions); err != nil {
		t.Fatalf("count transactions: %v", err)
	}
	if transactions != 1 {
		t.Errorf("transactions after freeze = %d, want 1 recharge transaction only", transactions)
	}
	assertAuditIdentity(t, customerID)
}

func TestWithdrawalPasswordErrorsDoNotFreeze(t *testing.T) {
	t.Run("not_set", func(t *testing.T) {
		username := uniqueUsername(t)
		token := registerAndLogin(t, username)
		customerID := customerIDOf(t, username)

		resp, body := createWithdrawal(t, token, 100, 999999, "anything")
		if resp.StatusCode != http.StatusForbidden || body["code"] != "WITHDRAWAL_PASSWORD_NOT_SET" {
			t.Fatalf("not set: status = %d, body = %v", resp.StatusCode, body)
		}
		available, frozen := walletBalances(t, customerID)
		if available != 0 || frozen != 0 || withdrawalOrderCount(t, customerID) != 0 {
			t.Errorf("not set mutated state: available=%d frozen=%d orders=%d", available, frozen, withdrawalOrderCount(t, customerID))
		}
	})

	t.Run("mismatch", func(t *testing.T) {
		username := uniqueUsername(t)
		token := registerAndLogin(t, username)
		customerID := customerIDOf(t, username)
		fundForWithdrawal(t, token, 1_000)
		if resp, body := setWithdrawalPassword(t, token, "correct"); resp.StatusCode != http.StatusNoContent {
			t.Fatalf("set password: status = %d, body = %v", resp.StatusCode, body)
		}
		cardID := addWithdrawalCard(t, token, "6222020202020303")

		resp, body := createWithdrawal(t, token, 100, cardID, "wrong")
		if resp.StatusCode != http.StatusForbidden || body["code"] != "WITHDRAWAL_PASSWORD_MISMATCH" {
			t.Fatalf("mismatch: status = %d, body = %v", resp.StatusCode, body)
		}
		available, frozen := walletBalances(t, customerID)
		if available != 1_000 || frozen != 0 || withdrawalOrderCount(t, customerID) != 0 {
			t.Errorf("mismatch mutated state: available=%d frozen=%d orders=%d", available, frozen, withdrawalOrderCount(t, customerID))
		}
	})
}

func TestWithdrawalInsufficientBalanceRollsBack(t *testing.T) {
	username := uniqueUsername(t)
	token := registerAndLogin(t, username)
	customerID := customerIDOf(t, username)
	fundForWithdrawal(t, token, 1_000)
	if resp, body := setWithdrawalPassword(t, token, "secret"); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("set password: status = %d, body = %v", resp.StatusCode, body)
	}
	cardID := addWithdrawalCard(t, token, "6222020202020404")

	resp, body := createWithdrawal(t, token, 1_001, cardID, "secret")
	if resp.StatusCode != http.StatusConflict || body["code"] != "WALLET_INSUFFICIENT_BALANCE" {
		t.Fatalf("insufficient: status = %d, body = %v", resp.StatusCode, body)
	}
	available, frozen := walletBalances(t, customerID)
	if available != 1_000 || frozen != 0 || withdrawalOrderCount(t, customerID) != 0 {
		t.Errorf("insufficient mutated state: available=%d frozen=%d orders=%d", available, frozen, withdrawalOrderCount(t, customerID))
	}
	assertAuditIdentity(t, customerID)
}

func TestWithdrawalRejectsUnownedOrDeletedBankCard(t *testing.T) {
	usernameA := uniqueUsername(t)
	tokenA := registerAndLogin(t, usernameA)
	usernameB := uniqueUsername(t)
	tokenB := registerAndLogin(t, usernameB)
	customerB := customerIDOf(t, usernameB)
	fundForWithdrawal(t, tokenB, 1_000)
	if resp, body := setWithdrawalPassword(t, tokenB, "secret"); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("set password: status = %d, body = %v", resp.StatusCode, body)
	}
	cardID := addWithdrawalCard(t, tokenA, "6222020202020909")

	resp, body := createWithdrawal(t, tokenB, 100, cardID, "secret")
	if resp.StatusCode != http.StatusNotFound || body["code"] != "BANK_CARD_NOT_FOUND" {
		t.Fatalf("other customer's card: status = %d, body = %v", resp.StatusCode, body)
	}
	available, frozen := walletBalances(t, customerB)
	if available != 1_000 || frozen != 0 || withdrawalOrderCount(t, customerB) != 0 {
		t.Fatalf("unowned card mutated state: available=%d frozen=%d orders=%d", available, frozen, withdrawalOrderCount(t, customerB))
	}

	path := "/api/v1/bank-cards/" + strconv.FormatInt(cardID, 10)
	if resp, body := doJSON(t, http.MethodDelete, path, tokenA, nil); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete unused card: status = %d, body = %v", resp.StatusCode, body)
	}
	resp, body = createWithdrawal(t, tokenA, 100, cardID, "anything")
	if resp.StatusCode != http.StatusForbidden || body["code"] != "WITHDRAWAL_PASSWORD_NOT_SET" {
		t.Fatalf("password check must precede deleted card lookup: status = %d, body = %v", resp.StatusCode, body)
	}
	if resp, body := setWithdrawalPassword(t, tokenA, "secret"); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("set A password: status = %d, body = %v", resp.StatusCode, body)
	}
	resp, body = createWithdrawal(t, tokenA, 100, cardID, "secret")
	if resp.StatusCode != http.StatusNotFound || body["code"] != "BANK_CARD_NOT_FOUND" {
		t.Fatalf("deleted card: status = %d, body = %v", resp.StatusCode, body)
	}
}

func TestWithdrawalListIsOwnedAndMasksPayeeAccount(t *testing.T) {
	usernameA := uniqueUsername(t)
	tokenA := registerAndLogin(t, usernameA)
	usernameB := uniqueUsername(t)
	tokenB := registerAndLogin(t, usernameB)

	for _, account := range []struct {
		token    string
		cardNo   string
		password string
	}{
		{token: tokenA, cardNo: "6222020202020505", password: "password-a"},
		{token: tokenB, cardNo: "6222020202020606", password: "password-b"},
	} {
		fundForWithdrawal(t, account.token, 1_000)
		if resp, body := setWithdrawalPassword(t, account.token, account.password); resp.StatusCode != http.StatusNoContent {
			t.Fatalf("set password: status = %d, body = %v", resp.StatusCode, body)
		}
		cardID := addWithdrawalCard(t, account.token, account.cardNo)
		if resp, body := createWithdrawal(t, account.token, 100, cardID, account.password); resp.StatusCode != http.StatusCreated {
			t.Fatalf("create withdrawal: status = %d, body = %v", resp.StatusCode, body)
		}
	}

	resp, body := getJSON(t, "/api/v1/withdrawal/orders", tokenA)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list withdrawals: status = %d, body = %v", resp.StatusCode, body)
	}
	items, ok := body["items"].([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("A withdrawal items = %v, want exactly one own order", body["items"])
	}
	item := items[0].(map[string]any)
	if item["status"] != "pending_review" || item["payee_account"] != "6222****0505" {
		t.Errorf("listed withdrawal = %v, want own pending order with masked account", item)
	}
	if item["payee_name"] != "张三" || item["payee_bank"] != "中国工商银行" {
		t.Errorf("listed payee snapshot = %v", item)
	}
}

func TestWithdrawalActiveOrderGuardsBankCardDelete(t *testing.T) {
	username := uniqueUsername(t)
	token := registerAndLogin(t, username)
	fundForWithdrawal(t, token, 1_000)
	if resp, body := setWithdrawalPassword(t, token, "secret"); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("set password: status = %d, body = %v", resp.StatusCode, body)
	}
	cardID := addWithdrawalCard(t, token, "6222020202020707")
	if resp, body := createWithdrawal(t, token, 100, cardID, "secret"); resp.StatusCode != http.StatusCreated {
		t.Fatalf("create withdrawal: status = %d, body = %v", resp.StatusCode, body)
	}

	path := "/api/v1/bank-cards/" + strconv.FormatInt(cardID, 10)
	resp, body := doJSON(t, http.MethodDelete, path, token, nil)
	if resp.StatusCode != http.StatusConflict || body["code"] != "BANK_CARD_IN_USE" {
		t.Fatalf("delete in-use card: status = %d, body = %v", resp.StatusCode, body)
	}
	resp, body = getJSON(t, "/api/v1/bank-cards", token)
	items, _ := body["items"].([]any)
	if resp.StatusCode != http.StatusOK || len(items) != 1 {
		t.Fatalf("card list after rejected delete: status = %d, body = %v", resp.StatusCode, body)
	}

	var account, name, bank string
	if err := testPool.QueryRow(context.Background(),
		`SELECT payee_account, payee_name, payee_bank FROM withdrawal_orders WHERE bank_card_id=$1`, cardID,
	).Scan(&account, &name, &bank); err != nil {
		t.Fatalf("query payee snapshot: %v", err)
	}
	if account != "6222020202020707" || name != "张三" || bank != "中国工商银行" {
		t.Errorf("payee snapshot changed after delete attempt: (%q, %q, %q)", account, name, bank)
	}
}

func TestWithdrawalPasswordCanBeChanged(t *testing.T) {
	username := uniqueUsername(t)
	token := registerAndLogin(t, username)
	fundForWithdrawal(t, token, 1_000)
	cardID := addWithdrawalCard(t, token, "6222020202020808")
	for _, password := range []string{"old-password", "new-password"} {
		if resp, body := setWithdrawalPassword(t, token, password); resp.StatusCode != http.StatusNoContent {
			t.Fatalf("set password %q: status = %d, body = %v", password, resp.StatusCode, body)
		}
	}

	resp, body := createWithdrawal(t, token, 100, cardID, "old-password")
	if resp.StatusCode != http.StatusForbidden || body["code"] != "WITHDRAWAL_PASSWORD_MISMATCH" {
		t.Fatalf("old password: status = %d, body = %v", resp.StatusCode, body)
	}
	resp, body = createWithdrawal(t, token, 100, cardID, "new-password")
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("new password: status = %d, body = %v", resp.StatusCode, body)
	}
}

func TestWithdrawalRequestValidation(t *testing.T) {
	token := registerAndLogin(t, uniqueUsername(t))
	for name, password := range map[string]string{
		"empty":    "",
		"too_long": strings.Repeat("密", 65),
	} {
		t.Run("password_"+name, func(t *testing.T) {
			resp, body := setWithdrawalPassword(t, token, password)
			if resp.StatusCode != http.StatusBadRequest || body["code"] != "COMMON_INVALID_ARGUMENT" {
				t.Fatalf("status = %d, body = %v", resp.StatusCode, body)
			}
		})
	}
	resp, body := createWithdrawal(t, token, 0, 1, "unused")
	if resp.StatusCode != http.StatusBadRequest || body["code"] != "COMMON_INVALID_ARGUMENT" {
		t.Fatalf("zero amount: status = %d, body = %v", resp.StatusCode, body)
	}
}
