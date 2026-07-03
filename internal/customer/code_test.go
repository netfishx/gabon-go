package customer

import (
	"strings"
	"testing"
)

// 回归：字母表长度整除 256 时（inviteCodeAlphabet 长 32），拒绝采样阈值
// 曾因 byte(256-0) 溢出为 0 而拒绝一切字节，randomString 无限循环。
func TestRandomString(t *testing.T) {
	tests := []struct {
		name     string
		alphabet string
		n        int
	}{
		{"invite_code_alphabet_len_divides_256", inviteCodeAlphabet, inviteCodeLen},
		{"public_id_alphabet", publicIDAlphabet, publicIDLen},
		{"tiny_alphabet", "ab", 16},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := randomString(tt.alphabet, tt.n)
			if err != nil {
				t.Fatalf("randomString() error = %v", err)
			}
			if len(got) != tt.n {
				t.Errorf("len = %d, want %d", len(got), tt.n)
			}
			for _, c := range got {
				if !strings.ContainsRune(tt.alphabet, c) {
					t.Errorf("char %q not in alphabet", c)
				}
			}
		})
	}
}
