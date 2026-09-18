package notify

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"
)

func TestSignatureUsesTimestampDotRawBody(t *testing.T) {
	secret := []byte("correct horse battery staple")
	body := []byte("{\"message\":\"unaltered bytes\"}\n")
	timestamp := time.Unix(1_789_732_800, 0).UTC()

	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte("1789732800."))
	_, _ = mac.Write(body)
	want := hex.EncodeToString(mac.Sum(nil))

	got := Sign(secret, timestamp, body)
	if got != want {
		t.Fatalf("Sign() = %q, want %q", got, want)
	}
	if !Verify(secret, timestamp, body, got) {
		t.Fatal("Verify() rejected valid signature")
	}
	changed := append([]byte(nil), body...)
	changed[len(changed)-2] ^= 1
	if Verify(secret, timestamp, changed, got) {
		t.Fatal("Verify() accepted changed raw body")
	}
	if Verify(secret, timestamp, body, "V1="+got) {
		t.Fatal("Verify() accepted a non-hex signature value")
	}
}
