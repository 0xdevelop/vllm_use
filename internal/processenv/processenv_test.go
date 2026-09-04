package processenv

import (
	"slices"
	"testing"
)

func TestWithoutManagerCredentials(t *testing.T) {
	env := []string{
		"PATH=/usr/bin",
		"VLLM_USE_ADMIN_TOKEN=admin-secret",
		"VLLM_USE_UPSTREAM_API_KEY=upstream-secret",
		"VLLM_USE_DATABASE=/data/state.db",
		"MALFORMED",
	}
	got := WithoutManagerCredentials(env)
	want := []string{"PATH=/usr/bin", "VLLM_USE_DATABASE=/data/state.db", "MALFORMED"}
	if !slices.Equal(got, want) {
		t.Fatalf("sanitized environment = %#v, want %#v", got, want)
	}
	if &got[0] == &env[0] {
		t.Fatal("sanitized environment aliases caller storage")
	}
}
