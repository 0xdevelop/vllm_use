package ability_settings

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestValidationAndLoopback(t *testing.T) {
	d := t.TempDir()
	c := Config{Listen: "127.0.0.1:8080", DataDir: d, Database: filepath.Join(d, "db"), ModelsDir: filepath.Join(d, "models"), VLLMBinary: "vllm", HFCLI: "hf", Upstream: "http://127.0.0.1:8000", ReadinessTimeout: time.Second, ShutdownGrace: time.Second, MaxDownloadWorkers: 1, MaxAuditRecords: 100}
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

func TestValidationRestrictsManagedVLLMUpstreamToLoopbackRoot(t *testing.T) {
	d := t.TempDir()
	base := Config{Listen: "127.0.0.1:8080", DataDir: d, Database: filepath.Join(d, "db"), ModelsDir: filepath.Join(d, "models"), VLLMBinary: "vllm", HFCLI: "hf", ReadinessTimeout: time.Second, ShutdownGrace: time.Second, MaxDownloadWorkers: 1, MaxAuditRecords: 100}
	for _, upstream := range []string{
		"http://127.0.0.1:8000",
		"http://localhost:8000",
		"http://LOCALHOST:8000",
		"http://[::1]:8000",
	} {
		t.Run("accept_"+upstream, func(t *testing.T) {
			c := base
			c.Upstream = upstream
			if err := c.Validate(); err != nil {
				t.Fatalf("loopback upstream %q rejected: %v", upstream, err)
			}
		})
	}
	for _, upstream := range []string{
		"",
		"http://0.0.0.0:8000",
		"http://192.0.2.10:8000",
		"https://vllm.example:8000",
		"http://user:password@127.0.0.1:8000",
		"http://127.0.0.1:8000/base",
		"http://127.0.0.1:8000?token=secret",
		"http://127.0.0.1:8000#fragment",
	} {
		t.Run("reject_"+upstream, func(t *testing.T) {
			c := base
			c.Upstream = upstream
			if err := c.Validate(); err == nil {
				t.Fatalf("unsafe upstream %q accepted", upstream)
			}
		})
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
	t.Setenv("VLLM_USE_MAX_AUDIT_RECORDS", "2500")
	t.Setenv("VLLM_USE_UPSTREAM", "http://127.0.0.1:19000")
	t.Setenv("VLLM_USE_READINESS_TIMEOUT", "45s")
	t.Setenv("VLLM_USE_SHUTDOWN_GRACE", "4s")
	t.Setenv("VLLM_USE_HEALTH_INTERVAL", "350ms")
	t.Setenv("VLLM_USE_MCP_ALLOWED_ORIGINS", "https://one.example, https://two.example")
	c := Default()
	if c.Listen != "127.0.0.1:19090" || c.DataDir != dataDir || c.Database != filepath.Join(dataDir, "vllm-use.db") || c.ModelsDir != filepath.Join(dataDir, "models") {
		t.Fatalf("path defaults %#v", c)
	}
	if c.VLLMBinary != "/opt/vllm/bin/vllm" || c.HFCLI != "/opt/hf/bin/hf" || c.HFHome != hfHome || c.MaxDownloadWorkers != 7 || c.MaxAuditRecords != 2500 || c.Upstream != "http://127.0.0.1:19000" {
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
		{"VLLM_USE_MAX_AUDIT_RECORDS", "many"},
		{"VLLM_USE_READINESS_TIMEOUT", "soon"},
		{"VLLM_USE_SHUTDOWN_GRACE", "later"},
		{"VLLM_USE_HEALTH_INTERVAL", "often"},
	} {
		t.Run(tc.key, func(t *testing.T) {
			for _, key := range []string{"VLLM_USE_MAX_DOWNLOAD_WORKERS", "VLLM_USE_MAX_AUDIT_RECORDS", "VLLM_USE_READINESS_TIMEOUT", "VLLM_USE_SHUTDOWN_GRACE", "VLLM_USE_HEALTH_INTERVAL"} {
				t.Setenv(key, "")
			}
			t.Setenv(tc.key, tc.value)
			if err := Default().Validate(); err == nil {
				t.Fatalf("invalid %s=%q accepted", tc.key, tc.value)
			}
		})
	}
}

func TestPrepareRejectsUnsafeManagedDirectories(t *testing.T) {
	for _, field := range []string{"data", "models", "hf_home"} {
		t.Run(field+"_symlink", func(t *testing.T) {
			root := t.TempDir()
			target := filepath.Join(root, "operator-owned")
			if err := os.Mkdir(target, 0755); err != nil {
				t.Fatal(err)
			}
			marker := filepath.Join(target, "marker")
			if err := os.WriteFile(marker, []byte("unchanged"), 0644); err != nil {
				t.Fatal(err)
			}
			link := filepath.Join(root, "configured")
			if err := os.Symlink(target, link); err != nil {
				t.Fatal(err)
			}

			dataDir := filepath.Join(root, "data")
			modelsDir := filepath.Join(root, "models")
			hfHome := ""
			switch field {
			case "data":
				dataDir = link
			case "models":
				modelsDir = link
			case "hf_home":
				hfHome = link
			}
			c := Config{
				Listen: "127.0.0.1:8080", DataDir: dataDir,
				Database: filepath.Join(root, "state.db"), ModelsDir: modelsDir,
				VLLMBinary: "vllm", HFCLI: "hf", HFHome: hfHome,
				Upstream: "http://127.0.0.1:8000", ReadinessTimeout: time.Second,
				ShutdownGrace: time.Second, MaxDownloadWorkers: 1, MaxAuditRecords: 100,
			}
			if err := c.Prepare(); err == nil {
				t.Fatal("managed directory symlink was accepted")
			}
			info, err := os.Stat(target)
			if err != nil {
				t.Fatal(err)
			}
			if info.Mode().Perm() != 0755 {
				t.Fatalf("symlink target permissions changed to %o", info.Mode().Perm())
			}
			contents, err := os.ReadFile(marker)
			if err != nil || string(contents) != "unchanged" {
				t.Fatalf("symlink target marker changed: contents=%q err=%v", contents, err)
			}
		})
	}

	t.Run("regular_file", func(t *testing.T) {
		root := t.TempDir()
		models := filepath.Join(root, "models")
		if err := os.WriteFile(models, []byte("not-a-directory"), 0644); err != nil {
			t.Fatal(err)
		}
		c := Config{
			Listen: "127.0.0.1:8080", DataDir: filepath.Join(root, "data"),
			Database: filepath.Join(root, "state.db"), ModelsDir: models,
			VLLMBinary: "vllm", HFCLI: "hf", Upstream: "http://127.0.0.1:8000",
			ReadinessTimeout: time.Second, ShutdownGrace: time.Second,
			MaxDownloadWorkers: 1, MaxAuditRecords: 100,
		}
		if err := c.Prepare(); err == nil {
			t.Fatal("regular file was accepted as a managed directory")
		}
	})
}

func TestPrepareCreatesAndTightensManagedDirectories(t *testing.T) {
	root := t.TempDir()
	dataDir := filepath.Join(root, "data")
	modelsDir := filepath.Join(root, "models")
	hfHome := filepath.Join(root, "hf-home")
	if err := os.Mkdir(modelsDir, 0755); err != nil {
		t.Fatal(err)
	}
	c := Config{
		Listen: "127.0.0.1:8080", DataDir: dataDir,
		Database: filepath.Join(root, "state.db"), ModelsDir: modelsDir,
		VLLMBinary: "vllm", HFCLI: "hf", HFHome: hfHome,
		Upstream: "http://127.0.0.1:8000", ReadinessTimeout: time.Second,
		ShutdownGrace: time.Second, MaxDownloadWorkers: 1, MaxAuditRecords: 100,
	}
	if err := c.Prepare(); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{dataDir, modelsDir, hfHome} {
		info, err := os.Lstat(path)
		if err != nil {
			t.Fatal(err)
		}
		if !info.IsDir() || info.Mode().Perm() != 0700 {
			t.Fatalf("managed path %q mode=%v, want private directory", path, info.Mode())
		}
	}
}

func TestEnsureAdminTokenCreatesPrivateCredentialAndReusesIt(t *testing.T) {
	dir := t.TempDir()
	c := Config{DataDir: dir}
	path, err := c.EnsureAdminToken()
	if err != nil {
		t.Fatal(err)
	}
	if path != filepath.Join(dir, "admin-bootstrap.token") || len(c.AdminToken) < 32 {
		t.Fatalf("unexpected bootstrap result: path=%q token_length=%d", path, len(c.AdminToken))
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("bootstrap mode = %o, want 600", info.Mode().Perm())
	}

	reloaded := Config{DataDir: dir}
	if _, err = reloaded.EnsureAdminToken(); err != nil {
		t.Fatal(err)
	}
	if reloaded.AdminToken != c.AdminToken {
		t.Fatal("existing bootstrap token was not reused")
	}
}

func TestEnsureAdminTokenHardensExistingCredentialMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "admin-bootstrap.token")
	want := strings.Repeat("a", 40)
	if err := os.WriteFile(path, []byte(want), 0644); err != nil {
		t.Fatal(err)
	}
	c := Config{DataDir: dir}
	if _, err := c.EnsureAdminToken(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.AdminToken != want || info.Mode().Perm() != 0600 {
		t.Fatalf("existing bootstrap token not hardened: token_match=%v mode=%o", c.AdminToken == want, info.Mode().Perm())
	}
}

func TestEnsureAdminTokenRejectsUnsafeCredentialFiles(t *testing.T) {
	t.Run("symlink", func(t *testing.T) {
		dir := t.TempDir()
		target := filepath.Join(t.TempDir(), "target")
		if err := os.WriteFile(target, []byte(strings.Repeat("b", 40)), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, filepath.Join(dir, "admin-bootstrap.token")); err != nil {
			t.Fatal(err)
		}
		if _, err := (&Config{DataDir: dir}).EnsureAdminToken(); err == nil {
			t.Fatal("symlink bootstrap credential accepted")
		}
	})

	for name, token := range map[string]string{
		"trailing_newline": strings.Repeat("c", 40) + "\n",
		"too_short":        "short",
		"too_large":        strings.Repeat("d", 4097),
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "admin-bootstrap.token"), []byte(token), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := (&Config{DataDir: dir}).EnsureAdminToken(); err == nil {
				t.Fatal("unsafe bootstrap credential accepted")
			}
		})
	}
}
