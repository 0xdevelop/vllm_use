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
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/0xdevelop/vllm-use/internal/api"
	"github.com/0xdevelop/vllm-use/internal/auth"
	"github.com/0xdevelop/vllm-use/internal/config"
	"github.com/0xdevelop/vllm-use/internal/download"
	"github.com/0xdevelop/vllm-use/internal/gateway"
	"github.com/0xdevelop/vllm-use/internal/gpu"
	managementmcp "github.com/0xdevelop/vllm-use/internal/mcp"
	"github.com/0xdevelop/vllm-use/internal/models"
	vruntime "github.com/0xdevelop/vllm-use/internal/runtime"
	"github.com/0xdevelop/vllm-use/internal/store"
	webui "github.com/0xdevelop/vllm-use/web"
)

// ParseConfig applies command-line options over environment-populated defaults.
func ParseConfig(args []string, stderr io.Writer) (config.Config, error) {
	c := config.Default()
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
		return config.Config{}, err
	}
	if flags.NArg() != 0 {
		return config.Config{}, fmt.Errorf("unexpected positional arguments: %s", strings.Join(flags.Args(), " "))
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
	st, err := store.Open(c.Database)
	if err != nil {
		slog.Error("open database", "error", err)
		return 1
	}
	defer st.Close()

	supervisor := vruntime.NewSupervisor(c.VLLMBinary, c.ShutdownGrace, c.ReadinessTimeout)
	switcher := vruntime.NewSwitchService(supervisor)
	supervisor.SetHealthInterval(c.HealthInterval)
	keys := auth.New(st)
	downloads := download.NewWithOptions(c.HFCLI, nil, c.MaxDownloadWorkers, 1000)
	downloads.SetStore(st)
	downloads.SetRoot(c.ModelsDir)
	downloads.SetHFHome(c.HFHome)
	registry := models.New(st, c.ModelsDir)
	gpuService := gpu.New(nil)
	mcpHandler := managementmcp.New(managementmcp.Dependencies{Models: registry, Keys: keys, GPU: gpuService, Runtime: supervisor, Switch: switcher, Downloads: downloads}, managementmcp.Options{AllowedOrigins: c.MCPAllowedOrigins})
	management := &api.Server{Models: registry, Keys: keys, GPU: gpuService, Runtime: supervisor, Switch: switcher, Downloads: downloads, Store: st, AdminToken: c.AdminToken, RequireAdmin: true, MCP: mcpHandler, MCPStatus: mcpHandler, Web: webui.Handler()}

	upstream, err := url.Parse(c.Upstream)
	if err != nil || upstream.Scheme == "" || upstream.Host == "" {
		slog.Error("invalid upstream URL")
		return 2
	}
	proxy := gateway.NewWithOptions(upstream, gateway.VerifyFunc(func(ctx context.Context, key, scope string) error {
		_, verifyErr := keys.Verify(ctx, key, scope)
		return verifyErr
	}), gateway.Options{UpstreamKey: c.UpstreamAPIKey, Record: func(ctx context.Context, metadata gateway.RequestMetadata) {
		_ = st.RecordRequest(ctx, store.APIRequest{RequestID: metadata.RequestID, Method: metadata.Method, Path: metadata.Path, Model: metadata.Model, StatusCode: metadata.StatusCode, DurationMS: metadata.Duration.Milliseconds(), RemoteAddr: metadata.RemoteAddr})
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

// ShutdownSignals are the process signals that trigger graceful shutdown.
var ShutdownSignals = []os.Signal{syscall.SIGINT, syscall.SIGTERM}
