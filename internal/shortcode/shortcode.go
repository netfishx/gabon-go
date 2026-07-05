// Package shortcode 加密安全的随机短码生成（public_id、邀请码、存储路径随机名）。
package shortcode

import (
	"crypto/rand"
	"fmt"
)

const (
	// Base58 去除 0OIl 歧义字符
	Base58 = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"
	// Base32Upper 大写+数字，去除 01OI 歧义字符（邀请码用）
	Base32Upper = "23456789ABCDEFGHJKLMNPQRSTUVWXYZ"
)

// New 用拒绝采样生成无模偏差的随机短码。
// 阈值必须用 int 比较：字母表长度整除 256 时（如 32），byte(256) 会溢出为 0 导致拒绝一切字节。
func New(alphabet string, n int) (string, error) {
	limit := 256 - 256%len(alphabet)
	out := make([]byte, 0, n)
	buf := make([]byte, n*2)
	for len(out) < n {
		if _, err := rand.Read(buf); err != nil {
			return "", fmt.Errorf("read random: %w", err)
		}
		for _, b := range buf {
			if int(b) >= limit {
				continue
			}
			out = append(out, alphabet[int(b)%len(alphabet)])
			if len(out) == n {
				break
			}
		}
	}
	return string(out), nil
}
