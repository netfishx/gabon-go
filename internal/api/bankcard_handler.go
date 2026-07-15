package api

import (
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"

	"github.com/netfishx/gabon-go/internal/apierr"
	"github.com/netfishx/gabon-go/internal/bankcard"
	"github.com/netfishx/gabon-go/internal/db"
)

var bankCardNoPattern = regexp.MustCompile(`^\d{12,19}$`)

type bankCardAddRequest struct {
	CardNo     string  `json:"card_no"`
	HolderName string  `json:"holder_name"`
	BankName   string  `json:"bank_name"`
	BankCode   *string `json:"bank_code"`
	Province   *string `json:"province"`
	City       *string `json:"city"`
}

type bankCardResponse struct {
	ID         int64     `json:"id"`
	CardNo     string    `json:"card_no"`
	HolderName string    `json:"holder_name"`
	BankName   string    `json:"bank_name"`
	BankCode   *string   `json:"bank_code,omitempty"`
	Province   *string   `json:"province,omitempty"`
	City       *string   `json:"city,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

type bankCardListResponse struct {
	Items []bankCardResponse `json:"items"`
}

func (h *Handler) handleBankCardAdd(w http.ResponseWriter, r *http.Request) {
	var req bankCardAddRequest
	if !apierr.DecodeJSON(w, r, &req) {
		return
	}
	req.CardNo = strings.TrimSpace(req.CardNo)
	req.HolderName = strings.TrimSpace(req.HolderName)
	req.BankName = strings.TrimSpace(req.BankName)
	req.BankCode = nonEmpty(req.BankCode)
	req.Province = nonEmpty(req.Province)
	req.City = nonEmpty(req.City)
	if req.CardNo == "" {
		apierr.Write(w, apierr.InvalidArgument("card_no is required"))
		return
	}
	if !bankCardNoPattern.MatchString(req.CardNo) {
		apierr.Write(w, apierr.InvalidArgument("card_no must be 12-19 digits"))
		return
	}
	if req.HolderName == "" {
		apierr.Write(w, apierr.InvalidArgument("holder_name is required"))
		return
	}
	if utf8.RuneCountInString(req.HolderName) > 64 {
		apierr.Write(w, apierr.InvalidArgument("holder_name must be at most 64 characters"))
		return
	}
	if req.BankName == "" {
		apierr.Write(w, apierr.InvalidArgument("bank_name is required"))
		return
	}
	if utf8.RuneCountInString(req.BankName) > 64 {
		apierr.Write(w, apierr.InvalidArgument("bank_name must be at most 64 characters"))
		return
	}
	for _, field := range []struct {
		name  string
		value *string
	}{
		{name: "bank_code", value: req.BankCode},
		{name: "province", value: req.Province},
		{name: "city", value: req.City},
	} {
		if field.value != nil && utf8.RuneCountInString(*field.value) > 32 {
			apierr.Write(w, apierr.InvalidArgument(field.name+" must be at most 32 characters"))
			return
		}
	}

	card, err := h.BankCards.Add(r.Context(), customerFrom(r.Context()).ID, bankcard.AddParams{
		CardNo:     req.CardNo,
		HolderName: req.HolderName,
		BankName:   req.BankName,
		BankCode:   req.BankCode,
		Province:   req.Province,
		City:       req.City,
	})
	if err != nil {
		apierr.Write(w, err)
		return
	}
	apierr.WriteJSON(w, http.StatusCreated, toBankCardResponse(card))
}

func (h *Handler) handleBankCardList(w http.ResponseWriter, r *http.Request) {
	cards, err := h.BankCards.List(r.Context(), customerFrom(r.Context()).ID)
	if err != nil {
		apierr.Write(w, err)
		return
	}
	out := bankCardListResponse{Items: make([]bankCardResponse, 0, len(cards))}
	for _, card := range cards {
		out.Items = append(out.Items, toBankCardResponse(card))
	}
	apierr.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) handleBankCardDelete(w http.ResponseWriter, r *http.Request) {
	cardID, err := strconv.ParseInt(chi.URLParam(r, "cardID"), 10, 64)
	if err != nil {
		apierr.Write(w, apierr.InvalidArgument("cardID must be an integer"))
		return
	}
	if err := h.BankCards.SoftDelete(r.Context(), customerFrom(r.Context()).ID, cardID); err != nil {
		apierr.Write(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func nonEmpty(value *string) *string {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

func toBankCardResponse(card db.BankCard) bankCardResponse {
	return bankCardResponse{
		ID:         card.ID,
		CardNo:     maskBankCardNo(card.CardNo),
		HolderName: card.HolderName,
		BankName:   card.BankName,
		BankCode:   card.BankCode,
		Province:   card.Province,
		City:       card.City,
		CreatedAt:  card.CreatedAt.Time,
	}
}

func maskBankCardNo(cardNo string) string {
	return cardNo[:4] + "****" + cardNo[len(cardNo)-4:]
}
