package customer

import "github.com/netfishx/gabon-go/internal/shortcode"

const (
	publicIDLen   = 12
	inviteCodeLen = 8
)

func newPublicID() (string, error) {
	return shortcode.New(shortcode.Base58, publicIDLen)
}

func newInviteCode() (string, error) {
	return shortcode.New(shortcode.Base32Upper, inviteCodeLen)
}
