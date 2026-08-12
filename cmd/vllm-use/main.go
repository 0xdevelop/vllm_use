package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
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

func main() {
	c := config.Default()
	flag.StringVar(&c.Listen, "listen", c.Listen, "HTTP listen address")
	flag.StringVar(&c.DataDir, "data-dir", c.DataDir, "private data directory")
	flag.StringVar(&c.Database, "db", c.Database, "SQLite database path")
	flag.StringVar(&c.ModelsDir, "models-dir", c.ModelsDir, "managed models directory")
	flag.StringVar(&c.VLLMBinary, "vllm", c.VLLMBinary, "vLLM executable")
	flag.StringVar(&c.HFCLI, "hf", c.HFCLI, "Hugging Face CLI executable")
	flag.StringVar(&c.Upstream, "upstream", c.Upstream, "vLLM upstream URL")
	flag.StringVar(&c.AdminToken, "admin-token", c.AdminToken, "admin token (required for management API access)")
	var mcpOrigins string
	flag.StringVar(&mcpOrigins, "mcp-allowed-origins", "", "comma-separated additional trusted MCP origins")
	flag.Parse()
	if mcpOrigins != "" {
		c.MCPAllowedOrigins = strings.Split(mcpOrigins, ",")
	}
	if e := c.Prepare(); e != nil {
		slog.Error("invalid configuration", "error", e)
		os.Exit(2)
	}
	bootstrapPath, e := c.EnsureAdminToken()
	if e != nil {
		slog.Error("prepare admin authentication", "error", e)
		os.Exit(2)
	}
	if bootstrapPath != "" {
		slog.Warn("admin authentication bootstrap file created", "path", bootstrapPath)
	}
	st, e := store.Open(c.Database)
	if e != nil {
		slog.Error("open database", "error", e)
		os.Exit(1)
	}
	defer st.Close()
	sup := vruntime.NewSupervisor(c.VLLMBinary, c.ShutdownGrace, c.ReadinessTimeout)
	switcher := vruntime.NewSwitchService(sup)
	sup.SetHealthInterval(c.HealthInterval)
	keys := auth.New(st)
	dl := download.NewWithOptions(c.HFCLI, nil, c.MaxDownloadWorkers, 1000)
	dl.SetStore(st)
	dl.SetRoot(c.ModelsDir)
	registry := models.New(st, c.ModelsDir)
	gpuService := gpu.New(nil)
	mcpHandler := managementmcp.New(managementmcp.Dependencies{Models: registry, Keys: keys, GPU: gpuService, Runtime: sup, Switch: switcher, Downloads: dl}, managementmcp.Options{AllowedOrigins: c.MCPAllowedOrigins})
	app := &api.Server{Models: registry, Keys: keys, GPU: gpuService, Runtime: sup, Switch: switcher, Downloads: dl, Store: st, AdminToken: c.AdminToken, RequireAdmin: true, MCP: mcpHandler, MCPStatus: mcpHandler, Web: webui.Handler()}
	upstream, e := url.Parse(c.Upstream)
	if e != nil || upstream.Scheme == "" || upstream.Host == "" {
		slog.Error("invalid upstream URL")
		os.Exit(2)
	}
	proxy := gateway.NewWithOptions(upstream, gateway.VerifyFunc(func(ctx context.Context, key, scope string) error { _, e := keys.Verify(ctx, key, scope); return e }), gateway.Options{UpstreamKey: c.UpstreamAPIKey, Record: func(ctx context.Context, m gateway.RequestMetadata) {
		_ = st.RecordRequest(ctx, store.APIRequest{RequestID: m.RequestID, Method: m.Method, Path: m.Path, Model: m.Model, StatusCode: m.StatusCode, DurationMS: m.Duration.Milliseconds(), RemoteAddr: m.RemoteAddr})
	}})
	mux := http.NewServeMux()
	mux.Handle("/v1/", proxy)
	mux.Handle("/", app.Handler())
	srv := &http.Server{Addr: c.Listen, Handler: mux, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 0, IdleTimeout: 120 * time.Second, MaxHeaderBytes: 1 << 20}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shut, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = sup.Stop(shut)
		_ = srv.Shutdown(shut)
	}()
	slog.Info("listening", "address", c.Listen)
	if e = srv.ListenAndServe(); e != nil && !errors.Is(e, http.ErrServerClosed) {
		slog.Error("server stopped", "error", e)
		os.Exit(1)
	}
}
