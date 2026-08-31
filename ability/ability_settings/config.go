package ability_settings

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Listen, DataDir, Database, ModelsDir, VLLMBinary, HFCLI, HFHome, Upstream string
	AdminToken                                                                string
	ReadinessTimeout, ShutdownGrace                                           time.Duration
	HealthInterval                                                            time.Duration
	MaxDownloadWorkers                                                        int
	UpstreamAPIKey                                                            string
	MCPAllowedOrigins                                                         []string
}

func Default() Config {
	d, _ := os.UserConfigDir()
	d = filepath.Join(d, "vllm-use")
	d = envOr("VLLM_USE_DATA_DIR", d)
	database := firstEnv("VLLM_USE_DATABASE", "VLLM_USE_DB")
	if database == "" {
		database = filepath.Join(d, "vllm-use.db")
	}
	models := envOr("VLLM_USE_MODELS_DIR", filepath.Join(d, "models"))
	return Config{
		Listen:             envOr("VLLM_USE_LISTEN", "127.0.0.1:8080"),
		DataDir:            d,
		Database:           database,
		ModelsDir:          models,
		VLLMBinary:         envOr("VLLM_USE_VLLM_BINARY", "vllm"),
		HFCLI:              envOr("VLLM_USE_HF_CLI", "hf"),
		HFHome:             os.Getenv("VLLM_USE_HF_HOME"),
		Upstream:           envOr("VLLM_USE_UPSTREAM", "http://127.0.0.1:8000"),
		AdminToken:         os.Getenv("VLLM_USE_ADMIN_TOKEN"),
		UpstreamAPIKey:     os.Getenv("VLLM_USE_UPSTREAM_API_KEY"),
		MCPAllowedOrigins:  splitList(os.Getenv("VLLM_USE_MCP_ALLOWED_ORIGINS")),
		ReadinessTimeout:   envDuration("VLLM_USE_READINESS_TIMEOUT", 2*time.Minute, 0),
		ShutdownGrace:      envDuration("VLLM_USE_SHUTDOWN_GRACE", 10*time.Second, 0),
		HealthInterval:     envDuration("VLLM_USE_HEALTH_INTERVAL", 200*time.Millisecond, -1),
		MaxDownloadWorkers: envInt("VLLM_USE_MAX_DOWNLOAD_WORKERS", 2),
	}
}

func envOr(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok && value != "" {
		return value
	}
	return fallback
}

func firstEnv(keys ...string) string {
	for _, key := range keys {
		if value := os.Getenv(key); value != "" {
			return value
		}
	}
	return ""
}

func envInt(key string, fallback int) int {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0 // rejected by Config.Validate
	}
	return value
}

func envDuration(key string, fallback, invalid time.Duration) time.Duration {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback
	}
	value, err := time.ParseDuration(raw)
	if err != nil {
		return invalid // rejected by Config.Validate
	}
	return value
}

func splitList(s string) []string {
	var out []string
	for _, v := range strings.Split(s, ",") {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}

func (c Config) Validate() error {
	if c.DataDir == "" || c.Database == "" || c.ModelsDir == "" {
		return errors.New("data, database, and models paths are required")
	}
	if !filepath.IsAbs(c.DataDir) || !filepath.IsAbs(c.Database) || !filepath.IsAbs(c.ModelsDir) {
		return errors.New("data paths must be absolute")
	}
	if c.VLLMBinary == "" || c.HFCLI == "" {
		return errors.New("vllm and hf executables are required")
	}
	u, err := url.Parse(c.Upstream)
	if err != nil || u.Scheme == "" || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return errors.New("upstream must be an absolute HTTP(S) URL")
	}
	if u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("upstream must be an origin URL without credentials, path, query, or fragment")
	}
	upstreamHost := u.Hostname()
	upstreamIP := net.ParseIP(upstreamHost)
	if !strings.EqualFold(upstreamHost, "localhost") && (upstreamIP == nil || !upstreamIP.IsLoopback()) {
		return errors.New("upstream host must be loopback for the managed host vLLM process")
	}
	if c.ReadinessTimeout <= 0 || c.ShutdownGrace <= 0 || c.HealthInterval < 0 {
		return errors.New("timeouts must be positive")
	}
	if c.MaxDownloadWorkers < 1 || c.MaxDownloadWorkers > 64 {
		return errors.New("max download workers must be between 1 and 64")
	}
	if c.HFHome != "" && !filepath.IsAbs(c.HFHome) {
		return errors.New("HF home must be absolute")
	}
	crossOriginProtection := http.NewCrossOriginProtection()
	for _, origin := range c.MCPAllowedOrigins {
		if err := crossOriginProtection.AddTrustedOrigin(origin); err != nil {
			return errors.New("invalid MCP trusted origin: " + err.Error())
		}
	}
	host, _, err := net.SplitHostPort(c.Listen)
	if err != nil {
		return err
	}
	if net.ParseIP(host) == nil && host != "localhost" {
		return errors.New("listen host must be an IP address or localhost")
	}
	return nil
}

// EnsureAdminToken creates a private bootstrap credential when none was
// configured. The secret is never logged; an operator can read the returned
// path and exchange/use it locally before replacing it with a scoped key.
func (c *Config) EnsureAdminToken() (string, error) {
	if c.AdminToken != "" {
		return "", nil
	}
	p := filepath.Join(c.DataDir, "admin-bootstrap.token")
	if b, e := os.ReadFile(p); e == nil {
		c.AdminToken = string(b)
		if len(c.AdminToken) < 32 {
			return p, errors.New("bootstrap token file is invalid")
		}
		return p, nil
	}
	b := make([]byte, 32)
	if _, e := rand.Read(b); e != nil {
		return p, e
	}
	c.AdminToken = base64.RawURLEncoding.EncodeToString(b)
	if e := os.WriteFile(p, []byte(c.AdminToken), 0600); e != nil {
		return p, e
	}
	return p, os.Chmod(p, 0600)
}

func (c Config) IsLoopback() bool {
	h, _, _ := net.SplitHostPort(c.Listen)
	return h == "localhost" || (net.ParseIP(h) != nil && net.ParseIP(h).IsLoopback())
}

func (c Config) Prepare() error {
	if err := c.Validate(); err != nil {
		return err
	}
	directories := []string{c.DataDir, c.ModelsDir}
	if c.HFHome != "" {
		directories = append(directories, c.HFHome)
	}
	for _, d := range directories {
		if err := os.MkdirAll(d, 0700); err != nil {
			return err
		}
		if err := os.Chmod(d, 0700); err != nil {
			return err
		}
	}
	return nil
}
