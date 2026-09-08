package httpauth

import (
	"net/http"
	"strings"
	"testing"
)

func TestBearerRequiresOneCanonicalHeader(t *testing.T) {
	valid := http.Header{"Authorization": {"Bearer token_123"}}
	if token, ok := Bearer(valid); !ok || token != "token_123" {
		t.Fatalf("valid bearer rejected: token=%q ok=%v", token, ok)
	}
	for _, header := range []http.Header{
		{},
		{"Authorization": {"bearer token_123"}},
		{"Authorization": {"Bearer  token_123"}},
		{"Authorization": {"Bearer token_123", "Bearer other"}},
		{"Authorization": {"Bearer token_123, Bearer other"}},
		{"Authorization": {"Bearer " + strings.Repeat("a", MaxCredentialBytes+1)}},
	} {
		if token, ok := Bearer(header); ok || token != "" {
			t.Fatalf("ambiguous bearer accepted: header=%#v token=%q", header, token)
		}
	}
}

func TestSingleCredentialRequiresVisibleASCII(t *testing.T) {
	for _, value := range []string{"", "with space", "with,comma", "中文", "line\nbreak", strings.Repeat("a", MaxCredentialBytes+1)} {
		header := http.Header{"X-Api-Key": {value}}
		if token, ok := SingleCredential(header, "X-API-Key"); ok || token != "" {
			t.Fatalf("unsafe credential %q accepted", value)
		}
	}
}
