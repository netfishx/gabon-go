package customer

import (
	"crypto/rand"
	"fmt"
)

const (
	// base58：去除 0OIl 歧义字符
	publicIDAlphabet = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"
	publicIDLen      = 12
	// 邀请码：大写+数字，去除 01OI 歧义字符
	inviteCodeAlphabet = "23456789ABCDEFGHJKLMNPQRSTUVWXYZ"
	inviteCodeLen      = 8
)

func newPublicID() (string, error) {
	return randomString(publicIDAlphabet, publicIDLen)
}

func newInviteCode() (string, error) {
	return randomString(inviteCodeAlphabet, inviteCodeLen)
}

// randomString 用拒绝采样避免模偏差。
// 阈值必须用 int 比较：字母表长度整除 256 时（如 32），byte(256) 会溢出为 0 导致拒绝一切字节。
func randomString(alphabet string, n int) (string, error) {
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
