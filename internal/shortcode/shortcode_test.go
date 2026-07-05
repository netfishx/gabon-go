package shortcode

import (
	"strings"
	"testing"
)

// 回归：字母表长度整除 256 时（Base32Upper 长 32），拒绝采样阈值
// 曾因 byte(256-0) 溢出为 0 而拒绝一切字节，New 无限循环。
func TestNew(t *testing.T) {
	tests := []struct {
		name     string
		alphabet string
		n        int
	}{
		{"base32_len_divides_256", Base32Upper, 8},
		{"base58", Base58, 12},
		{"tiny_alphabet", "ab", 16},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := New(tt.alphabet, tt.n)
			if err != nil {
				t.Fatalf("New() error = %v", err)
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
