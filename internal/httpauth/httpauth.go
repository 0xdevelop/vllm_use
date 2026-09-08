// Package httpauth provides strict parsing for credentials carried in HTTP headers.
package httpauth

import (
	"net/http"
	"strings"
)

const MaxCredentialBytes = 4096

// ValidCredential reports whether value is a bounded visible-ASCII token that
// can be represented unambiguously in one HTTP header field.
func ValidCredential(value string) bool {
	if value == "" || len(value) > MaxCredentialBytes {
		return false
	}
	for i := 0; i < len(value); i++ {
		if value[i] < 0x21 || value[i] > 0x7e || value[i] == ',' {
			return false
		}
	}
	return true
}

// Bearer accepts exactly one Authorization field with the canonical
// "Bearer <token>" form. Duplicate and comma-combined fields fail closed.
func Bearer(header http.Header) (string, bool) {
	values := header.Values("Authorization")
	if len(values) != 1 || !strings.HasPrefix(values[0], "Bearer ") {
		return "", false
	}
	token := strings.TrimPrefix(values[0], "Bearer ")
	if !ValidCredential(token) {
		return "", false
	}
	return token, true
}

// SingleCredential accepts exactly one value for a non-Authorization
// credential header, such as Anthropic's X-API-Key.
func SingleCredential(header http.Header, name string) (string, bool) {
	values := header.Values(name)
	if len(values) != 1 || !ValidCredential(values[0]) {
		return "", false
	}
	return values[0], true
}

func Present(header http.Header, name string) bool {
	return len(header.Values(name)) != 0
}
