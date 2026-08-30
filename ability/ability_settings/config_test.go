package ability_settings

import (
	"path/filepath"
	"reflect"
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

func TestDefaultReadsEnvironmentAndDerivesDataPaths(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "state")
	hfHome := filepath.Join(t.TempDir(), "hf-home")
	t.Setenv("VLLM_USE_LISTEN", "127.0.0.1:19090")
	t.Setenv("VLLM_USE_DATA_DIR", dataDir)
	t.Setenv("VLLM_USE_VLLM_BINARY", "/opt/vllm/bin/vllm")
	t.Setenv("VLLM_USE_HF_CLI", "/opt/hf/bin/hf")
	t.Setenv("VLLM_USE_HF_HOME", hfHome)
	t.Setenv("VLLM_USE_MAX_DOWNLOAD_WORKERS", "7")
	t.Setenv("VLLM_USE_UPSTREAM", "http://127.0.0.1:19000")
	t.Setenv("VLLM_USE_READINESS_TIMEOUT", "45s")
	t.Setenv("VLLM_USE_SHUTDOWN_GRACE", "4s")
	t.Setenv("VLLM_USE_HEALTH_INTERVAL", "350ms")
	t.Setenv("VLLM_USE_MCP_ALLOWED_ORIGINS", "https://one.example, https://two.example")
	c := Default()
	if c.Listen != "127.0.0.1:19090" || c.DataDir != dataDir || c.Database != filepath.Join(dataDir, "vllm-use.db") || c.ModelsDir != filepath.Join(dataDir, "models") {
		t.Fatalf("path defaults %#v", c)
	}
	if c.VLLMBinary != "/opt/vllm/bin/vllm" || c.HFCLI != "/opt/hf/bin/hf" || c.HFHome != hfHome || c.MaxDownloadWorkers != 7 || c.Upstream != "http://127.0.0.1:19000" {
		t.Fatalf("defaults %#v", c)
	}
	if c.ReadinessTimeout != 45*time.Second || c.ShutdownGrace != 4*time.Second || c.HealthInterval != 350*time.Millisecond {
		t.Fatalf("duration defaults %#v", c)
	}
	if !reflect.DeepEqual(c.MCPAllowedOrigins, []string{"https://one.example", "https://two.example"}) {
		t.Fatalf("origins %#v", c.MCPAllowedOrigins)
	}
	c.HFHome = "relative"
	if err := c.Validate(); err == nil {
		t.Fatal("relative HF home accepted")
	}
}

func TestDefaultHonorsExplicitDatabaseAndModelsEnvironment(t *testing.T) {
	t.Setenv("VLLM_USE_DATA_DIR", filepath.Join(t.TempDir(), "data"))
	database := filepath.Join(t.TempDir(), "database", "state.db")
	models := filepath.Join(t.TempDir(), "model-store")
	t.Setenv("VLLM_USE_DATABASE", database)
	t.Setenv("VLLM_USE_MODELS_DIR", models)
	c := Default()
	if c.Database != database || c.ModelsDir != models {
		t.Fatalf("explicit paths ignored: %#v", c)
	}
}

func TestDefaultMakesInvalidNumericEnvironmentFailValidation(t *testing.T) {
	for _, tc := range []struct{ key, value string }{
		{"VLLM_USE_MAX_DOWNLOAD_WORKERS", "many"},
		{"VLLM_USE_READINESS_TIMEOUT", "soon"},
		{"VLLM_USE_SHUTDOWN_GRACE", "later"},
		{"VLLM_USE_HEALTH_INTERVAL", "often"},
	} {
		t.Run(tc.key, func(t *testing.T) {
			for _, key := range []string{"VLLM_USE_MAX_DOWNLOAD_WORKERS", "VLLM_USE_READINESS_TIMEOUT", "VLLM_USE_SHUTDOWN_GRACE", "VLLM_USE_HEALTH_INTERVAL"} {
				t.Setenv(key, "")
			}
			t.Setenv(tc.key, tc.value)
			if err := Default().Validate(); err == nil {
				t.Fatalf("invalid %s=%q accepted", tc.key, tc.value)
			}
		})
	}
}
