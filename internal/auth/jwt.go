package auth

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// 受众常量：JWT aud，区分双主体。
const (
	AudienceCustomer = "customer"
	AudienceAdmin    = "admin"

	tokenTTL = 7 * 24 * time.Hour
)

// Claims 在注册声明之外携带 pwd = 签发时主体 password_changed_at 的 Unix 微秒
// （与 Postgres timestamptz 精度精确对齐，int64 微秒 < 2^53 可安全走 JSON 数字）。
// 校验时与当前值精确相等比较——改密后旧 token 全部失效，且不受时钟粒度竞态影响。
type Claims struct {
	jwt.RegisteredClaims
	PasswordStamp int64 `json:"pwd"`
}

// TokenIssuer 负责 HS256 token 的签发与校验，customer/admin 共用。
type TokenIssuer struct {
	secret []byte
}

// NewTokenIssuer 用 config 中的对称密钥构造签发器。
func NewTokenIssuer(secret []byte) *TokenIssuer {
	return &TokenIssuer{secret: secret}
}

// Issue 为主体签发带 pwd 戳的 token。
func (i *TokenIssuer) Issue(subjectID int64, audience string, passwordChangedAt time.Time) (string, error) {
	now := time.Now()
	claims := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   strconv.FormatInt(subjectID, 10),
			Audience:  jwt.ClaimStrings{audience},
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(tokenTTL)),
		},
		PasswordStamp: passwordChangedAt.UnixMicro(),
	}
	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(i.secret)
	if err != nil {
		return "", fmt.Errorf("sign token: %w", err)
	}
	return signed, nil
}

// FromRequest 从 Authorization: Bearer 头提取并校验 token，双面中间件共用。
func (i *TokenIssuer) FromRequest(r *http.Request, audience string) (subjectID, passwordStamp int64, err error) {
	raw, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || raw == "" {
		return 0, 0, fmt.Errorf("missing bearer token")
	}
	return i.Parse(raw, audience)
}

// Parse 校验签名、有效期与受众，返回主体 id 与密码戳。
func (i *TokenIssuer) Parse(token, audience string) (subjectID int64, passwordStamp int64, err error) {
	var claims Claims
	_, err = jwt.ParseWithClaims(
		token, &claims,
		func(_ *jwt.Token) (any, error) { return i.secret, nil },
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithAudience(audience),
		jwt.WithExpirationRequired(),
	)
	if err != nil {
		return 0, 0, fmt.Errorf("parse token: %w", err)
	}
	id, err := strconv.ParseInt(claims.Subject, 10, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("parse subject: %w", err)
	}
	return id, claims.PasswordStamp, nil
}
