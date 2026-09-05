package ability_settings

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"
)

type Config struct {
	Listen, DataDir, Database, ModelsDir, VLLMBinary, HFCLI, HFHome, Upstream string
	AdminToken                                                                string
	ReadinessTimeout, ShutdownGrace                                           time.Duration
	HealthInterval                                                            time.Duration
	MaxDownloadWorkers                                                        int
	MaxAuditRecords                                                           int
	UpstreamAPIKey                                                            string
	MCPAllowedOrigins                                                         []string
	ModelAliases                                                              map[string]string
	modelAliasesRaw                                                           string
	modelAliasesErr                                                           error
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
	c := Config{
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
		MaxAuditRecords:    envIntInvalid("VLLM_USE_MAX_AUDIT_RECORDS", 10_000, -1),
	}
	_ = c.SetModelAliases(os.Getenv("VLLM_USE_MODEL_ALIASES"))
	return c
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
	return envIntInvalid(key, fallback, 0)
}

func envIntInvalid(key string, fallback, invalid int) int {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return invalid // rejected by Config.Validate
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

// SetModelAliases parses a comma-separated alias=upstream-model mapping. The
// raw value is retained so flag parsing can preserve environment defaults and
// let an explicit CLI value replace an invalid environment value.
func (c *Config) SetModelAliases(raw string) error {
	c.modelAliasesRaw = raw
	c.ModelAliases, c.modelAliasesErr = ParseModelAliases(raw)
	return c.modelAliasesErr
}

func (c Config) ModelAliasesRaw() string { return c.modelAliasesRaw }

func ParseModelAliases(raw string) (map[string]string, error) {
	aliases := map[string]string{}
	if strings.TrimSpace(raw) == "" {
		return aliases, nil
	}
	if len(raw) > 64*1024 {
		return nil, errors.New("model aliases exceed 64 KiB")
	}
	for _, pair := range strings.Split(raw, ",") {
		parts := strings.SplitN(pair, "=", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid model alias %q; expected alias=upstream-model", strings.TrimSpace(pair))
		}
		alias, target := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
		if err := validateModelName(alias); err != nil {
			return nil, fmt.Errorf("invalid model alias %q: %w", alias, err)
		}
		if err := validateModelName(target); err != nil {
			return nil, fmt.Errorf("invalid upstream model for alias %q: %w", alias, err)
		}
		if _, exists := aliases[alias]; exists {
			return nil, fmt.Errorf("duplicate model alias %q", alias)
		}
		aliases[alias] = target
		if len(aliases) > 128 {
			return nil, errors.New("model aliases exceed 128 entries")
		}
	}
	return aliases, nil
}

func validateModelName(value string) error {
	if value == "" {
		return errors.New("name is required")
	}
	if len(value) > 512 {
		return errors.New("name exceeds 512 bytes")
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return errors.New("name contains control characters")
		}
	}
	return nil
}

func (c Config) Validate() error {
	if c.modelAliasesErr != nil {
		return c.modelAliasesErr
	}
	if len(c.ModelAliases) > 128 {
		return errors.New("model aliases exceed 128 entries")
	}
	for alias, target := range c.ModelAliases {
		if err := validateModelName(alias); err != nil {
			return fmt.Errorf("invalid model alias %q: %w", alias, err)
		}
		if err := validateModelName(target); err != nil {
			return fmt.Errorf("invalid upstream model for alias %q: %w", alias, err)
		}
	}
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
	if c.MaxAuditRecords < 0 || c.MaxAuditRecords > 1_000_000 {
		return errors.New("max audit records must be between 0 and 1000000")
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
	for attempts := 0; attempts < 2; attempts++ {
		token, err := readBootstrapToken(p)
		if err == nil {
			c.AdminToken = token
			return p, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return p, err
		}

		b := make([]byte, 32)
		if _, err = rand.Read(b); err != nil {
			return p, err
		}
		token = base64.RawURLEncoding.EncodeToString(b)
		if err = createBootstrapToken(p, token); err == nil {
			c.AdminToken = token
			return p, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return p, err
		}
	}
	return p, errors.New("bootstrap token file changed while being created")
}

func readBootstrapToken(path string) (string, error) {
	pathInfo, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !pathInfo.Mode().IsRegular() {
		return "", errors.New("bootstrap token must be a regular file")
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	openedInfo, err := f.Stat()
	if err != nil {
		return "", err
	}
	if !openedInfo.Mode().IsRegular() || !os.SameFile(pathInfo, openedInfo) {
		return "", errors.New("bootstrap token file changed while being opened")
	}
	if err = f.Chmod(0600); err != nil {
		return "", fmt.Errorf("secure bootstrap token permissions: %w", err)
	}
	b, err := io.ReadAll(io.LimitReader(f, 4097))
	if err != nil {
		return "", fmt.Errorf("read bootstrap token: %w", err)
	}
	currentInfo, err := os.Lstat(path)
	if err != nil || !os.SameFile(openedInfo, currentInfo) {
		return "", errors.New("bootstrap token file changed while being read")
	}
	token := string(b)
	if len(token) < 32 || len(token) > 4096 || strings.TrimSpace(token) != token {
		return "", errors.New("bootstrap token file is invalid")
	}
	for _, r := range token {
		if unicode.IsControl(r) {
			return "", errors.New("bootstrap token file contains control characters")
		}
	}
	return token, nil
}

func createBootstrapToken(path, token string) (err error) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	keep := false
	defer func() {
		if !keep {
			_ = f.Close()
			_ = os.Remove(path)
		}
	}()
	if _, err = io.WriteString(f, token); err != nil {
		return fmt.Errorf("write bootstrap token: %w", err)
	}
	if err = f.Sync(); err != nil {
		return fmt.Errorf("sync bootstrap token: %w", err)
	}
	if err = f.Close(); err != nil {
		return fmt.Errorf("close bootstrap token: %w", err)
	}
	keep = true
	return nil
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
		if err := secureManagedDirectory(d); err != nil {
			return fmt.Errorf("prepare managed directory %q: %w", d, err)
		}
	}
	return nil
}

// secureManagedDirectory rejects a configured final path that is a symlink or
// another non-directory object. Permissions are changed through an opened
// descriptor after identity checks, so rejection never chmods a symlink target.
// As with database opening, a downstream pathname user still leaves a narrow
// replacement window when the parent is writable by an attacker.
func secureManagedDirectory(path string) error {
	pathInfo, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		if err = os.MkdirAll(path, 0700); err != nil {
			return fmt.Errorf("create: %w", err)
		}
		pathInfo, err = os.Lstat(path)
	}
	if err != nil {
		return fmt.Errorf("inspect: %w", err)
	}
	if !pathInfo.IsDir() || pathInfo.Mode()&os.ModeSymlink != 0 {
		return errors.New("path must be a directory and must not be a symlink")
	}

	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open securely: %w", err)
	}
	defer f.Close()
	openedInfo, err := f.Stat()
	if err != nil {
		return fmt.Errorf("inspect opened directory: %w", err)
	}
	if !openedInfo.IsDir() || !os.SameFile(pathInfo, openedInfo) {
		return errors.New("directory changed while being opened")
	}
	if err = f.Chmod(0700); err != nil {
		return fmt.Errorf("secure permissions: %w", err)
	}
	currentInfo, err := os.Lstat(path)
	if err != nil || !currentInfo.IsDir() || !os.SameFile(openedInfo, currentInfo) {
		return errors.New("directory changed while being secured")
	}
	return nil
}
