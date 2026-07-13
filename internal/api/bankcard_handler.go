package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/netfishx/gabon-go/internal/apierr"
	"github.com/netfishx/gabon-go/internal/bankcard"
	"github.com/netfishx/gabon-go/internal/db"
)

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
	if req.CardNo == "" {
		apierr.Write(w, apierr.InvalidArgument("card_no is required"))
		return
	}
	if req.HolderName == "" {
		apierr.Write(w, apierr.InvalidArgument("holder_name is required"))
		return
	}
	if req.BankName == "" {
		apierr.Write(w, apierr.InvalidArgument("bank_name is required"))
		return
	}

	card, err := h.BankCards.Add(r.Context(), customerFrom(r.Context()).ID, bankcard.AddParams{
		CardNo:     req.CardNo,
		HolderName: req.HolderName,
		BankName:   req.BankName,
		BankCode:   nonEmpty(req.BankCode),
		Province:   nonEmpty(req.Province),
		City:       nonEmpty(req.City),
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
	if value == nil || *value == "" {
		return nil
	}
	return value
}

func toBankCardResponse(card db.BankCard) bankCardResponse {
	return bankCardResponse{
		ID:         card.ID,
		CardNo:     card.CardNo,
		HolderName: card.HolderName,
		BankName:   card.BankName,
		BankCode:   card.BankCode,
		Province:   card.Province,
		City:       card.City,
		CreatedAt:  card.CreatedAt.Time,
	}
}
