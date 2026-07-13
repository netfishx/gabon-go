package app_test

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

func TestBankCardAddThenList(t *testing.T) {
	token := registerAndLogin(t, uniqueUsername(t))

	resp, card := postJSON(t, "/api/v1/bank-cards", map[string]any{
		"card_no":     " 6222020202020202 ",
		"holder_name": " 张三 ",
		"bank_name":   " 中国工商银行 ",
		"bank_code":   " ICBC ",
		"province":    " 广东省 ",
		"city":        " 深圳市 ",
	}, token)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("add bank card: status = %d, want 201, body = %v", resp.StatusCode, card)
	}
	for field, want := range map[string]any{
		"card_no":     "6222****0202",
		"holder_name": "张三",
		"bank_name":   "中国工商银行",
		"bank_code":   "ICBC",
		"province":    "广东省",
		"city":        "深圳市",
	} {
		if got := card[field]; got != want {
			t.Errorf("added card %s = %v, want %v", field, got, want)
		}
	}
	if card["id"] == nil || card["created_at"] == nil {
		t.Errorf("added card missing id/created_at: %v", card)
	}
	cardID := int64(card["id"].(float64))
	var storedCardNo, storedHolderName, storedBankName, storedBankCode, storedProvince, storedCity string
	if err := testPool.QueryRow(context.Background(),
		`SELECT card_no, holder_name, bank_name, bank_code, province, city FROM bank_cards WHERE id = $1`, cardID,
	).Scan(&storedCardNo, &storedHolderName, &storedBankName, &storedBankCode, &storedProvince, &storedCity); err != nil {
		t.Fatalf("query stored bank card: %v", err)
	}
	if storedCardNo != "6222020202020202" {
		t.Errorf("stored card_no = %q, want full number", storedCardNo)
	}
	if storedHolderName != "张三" || storedBankName != "中国工商银行" ||
		storedBankCode != "ICBC" || storedProvince != "广东省" || storedCity != "深圳市" {
		t.Errorf("stored trimmed fields = (%q, %q, %q, %q, %q)",
			storedHolderName, storedBankName, storedBankCode, storedProvince, storedCity)
	}

	resp, body := getJSON(t, "/api/v1/bank-cards", token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list bank cards: status = %d, want 200, body = %v", resp.StatusCode, body)
	}
	items, ok := body["items"].([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("list items = %v, want one card", body["items"])
	}
	listed, ok := items[0].(map[string]any)
	if !ok {
		t.Fatalf("listed card = %T, want object", items[0])
	}
	if listed["id"] != card["id"] || listed["card_no"] != card["card_no"] {
		t.Errorf("listed card = %v, want added card %v", listed, card)
	}
}

func TestBankCardSoftDeleteKeepsRow(t *testing.T) {
	token := registerAndLogin(t, uniqueUsername(t))
	resp, card := postJSON(t, "/api/v1/bank-cards", map[string]any{
		"card_no":     "6217000012345678",
		"holder_name": "李四",
		"bank_name":   "中国建设银行",
	}, token)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("add bank card: status = %d, body = %v", resp.StatusCode, card)
	}
	cardID := int64(card["id"].(float64))

	resp, body := doJSON(t, http.MethodDelete, "/api/v1/bank-cards/"+strconv.FormatInt(cardID, 10), token, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete bank card: status = %d, want 204, body = %v", resp.StatusCode, body)
	}

	resp, body = getJSON(t, "/api/v1/bank-cards", token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list bank cards: status = %d, body = %v", resp.StatusCode, body)
	}
	if items, ok := body["items"].([]any); !ok || len(items) != 0 {
		t.Errorf("list after delete = %v, want empty items", body["items"])
	}

	var deleted bool
	if err := testPool.QueryRow(context.Background(),
		`SELECT deleted_at IS NOT NULL FROM bank_cards WHERE id = $1`, cardID).Scan(&deleted); err != nil {
		t.Fatalf("query soft-deleted bank card: %v", err)
	}
	if !deleted {
		t.Error("bank card row still exists but deleted_at is null")
	}
}

