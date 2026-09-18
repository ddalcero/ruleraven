package notify

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"time"
)

// Sign returns the lowercase hexadecimal HMAC-SHA256 of
// <unix-seconds>.<raw-body>. The body must be the exact bytes sent on the wire.
func Sign(secret []byte, timestamp time.Time, rawBody []byte) string {
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(strconv.FormatInt(timestamp.Unix(), 10)))
	_, _ = mac.Write([]byte("."))
	_, _ = mac.Write(rawBody)
	return hex.EncodeToString(mac.Sum(nil))
}

// Verify decodes the expected lowercase hex digest and compares it in constant
// time. Non-canonical encodings are rejected.
func Verify(secret []byte, timestamp time.Time, rawBody []byte, signature string) bool {
	if len(signature) != sha256.Size*2 {
		return false
	}
	decoded, err := hex.DecodeString(signature)
	if err != nil || hex.EncodeToString(decoded) != signature {
		return false
	}
	actual, err := hex.DecodeString(Sign(secret, timestamp, rawBody))
	if err != nil {
		return false
	}
	return hmac.Equal(actual, decoded)
}
