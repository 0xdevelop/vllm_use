package app

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestParseConfigPreservesEnvironmentAndAppliesFlags(t *testing.T) {
	t.Setenv("VLLM_USE_ADMIN_TOKEN", "environment-token")
	t.Setenv("VLLM_USE_HF_HOME", filepath.Join(t.TempDir(), "environment-hf"))
	t.Setenv("VLLM_USE_UPSTREAM", "http://127.0.0.1:18000")
	t.Setenv("VLLM_USE_READINESS_TIMEOUT", "40s")
	t.Setenv("VLLM_USE_MAX_AUDIT_RECORDS", "2000")

	cfg, err := ParseConfig([]string{"--listen", "127.0.0.1:19090", "--admin-token", "flag-token", "--upstream", "http://127.0.0.1:28000", "--readiness-timeout", "50s", "--shutdown-grace", "5s", "--health-interval", "500ms", "--max-audit-records", "3000", "--mcp-allowed-origins", "https://one.example, https://two.example"}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Listen != "127.0.0.1:19090" || cfg.AdminToken != "flag-token" {
		t.Fatalf("flag overrides not applied: %+v", cfg)
	}
	if cfg.HFHome != os.Getenv("VLLM_USE_HF_HOME") {
		t.Fatalf("environment default lost: got %q", cfg.HFHome)
	}
	if cfg.Upstream != "http://127.0.0.1:28000" || cfg.ReadinessTimeout != 50*time.Second || cfg.ShutdownGrace != 5*time.Second || cfg.HealthInterval != 500*time.Millisecond || cfg.MaxAuditRecords != 3000 {
		t.Fatalf("runtime flag overrides not applied: %+v", cfg)
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

func TestParseConfigDataDirDerivesPathsUnlessExplicitlyConfigured(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "data")
	cfg, err := ParseConfig([]string{"--data-dir", dataDir}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Database != filepath.Join(dataDir, "vllm-use.db") || cfg.ModelsDir != filepath.Join(dataDir, "models") {
		t.Fatalf("data-dir did not derive child paths: %+v", cfg)
	}

	explicitDB := filepath.Join(t.TempDir(), "explicit.db")
	t.Setenv("VLLM_USE_DATABASE", explicitDB)
	customModels := filepath.Join(dataDir, "custom-models")
	cfg, err = ParseConfig([]string{"--data-dir", dataDir, "--models-dir", customModels}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Database != explicitDB || cfg.ModelsDir != customModels {
		t.Fatalf("explicit child paths overwritten: %+v", cfg)
	}
}

func TestParseConfigHelp(t *testing.T) {
	var output bytes.Buffer
	if _, err := ParseConfig([]string{"--help"}, &output); err == nil || output.Len() == 0 {
		t.Fatalf("help err=%v output=%q", err, output.String())
	}
}