func TestBankCardDeleteRejectsOtherCustomer(t *testing.T) {
	tokenA := registerAndLogin(t, uniqueUsername(t))
	tokenB := registerAndLogin(t, uniqueUsername(t))
	resp, card := postJSON(t, "/api/v1/bank-cards", map[string]any{
		"card_no":     "6228480012345678",
		"holder_name": "王五",
		"bank_name":   "中国农业银行",
	}, tokenA)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("add A bank card: status = %d, body = %v", resp.StatusCode, card)
	}
	path := "/api/v1/bank-cards/" + strconv.FormatInt(int64(card["id"].(float64)), 10)

	resp, body := doJSON(t, http.MethodDelete, path, tokenB, nil)
	if resp.StatusCode != http.StatusNotFound || body["code"] != "BANK_CARD_NOT_FOUND" {
		t.Fatalf("B deletes A card: status = %d, body = %v, want 404 BANK_CARD_NOT_FOUND", resp.StatusCode, body)
	}

	resp, body = getJSON(t, "/api/v1/bank-cards", tokenA)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list A bank cards: status = %d, body = %v", resp.StatusCode, body)
	}
	if items, ok := body["items"].([]any); !ok || len(items) != 1 {
		t.Errorf("A list after B delete = %v, want original card", body["items"])
	}

	resp, body = getJSON(t, "/api/v1/bank-cards", tokenB)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list B bank cards: status = %d, body = %v", resp.StatusCode, body)
	}
	if items, ok := body["items"].([]any); !ok || len(items) != 0 {
		t.Errorf("B list = %v, want empty", body["items"])
	}
}

func TestBankCardAddValidatesRequiredFields(t *testing.T) {
	token := registerAndLogin(t, uniqueUsername(t))
	resp, body := postJSON(t, "/api/v1/bank-cards", map[string]any{
		"holder_name": "赵六",
		"bank_name":   "招商银行",
	}, token)
	if resp.StatusCode != http.StatusBadRequest || body["code"] != "COMMON_INVALID_ARGUMENT" {
		t.Fatalf("missing card_no: status = %d, body = %v, want 400 COMMON_INVALID_ARGUMENT", resp.StatusCode, body)
	}
	for _, tc := range []struct {
		name string
		body map[string]any
	}{
		{
			name: "card_no_non_digits",
			body: map[string]any{"card_no": "6222abcd02020202", "holder_name": "赵六", "bank_name": "招商银行"},
		},
		{
			name: "card_no_too_short",
			body: map[string]any{"card_no": "62220202020", "holder_name": "赵六", "bank_name": "招商银行"},
		},
		{
			name: "holder_name_too_long",
			body: map[string]any{"card_no": "6225880012345678", "holder_name": strings.Repeat("名", 65), "bank_name": "招商银行"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp, body := postJSON(t, "/api/v1/bank-cards", tc.body, token)
			if resp.StatusCode != http.StatusBadRequest || body["code"] != "COMMON_INVALID_ARGUMENT" {
				t.Fatalf("status = %d, body = %v, want 400 COMMON_INVALID_ARGUMENT", resp.StatusCode, body)
			}
		})
	}

	resp, body = postJSON(t, "/api/v1/bank-cards", map[string]any{
		"card_no":     "6225880012345678",
		"holder_name": "赵六",
		"bank_name":   "招商银行",
		"bank_code":   "",
		"province":    "",
		"city":        "",
	}, token)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("add with empty optional fields: status = %d, body = %v", resp.StatusCode, body)
	}
	for _, field := range []string{"bank_code", "province", "city"} {
		if _, exists := body[field]; exists {
			t.Errorf("empty optional field %s was not normalized away: %v", field, body)
		}
	}
}

func TestBankCardDeleteNotFoundAndRepeated(t *testing.T) {
	token := registerAndLogin(t, uniqueUsername(t))

	resp, body := doJSON(t, http.MethodDelete, "/api/v1/bank-cards/9223372036854775807", token, nil)
	if resp.StatusCode != http.StatusNotFound || body["code"] != "BANK_CARD_NOT_FOUND" {
		t.Fatalf("delete missing card: status = %d, body = %v, want 404 BANK_CARD_NOT_FOUND", resp.StatusCode, body)
	}

	resp, card := postJSON(t, "/api/v1/bank-cards", map[string]any{
		"card_no":     "6214830012345678",
		"holder_name": "钱七",
		"bank_name":   "招商银行",
	}, token)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("add bank card: status = %d, body = %v", resp.StatusCode, card)
	}
	path := "/api/v1/bank-cards/" + strconv.FormatInt(int64(card["id"].(float64)), 10)
	if resp, body = doJSON(t, http.MethodDelete, path, token, nil); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("first delete: status = %d, body = %v", resp.StatusCode, body)
	}
	if resp, body = doJSON(t, http.MethodDelete, path, token, nil); resp.StatusCode != http.StatusNotFound || body["code"] != "BANK_CARD_NOT_FOUND" {
		t.Fatalf("repeated delete: status = %d, body = %v, want 404 BANK_CARD_NOT_FOUND", resp.StatusCode, body)
	}

	resp, body = doJSON(t, http.MethodDelete, "/api/v1/bank-cards/not-an-id", token, nil)
	if resp.StatusCode != http.StatusBadRequest || body["code"] != "COMMON_INVALID_ARGUMENT" {
		t.Fatalf("invalid card id: status = %d, body = %v, want 400 COMMON_INVALID_ARGUMENT", resp.StatusCode, body)
	}
}
