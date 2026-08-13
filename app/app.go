// Package app owns the vllm-use composition root and process lifecycle.
package app

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/0xdevelop/vllm-use/ability"
	"github.com/0xdevelop/vllm-use/ability/ability_api_key"
	"github.com/0xdevelop/vllm-use/ability/ability_download"
	"github.com/0xdevelop/vllm-use/ability/ability_gpu"
	"github.com/0xdevelop/vllm-use/ability/ability_model"
	"github.com/0xdevelop/vllm-use/ability/ability_runtime"
	"github.com/0xdevelop/vllm-use/ability/ability_settings"
	"github.com/0xdevelop/vllm-use/api/api_gateway"
	"github.com/0xdevelop/vllm-use/api/api_http"
	"github.com/0xdevelop/vllm-use/api/api_mcp"
	"github.com/0xdevelop/vllm-use/db/sqlite"
	webui "github.com/0xdevelop/vllm-use/web"
)

// ParseConfig applies command-line options over environment-populated defaults.
func ParseConfig(args []string, stderr io.Writer) (ability_settings.Config, error) {
	c := ability_settings.Default()
	flags := flag.NewFlagSet("vllm-use", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&c.Listen, "listen", c.Listen, "HTTP listen address")
	flags.StringVar(&c.DataDir, "data-dir", c.DataDir, "private data directory")
	flags.StringVar(&c.Database, "db", c.Database, "SQLite database path")
	flags.StringVar(&c.ModelsDir, "models-dir", c.ModelsDir, "managed models directory")
	flags.StringVar(&c.VLLMBinary, "vllm", c.VLLMBinary, "vLLM executable")
	flags.StringVar(&c.HFCLI, "hf", c.HFCLI, "Hugging Face CLI executable")
	flags.StringVar(&c.HFHome, "hf-home", c.HFHome, "Hugging Face cache/config directory (preserves inherited HF_HOME when unset)")
	flags.IntVar(&c.MaxDownloadWorkers, "max-download-workers", c.MaxDownloadWorkers, "maximum concurrent Hugging Face downloads")
	flags.StringVar(&c.Upstream, "upstream", c.Upstream, "vLLM upstream URL")
	flags.StringVar(&c.AdminToken, "admin-token", c.AdminToken, "admin token (required for management API access)")
	var mcpOrigins string
	flags.StringVar(&mcpOrigins, "mcp-allowed-origins", "", "comma-separated additional trusted MCP origins")
	if err := flags.Parse(args); err != nil {
		return ability_settings.Config{}, err
	}
	if flags.NArg() != 0 {
		return ability_settings.Config{}, fmt.Errorf("unexpected positional arguments: %s", strings.Join(flags.Args(), " "))
	}
	if mcpOrigins != "" {
		c.MCPAllowedOrigins = splitList(mcpOrigins)
	}
	return c, nil
}

func splitList(value string) []string {
	var values []string
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			values = append(values, item)
		}
	}
	return values
}

// Run builds and serves the application until the context is cancelled.
func Run(ctx context.Context, args []string, stderr io.Writer) int {
	c, err := ParseConfig(args, stderr)
	if err != nil {
		return 2
	}
	if err = c.Prepare(); err != nil {
		slog.Error("invalid configuration", "error", err)
		return 2
	}
	bootstrapPath, err := c.EnsureAdminToken()
	if err != nil {
		slog.Error("prepare admin authentication", "error", err)
		return 2
	}
	if bootstrapPath != "" {
		slog.Warn("admin authentication bootstrap file created", "path", bootstrapPath)
	}
	st, err := sqlite.Open(c.Database)
	if err != nil {
		slog.Error("open database", "error", err)
		return 1
	}
	defer st.Close()

	supervisor := ability_runtime.NewSupervisor(c.VLLMBinary, c.ShutdownGrace, c.ReadinessTimeout)
	switcher := ability_runtime.NewSwitchService(supervisor)
	supervisor.SetHealthInterval(c.HealthInterval)
	keys := ability_api_key.New(st)
	downloads := ability_download.NewWithOptions(c.HFCLI, nil, c.MaxDownloadWorkers, 1000)
	downloads.SetStore(st)
	downloads.SetRoot(c.ModelsDir)
	downloads.SetHFHome(c.HFHome)
	registry := ability_model.New(st, c.ModelsDir)
	gpuService := ability_gpu.New(nil)
	ability_model.Setup(registry)
	ability_gpu.Setup(gpuService)
	ability.LoadAbilityAPIMethods()
	management := &api_http.Server{Models: registry, Keys: keys, GPU: gpuService, Runtime: supervisor, Switch: switcher, Downloads: downloads, Store: st, AdminToken: c.AdminToken, RequireAdmin: true, MCP: api_mcp.Handler(), Web: webui.Handler()}

	upstream, err := url.Parse(c.Upstream)
	if err != nil || upstream.Scheme == "" || upstream.Host == "" {
		slog.Error("invalid upstream URL")
		return 2
	}
	proxy := api_gateway.NewWithOptions(upstream, api_gateway.VerifyFunc(func(ctx context.Context, key, scope string) error {
		_, verifyErr := keys.Verify(ctx, key, scope)
		return verifyErr
	}), api_gateway.Options{UpstreamKey: c.UpstreamAPIKey, Record: func(ctx context.Context, metadata api_gateway.RequestMetadata) {
		_ = st.RecordRequest(ctx, sqlite.APIRequest{RequestID: metadata.RequestID, Method: metadata.Method, Path: metadata.Path, Model: metadata.Model, StatusCode: metadata.StatusCode, DurationMS: metadata.Duration.Milliseconds(), RemoteAddr: metadata.RemoteAddr})
	}})
	mux := http.NewServeMux()
	mux.Handle("/v1/", proxy)
	mux.Handle("/", management.Handler())
	httpServer := &http.Server{Addr: c.Listen, Handler: mux, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 0, IdleTimeout: 120 * time.Second, MaxHeaderBytes: 1 << 20}

	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = supervisor.Stop(shutdownCtx)
		_ = httpServer.Shutdown(shutdownCtx)
	}()

	slog.Info("listening", "address", c.Listen)
	if err = httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		slog.Error("server stopped", "error", err)
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = supervisor.Stop(shutdownCtx)
		_ = httpServer.Shutdown(shutdownCtx)
		return 1
	}
	if ctx.Err() != nil {
		<-shutdownDone
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_ = supervisor.Stop(shutdownCtx)
	return 0
}
