package kube_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/ddalcero/ruleraven/internal/kube"
)

func TestRedactFieldsRemovesSensitiveValues(t *testing.T) {
	const sentinel = "LEAK_SENTINEL_DO_NOT_COPY"
	for _, key := range []string{"clientSecret", "api_token", "PASSWORD", "Authorization", "dbCredential"} {
		t.Run(key, func(t *testing.T) {
			fields := map[string]any{
				"safe": "visible",
				"nested": map[string]any{
					key: sentinel,
				},
				"items": []any{map[string]any{key: sentinel}},
			}
			redacted := kube.RedactFields(fields)
			encoded, err := json.Marshal(redacted)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(encoded), sentinel) {
				t.Fatalf("redacted structured fields leaked value: %s", encoded)
			}
			if redacted["safe"] != "visible" {
				t.Fatalf("safe field was changed: %#v", redacted)
			}
		})
	}
}

func TestRedactTextRemovesCredentialAssignments(t *testing.T) {
	const sentinel = "LEAK_SENTINEL_DO_NOT_COPY"
	for _, input := range []string{
		"token=" + sentinel,
		"password: " + sentinel,
		"Authorization Bearer " + sentinel,
		`credential="` + sentinel + `"`,
	} {
		redacted := kube.RedactText(input)
		if strings.Contains(redacted, sentinel) || !strings.Contains(redacted, kube.RedactedValue) {
			t.Fatalf("RedactText(%q) = %q", input, redacted)
		}
	}
}

func FuzzRedactText(f *testing.F) {
	f.Add("token", "value")
	f.Add("password", "hunter2")
	f.Fuzz(func(t *testing.T, key, value string) {
		if !kube.IsSensitiveKey(key) || value == "" || strings.ContainsAny(value, " \t\r\n,;") {
			return
		}
		redacted := kube.RedactText(key + "=" + value)
		if strings.Contains(redacted, value) {
			t.Fatalf("sensitive value remained in %q", redacted)
		}
	})
}
