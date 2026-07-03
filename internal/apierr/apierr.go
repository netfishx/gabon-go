// Package apierr 定义 API 错误类型、字符串错误码与统一的错误响应写出。
// 错误响应形状：4xx/5xx + {"code": "...", "message": "..."}。
package apierr

import (
	"encoding/json"
	"errors"
	"net/http"
)

// 错误码常量：大写蛇形，"域_语义"，各域按前缀自行扩展。
const (
	CodeInvalidArgument = "COMMON_INVALID_ARGUMENT"
	CodeInternal        = "COMMON_INTERNAL"

	CodeCustomerUsernameTaken = "CUSTOMER_USERNAME_TAKEN"
)

type Error struct {
	Status  int    `json:"-"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

func New(status int, code, message string) *Error {
	return &Error{Status: status, Code: code, Message: message}
}

func InvalidArgument(message string) *Error {
	return New(http.StatusBadRequest, CodeInvalidArgument, message)
}

// Write 将错误写为统一 JSON 响应；非 *Error 一律 500 且不泄露内部信息。
func Write(w http.ResponseWriter, err error) {
	var apiErr *Error
	if !errors.As(err, &apiErr) {
		apiErr = New(http.StatusInternalServerError, CodeInternal, "internal error")
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(apiErr.Status)
	_ = json.NewEncoder(w).Encode(apiErr)
}
