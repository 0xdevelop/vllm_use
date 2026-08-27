package ability_settings

import (
	"path/filepath"
	"testing"
	"time"
)

func TestValidationAndLoopback(t *testing.T) {
	d := t.TempDir()
	c := Config{Listen: "127.0.0.1:8080", DataDir: d, Database: filepath.Join(d, "db"), ModelsDir: filepath.Join(d, "models"), VLLMBinary: "vllm", HFCLI: "hf", ReadinessTimeout: time.Second, ShutdownGrace: time.Second, MaxDownloadWorkers: 1}
	if e := c.Validate(); e != nil {
		t.Fatal(e)
	}
	if !c.IsLoopback() {
		t.Fatal("loopback not detected")
	}
	c.MCPAllowedOrigins = []string{"https://admin.example"}
	if e := c.Validate(); e != nil {
		t.Fatalf("trusted origin rejected: %v", e)
	}
	c.MCPAllowedOrigins = []string{"not-an-origin"}
	if e := c.Validate(); e == nil {
		t.Fatal("invalid trusted origin accepted")
	}
	c.MCPAllowedOrigins = nil
	c.Listen = "0.0.0.0:8080"
	if c.IsLoopback() {
		t.Fatal("wildcard treated as loopback")
	}
	c.ModelsDir = "relative"
	if e := c.Validate(); e == nil {
		t.Fatal("relative path accepted")
	}
}

func TestDefaultDownloadEnvironment(t *testing.T) {
	t.Setenv("VLLM_USE_HF_HOME", "/tmp/hf-home")
	t.Setenv("VLLM_USE_MAX_DOWNLOAD_WORKERS", "7")
	c := Default()
	if c.HFHome != "/tmp/hf-home" || c.MaxDownloadWorkers != 7 {
		t.Fatalf("defaults %#v", c)
	}
	c.HFHome = "relative"
	if err := c.Validate(); err == nil {
		t.Fatal("relative HF home accepted")
	}
}
