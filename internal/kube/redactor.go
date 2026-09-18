package kube

import (
	"regexp"
	"strings"
)

const RedactedValue = "[REDACTED]"

var (
	sensitiveAssignment = regexp.MustCompile(`(?i)\b(secret|token|password|authorization|credential)([-_.a-z0-9]*)\s*([=:])\s*(?:"[^"]*"|'[^']*'|[^\s,;]+)`)
	authorizationValue  = regexp.MustCompile(`(?i)\bauthorization\s+(?:bearer\s+)?[^\s,;]+`)
)

// IsSensitiveKey reports whether a structured field name indicates credential data.
func IsSensitiveKey(key string) bool {
	key = strings.ToLower(key)
	for _, marker := range []string{"secret", "token", "password", "authorization", "credential"} {
		if strings.Contains(key, marker) {
			return true
		}
	}
	return false
}

// RedactText removes common credential assignments from allowlisted free text.
func RedactText(value string) string {
	value = sensitiveAssignment.ReplaceAllString(value, `$1$2$3`+RedactedValue)
	return authorizationValue.ReplaceAllString(value, "authorization "+RedactedValue)
}

// RedactFields returns a recursively copied log-field map with credential fields removed.
func RedactFields(fields map[string]any) map[string]any {
	return redactMap(fields)
}

func redactMap(fields map[string]any) map[string]any {
	if fields == nil {
		return nil
	}
	redacted := make(map[string]any, len(fields))
	for key, value := range fields {
		if IsSensitiveKey(key) {
			redacted[key] = RedactedValue
			continue
		}
		redacted[key] = redactFieldValue(value)
	}
	return redacted
}

func redactFieldValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return redactMap(typed)
	case map[string]string:
		redacted := make(map[string]string, len(typed))
		for key, item := range typed {
			if IsSensitiveKey(key) {
				redacted[key] = RedactedValue
			} else {
				redacted[key] = RedactText(item)
			}
		}
		return redacted
	case []any:
		redacted := make([]any, len(typed))
		for i := range typed {
			redacted[i] = redactFieldValue(typed[i])
		}
		return redacted
	case string:
		return RedactText(typed)
	default:
		return value
	}
}
