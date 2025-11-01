package git

import (
	"crypto/sha256"
	"encoding/hex"
)

// CalculateHash computes SHA256 hash of content
func CalculateHash(content string) string {
	h := sha256.New()
	h.Write([]byte(content))
	return hex.EncodeToString(h.Sum(nil))
}
