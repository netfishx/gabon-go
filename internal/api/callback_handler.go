package api

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"

	"github.com/go-chi/chi/v5"

	"github.com/netfishx/gabon-go/internal/payment"
)

// callbackMaxBody 回调体上限（防滥用；正常渠道报文远小于此）。
const callbackMaxBody = 1 << 20 // 1 MiB

// CallbackRoutes 组装支付回调路由。挂在 /callback（/api/v1 之外、无 JWT）——
// 身份完全靠 Provider.ParseCallback 验签，见 PRD #63。
func (h *Handler) CallbackRoutes() chi.Router {
	r := chi.NewRouter()
	r.Post("/{provider}/pay", h.handlePayCallback)
	return r
}

func (h *Handler) handlePayCallback(w http.ResponseWriter, r *http.Request) {
	providerCode := chi.URLParam(r, "provider")

	body, err := io.ReadAll(io.LimitReader(r.Body, callbackMaxBody))
	if err != nil {
		slog.WarnContext(r.Context(), "read callback body failed", "provider", providerCode, "error", err)
		http.Error(w, "fail", http.StatusBadRequest)
		return
	}
	// 表单渠道从 body 解析；JSON/Query 渠道各读 Body/Query（best-effort，格式不符时留空）。
	form, _ := url.ParseQuery(string(body))

	ack, err := h.Payments.HandlePayCallback(r.Context(), providerCode, &payment.CallbackRequest{
		Body:  body,
		Form:  form,
		Query: r.URL.Query(),
	})
	if errors.Is(err, payment.ErrUnknownProvider) {
		http.Error(w, "unknown provider", http.StatusNotFound)
		return
	}
	if err != nil {
		slog.ErrorContext(r.Context(), "pay callback failed", "provider", providerCode, "error", err)
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", ack.ContentType)
	w.WriteHeader(http.StatusOK)
	// #nosec G705 -- 回调应答由 Provider 生成、机器对机器消费（非浏览器渲染），Content-Type 为 text/plain
	_, _ = w.Write(ack.Body)
}
