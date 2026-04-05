package ingest

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
)

// ComputeSignature returns the hex-encoded HMAC-SHA256 of body using secret.
func ComputeSignature(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// VerifySignature does a constant-time comparison of the provided hex
// signature against the expected HMAC of body under secret.
func VerifySignature(secret, providedHex string, body []byte) bool {
	if secret == "" || providedHex == "" {
		return false
	}
	expected := ComputeSignature(secret, body)
	// hmac.Equal is constant-time on equal-length inputs.
	return hmac.Equal([]byte(expected), []byte(providedHex))
}
