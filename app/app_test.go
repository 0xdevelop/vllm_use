package app

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestParseConfigPreservesEnvironmentAndAppliesFlags(t *testing.T) {
	t.Setenv("VLLM_USE_ADMIN_TOKEN", "environment-token")
	t.Setenv("VLLM_USE_HF_HOME", filepath.Join(t.TempDir(), "environment-hf"))

	cfg, err := ParseConfig([]string{"--listen", "127.0.0.1:19090", "--admin-token", "flag-token", "--mcp-allowed-origins", "https://one.example, https://two.example"}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Listen != "127.0.0.1:19090" || cfg.AdminToken != "flag-token" {
		t.Fatalf("flag overrides not applied: %+v", cfg)
	}
	if cfg.HFHome != os.Getenv("VLLM_USE_HF_HOME") {
		t.Fatalf("environment default lost: got %q", cfg.HFHome)
	}
	wantOrigins := []string{"https://one.example", "https://two.example"}
	if !reflect.DeepEqual(cfg.MCPAllowedOrigins, wantOrigins) {
		t.Fatalf("origins = %#v, want %#v", cfg.MCPAllowedOrigins, wantOrigins)
	}
}

func TestParseConfigRejectsPositionalArguments(t *testing.T) {
	if _, err := ParseConfig([]string{"unexpected"}, &bytes.Buffer{}); err == nil {
		t.Fatal("expected positional argument error")
	}
}

func TestParseConfigHelp(t *testing.T) {
	var output bytes.Buffer
	if _, err := ParseConfig([]string{"--help"}, &output); err == nil || output.Len() == 0 {
		t.Fatalf("help err=%v output=%q", err, output.String())
	}
}
