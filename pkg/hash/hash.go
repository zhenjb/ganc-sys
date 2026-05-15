package hash

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

const HexPrefix = "0x"

func SHA256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return HexPrefix + hex.EncodeToString(sum[:])
}

func SHA256HexString(s string) string {
	return SHA256Hex([]byte(s))
}

func IsHexPrefixed(s string) bool {
	return strings.HasPrefix(s, HexPrefix)
}

func StripHex(s string) string {
	if IsHexPrefixed(s) {
		return s[len(HexPrefix):]
	}
	return s
}
