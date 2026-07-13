// Package apierr 定义 API 错误类型、字符串错误码与统一的错误响应写出。
// 错误响应形状：4xx/5xx + {"code": "...", "message": "..."}。
package apierr

import (
	"encoding/json"
	"errors"
	"net/http"
)

// 注：本包同时承载统一响应写出（WriteJSON/DecodeJSON），见 docs/skeleton.md 对 apierr 的职责定义。

// 错误码常量：大写蛇形，"域_语义"，各域按前缀自行扩展。
const (
	CodeInvalidArgument = "COMMON_INVALID_ARGUMENT"
	CodeInternal        = "COMMON_INTERNAL"

	CodeAuthBadCredentials = "AUTH_BAD_CREDENTIALS" // #nosec G101 -- 错误码常量，非凭据
	CodeAuthUnauthorized   = "AUTH_UNAUTHORIZED"

	CodeAdminDisabled = "ADMIN_DISABLED"

	CodeCustomerNotFound          = "CUSTOMER_NOT_FOUND"
	CodeCustomerUsernameTaken     = "CUSTOMER_USERNAME_TAKEN"
	CodeCustomerInviteCodeInvalid = "CUSTOMER_INVITE_CODE_INVALID"
	CodeCustomerBanned            = "CUSTOMER_BANNED"
	CodeCustomerPhoneTaken        = "CUSTOMER_PHONE_TAKEN"
	CodeCustomerEmailTaken        = "CUSTOMER_EMAIL_TAKEN"
	CodeCustomerTeamForbidden     = "CUSTOMER_TEAM_FORBIDDEN"

	CodeWalletInsufficientBalance = "WALLET_INSUFFICIENT_BALANCE"

	CodeUploadPathForbidden = "UPLOAD_PATH_FORBIDDEN"
	CodeUploadObjectMissing = "UPLOAD_OBJECT_MISSING"

	CodeClaimTaskNotFound       = "CLAIM_TASK_NOT_FOUND"
	CodeClaimTaskOffline        = "CLAIM_TASK_OFFLINE"
	CodeClaimTaskWindowClosed   = "CLAIM_TASK_WINDOW_CLOSED"
	CodeClaimTaskVipRequired    = "CLAIM_TASK_VIP_REQUIRED"
	CodeClaimTaskAlreadyClaimed = "CLAIM_TASK_ALREADY_CLAIMED"
	CodeClaimTaskNotSubmittable = "CLAIM_TASK_NOT_SUBMITTABLE"
	CodeClaimTaskNotReviewable  = "CLAIM_TASK_NOT_REVIEWABLE"

	CodeAdminForbidden = "ADMIN_FORBIDDEN"

	CodeSignInAlreadyToday = "SIGN_IN_ALREADY_TODAY"

	CodeAdNotFound = "AD_NOT_FOUND"

	CodeVideoPathForbidden     = "VIDEO_PATH_FORBIDDEN"
	CodeVideoObjectMissing     = "VIDEO_OBJECT_MISSING"
	CodeVideoNotFound          = "VIDEO_NOT_FOUND"
	CodeVideoNotReviewable     = "VIDEO_NOT_REVIEWABLE"
	CodeVideoCommentDailyLimit = "VIDEO_COMMENT_DAILY_LIMIT"
	CodeVideoCommentNotFound   = "VIDEO_COMMENT_NOT_FOUND"
	CodeVideoUploadLimit       = "VIDEO_UPLOAD_LIMIT_REACHED"

	CodeVipNotUpgrade = "VIP_NOT_UPGRADE"

	CodeRechargeMethodUnsupported = "RECHARGE_METHOD_UNSUPPORTED"

	CodeBankCardNotFound = "BANK_CARD_NOT_FOUND"
)

// Error 是全 API 统一的业务错误：HTTP status 承载大类，Code 承载细类。
type Error struct {
	Status  int    `json:"-"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

// New 构造带状态码与错误码的业务错误。
func New(status int, code, message string) *Error {
	return &Error{Status: status, Code: code, Message: message}
}

// InvalidArgument 400 参数错误的便捷构造。
func InvalidArgument(message string) *Error {
	return New(http.StatusBadRequest, CodeInvalidArgument, message)
}

// Unauthorized 401 未认证的便捷构造。
func Unauthorized() *Error {
	return New(http.StatusUnauthorized, CodeAuthUnauthorized, "authentication required")
}

// WriteJSON 成功响应：2xx + data 直出（status-first，无 envelope）。api/admin 两面共用。
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// DecodeJSON 解码请求体；失败时写统一 400 并返回 false。
func DecodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		Write(w, InvalidArgument("malformed JSON body"))
		return false
	}
	return true
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
