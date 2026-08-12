// Package mcp exposes the manager's management services over MCP.
package mcp

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/0xdevelop/vllm-use/internal/auth"
	"github.com/0xdevelop/vllm-use/internal/download"
	"github.com/0xdevelop/vllm-use/internal/gpu"
	"github.com/0xdevelop/vllm-use/internal/models"
	vruntime "github.com/0xdevelop/vllm-use/internal/runtime"
	"github.com/0xdevelop/vllm-use/internal/store"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const ProtocolVersion = "2026-07-28"

type TokenVerifier interface {
	Verify(context.Context, string, string) (*auth.Key, error)
}

type Dependencies struct {
	Models    *models.Registry
	Keys      TokenVerifier
	GPU       *gpu.NVIDIA
	Runtime   *vruntime.Supervisor
	Switch    *vruntime.SwitchService
	Downloads *download.Downloader
}

type Options struct {
	AllowedOrigins []string
	JSONResponse   bool
}

type RequestMetadata struct {
	At         time.Time `json:"at"`
	Method     string    `json:"method,omitempty"`
	Name       string    `json:"name,omitempty"`
	KeyID      string    `json:"key_id,omitempty"`
	RemoteAddr string    `json:"remote_addr,omitempty"`
	StatusCode int       `json:"status_code"`
	DurationMS int64     `json:"duration_ms"`
}

type status struct {
	ProtocolVersion string            `json:"protocol_version"`
	Transport       string            `json:"transport"`
	Stateless       bool              `json:"stateless"`
	Recent          []RequestMetadata `json:"recent_requests"`
}

type Handler struct {
	next    http.Handler
	origins map[string]struct{}
	keys    TokenVerifier
	mu      sync.Mutex
	recent  []RequestMetadata
}

type principalKey struct{}

func New(d Dependencies, opts Options) *Handler {
	server := sdk.NewServer(&sdk.Implementation{Name: "vllm-use", Version: "phase-1"}, nil)
	registerTools(server, d)
	transport := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return server }, &sdk.StreamableHTTPOptions{
		Stateless:                    true,
		JSONResponse:                 opts.JSONResponse,
		PropagateRequestCancellation: true,
		MaxRequestBodyBytes:          1 << 20,
	})
	h := &Handler{next: transport, keys: d.Keys, origins: make(map[string]struct{})}
	for _, raw := range opts.AllowedOrigins {
		if origin, ok := canonicalOrigin(raw); ok {
			h.origins[origin] = struct{}{}
		}
	}
	return h
}

func (h *Handler) Status() any {
	h.mu.Lock()
	defer h.mu.Unlock()
	recent := append([]RequestMetadata(nil), h.recent...)
	return status{ProtocolVersion: ProtocolVersion, Transport: "streamable-http-post", Stateless: true, Recent: recent}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	rw := &responseWriter{ResponseWriter: w, status: http.StatusOK}
	meta := RequestMetadata{At: start.UTC(), Method: r.Header.Get("Mcp-Method"), Name: r.Header.Get("Mcp-Name"), RemoteAddr: r.RemoteAddr}
	defer func() {
		meta.StatusCode = rw.status
		meta.DurationMS = time.Since(start).Milliseconds()
		h.record(meta)
	}()
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(rw, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if r.Header.Get("Mcp-Session-Id") != "" {
		http.Error(rw, "Mcp-Session-Id is not supported", http.StatusBadRequest)
		return
	}
	if r.Header.Get("Mcp-Protocol-Version") != ProtocolVersion {
		http.Error(rw, "unsupported or missing MCP protocol version", http.StatusBadRequest)
		return
	}
	if !h.validOrigin(r) {
		http.Error(rw, "forbidden origin or host", http.StatusForbidden)
		return
	}
	token, ok := bearer(r.Header.Get("Authorization"))
	if !ok || h.keys == nil {
		bearerUnauthorized(rw)
		return
	}
	key, err := h.keys.Verify(r.Context(), token, "")
	if err != nil || !hasMCPScope(key.Scopes) {
		bearerUnauthorized(rw)
		return
	}
	meta.KeyID = key.ID
	ctx := context.WithValue(r.Context(), principalKey{}, key)
	h.next.ServeHTTP(rw, r.WithContext(ctx))
}

func (h *Handler) record(v RequestMetadata) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.recent = append([]RequestMetadata{v}, h.recent...)
	if len(h.recent) > 50 {
		h.recent = h.recent[:50]
	}
}

func (h *Handler) validOrigin(r *http.Request) bool {
	host := canonicalHost(r.Host)
	if host == "" {
		return false
	}
	hostAllowed := isLoopbackHost(host)
	for origin := range h.origins {
		u, _ := url.Parse(origin)
		if canonicalHost(u.Host) == host {
			hostAllowed = true
			break
		}
	}
	if !hostAllowed {
		return false
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	canonical, ok := canonicalOrigin(origin)
	if !ok {
		return false
	}
	if _, ok := h.origins[canonical]; ok {
		return true
	}
	u, _ := url.Parse(canonical)
	return isLoopbackHost(canonicalHost(u.Host))
}

func canonicalOrigin(raw string) (string, bool) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return "", false
	}
	host := canonicalHost(u.Host)
	if host == "" {
		return "", false
	}
	return strings.ToLower(u.Scheme) + "://" + host, true
}

func canonicalHost(raw string) string {
	raw = strings.TrimSpace(strings.ToLower(raw))
	if raw == "" {
		return ""
	}
	if host, port, err := net.SplitHostPort(raw); err == nil {
		if net.ParseIP(strings.Trim(host, "[]")) == nil && !validDNSName(host) {
			return ""
		}
		return net.JoinHostPort(strings.Trim(host, "[]"), port)
	}
	host := strings.Trim(raw, "[]")
	if net.ParseIP(host) == nil && !validDNSName(host) {
		return ""
	}
	return host
}

func validDNSName(s string) bool {
	s = strings.TrimSuffix(strings.Trim(s, "[]"), ".")
	if s == "" || len(s) > 253 {
		return false
	}
	for _, label := range strings.Split(s, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, c := range label {
			if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
				return false
			}
		}
	}
	return true
}

func isLoopbackHost(hostport string) bool {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	host = strings.Trim(strings.TrimSuffix(host, "."), "[]")
	return host == "localhost" || strings.HasSuffix(host, ".localhost") || (net.ParseIP(host) != nil && net.ParseIP(host).IsLoopback())
}

func bearer(raw string) (string, bool) {
	parts := strings.Fields(raw)
	returnValue := ""
	if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") && parts[1] != "" {
		returnValue = parts[1]
		return returnValue, true
	}
	return "", false
}

func bearerUnauthorized(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Bearer realm="mcp"`)
	http.Error(w, "unauthorized", http.StatusUnauthorized)
}

func hasMCPScope(scopes []string) bool {
	for _, s := range scopes {
		if s == "mcp.read" || s == "mcp.runtime" || s == "mcp.models" || s == "mcp.admin" {
			return true
		}
	}
	return false
}

func authorized(ctx context.Context, need string) error {
	k, _ := ctx.Value(principalKey{}).(*auth.Key)
	if k == nil {
		return errors.New("unauthorized")
	}
	for _, scope := range k.Scopes {
		if scope == need || scope == "mcp.admin" {
			return nil
		}
	}
	return errors.New("insufficient scope")
}

type responseWriter struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (w *responseWriter) WriteHeader(status int) {
	if w.wrote {
		return
	}
	w.wrote = true
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *responseWriter) Write(p []byte) (int, error) {
	if !w.wrote {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(p)
}

func (w *responseWriter) Flush() {
	if !w.wrote {
		w.WriteHeader(http.StatusOK)
	}
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *responseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

type empty struct{}

func boolptr(v bool) *bool { return &v }

func annotations(readOnly, destructive, idempotent, openWorld bool) *sdk.ToolAnnotations {
	return &sdk.ToolAnnotations{ReadOnlyHint: readOnly, DestructiveHint: boolptr(destructive), IdempotentHint: idempotent, OpenWorldHint: boolptr(openWorld)}
}

func toolError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, store.ErrNotFound) || strings.Contains(strings.ToLower(err.Error()), "not found") {
		return errors.New("not found")
	}
	msg := strings.ToLower(err.Error())
	for _, safe := range []string{"invalid ", "required", "already active", "already running", "maximum concurrent", "has no managed files", "outside configured root"} {
		if strings.Contains(msg, safe) {
			return errors.New(msg)
		}
	}
	return errors.New("management operation failed")
}

type publicModel struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	Kind       string    `json:"kind"`
	Repository string    `json:"repository,omitempty"`
	Revision   string    `json:"revision,omitempty"`
	Status     string    `json:"status"`
	SizeBytes  int64     `json:"size_bytes"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

func modelOutput(m models.Model) publicModel {
	return publicModel{ID: m.ID, Name: m.Name, Kind: m.Kind, Repository: m.Repository, Revision: m.Revision, Status: m.Status, SizeBytes: m.SizeBytes, CreatedAt: m.CreatedAt, UpdatedAt: m.UpdatedAt}
}

type publicJob struct {
	ID         string         `json:"id"`
	ModelID    string         `json:"model_id,omitempty"`
	Repository string         `json:"repository"`
	Revision   string         `json:"revision,omitempty"`
	State      download.State `json:"state"`
	Progress   float64        `json:"progress"`
	StartedAt  *time.Time     `json:"started_at,omitempty"`
	FinishedAt *time.Time     `json:"finished_at,omitempty"`
}

type publicRuntime struct {
	Status    vruntime.Status `json:"status"`
	PID       int             `json:"pid,omitempty"`
	StartedAt *time.Time      `json:"started_at,omitempty"`
	ReadyAt   *time.Time      `json:"ready_at,omitempty"`
	StoppedAt *time.Time      `json:"stopped_at,omitempty"`
	ExitCode  *int            `json:"exit_code,omitempty"`
}

func runtimeOutput(v vruntime.State) publicRuntime {
	return publicRuntime{Status: v.Status, PID: v.PID, StartedAt: v.StartedAt, ReadyAt: v.ReadyAt, StoppedAt: v.StoppedAt, ExitCode: v.ExitCode}
}

func jobOutput(j download.Job) publicJob {
	return publicJob{ID: j.ID, ModelID: j.ModelID, Repository: j.Repo, Revision: j.Revision, State: j.State, Progress: j.Progress, StartedAt: j.StartedAt, FinishedAt: j.FinishedAt}
}

func registerTools(s *sdk.Server, d Dependencies) {
	read := func(name, description string, handler sdk.ToolHandlerFor[empty, any]) {
		sdk.AddTool(s, &sdk.Tool{Name: name, Description: description, Annotations: annotations(true, false, true, false)}, handler)
	}
	read("models.list", "List registered models without exposing local filesystem paths.", func(ctx context.Context, _ *sdk.CallToolRequest, _ empty) (*sdk.CallToolResult, any, error) {
		if err := authorized(ctx, "mcp.read"); err != nil {
			return nil, nil, err
		}
		items, err := d.Models.List(ctx)
		out := make([]publicModel, 0, len(items))
		for _, item := range items {
			out = append(out, modelOutput(item))
		}
		return nil, out, toolError(err)
	})
	type modelID struct {
		ID string `json:"id" jsonschema:"model registry identifier"`
	}
	sdk.AddTool(s, &sdk.Tool{Name: "models.get", Description: "Get one registered model.", Annotations: annotations(true, false, true, false)}, func(ctx context.Context, _ *sdk.CallToolRequest, in modelID) (*sdk.CallToolResult, any, error) {
		if err := authorized(ctx, "mcp.read"); err != nil {
			return nil, nil, err
		}
		v, err := d.Models.Get(ctx, in.ID)
		return nil, modelOutput(v), toolError(err)
	})
	type registerInput struct {
		Kind       string `json:"kind"`
		Name       string `json:"name,omitempty"`
		Repository string `json:"repository,omitempty"`
		Revision   string `json:"revision,omitempty"`
		Path       string `json:"path,omitempty"`
	}
	sdk.AddTool(s, &sdk.Tool{Name: "models.register", Description: "Register a Hugging Face model or an existing local model inside the managed models root.", Annotations: annotations(false, false, false, true)}, func(ctx context.Context, _ *sdk.CallToolRequest, in registerInput) (*sdk.CallToolResult, any, error) {
		if err := authorized(ctx, "mcp.models"); err != nil {
			return nil, nil, err
		}
		var v models.Model
		var err error
		switch in.Kind {
		case "huggingface":
			v, err = d.Models.RegisterHuggingFace(ctx, in.Repository, in.Revision)
		case "local":
			v, err = d.Models.RegisterLocal(ctx, in.Name, in.Path)
		default:
			err = errors.New("invalid kind")
		}
		return nil, modelOutput(v), toolError(err)
	})
	type downloadInput struct {
		ID          string `json:"id"`
		ModelID     string `json:"model_id,omitempty"`
		Repository  string `json:"repository"`
		Revision    string `json:"revision,omitempty"`
		Destination string `json:"destination"`
		Token       string `json:"token,omitempty"`
	}
	sdk.AddTool(s, &sdk.Tool{Name: "models.download", Description: "Start a Hugging Face model download into the managed models root.", Annotations: annotations(false, false, false, true)}, func(ctx context.Context, _ *sdk.CallToolRequest, in downloadInput) (*sdk.CallToolResult, any, error) {
		if err := authorized(ctx, "mcp.models"); err != nil {
			return nil, nil, err
		}
		j, err := d.Downloads.DownloadRequest(context.WithoutCancel(ctx), download.Request{ID: in.ID, ModelID: in.ModelID, Repository: in.Repository, Revision: in.Revision, Destination: in.Destination, Token: in.Token})
		if j == nil {
			return nil, nil, toolError(err)
		}
		return nil, jobOutput(*j), toolError(err)
	})
	type cancelInput struct {
		ID string `json:"id"`
	}
	sdk.AddTool(s, &sdk.Tool{Name: "models.download_cancel", Description: "Cancel a model download.", Annotations: annotations(false, true, true, true)}, func(ctx context.Context, _ *sdk.CallToolRequest, in cancelInput) (*sdk.CallToolResult, any, error) {
		if err := authorized(ctx, "mcp.models"); err != nil {
			return nil, nil, err
		}
		err := d.Downloads.Cancel(in.ID)
		return nil, map[string]bool{"canceled": err == nil}, toolError(err)
	})
	type deleteInput struct {
		ID             string `json:"id"`
		ConfirmModelID string `json:"confirm_model_id"`
		DeleteFiles    bool   `json:"delete_files"`
	}
	sdk.AddTool(s, &sdk.Tool{Name: "models.delete", Description: "Delete a model registration and optionally its managed files. confirm_model_id must exactly equal id.", Annotations: annotations(false, true, true, false)}, func(ctx context.Context, _ *sdk.CallToolRequest, in deleteInput) (*sdk.CallToolResult, any, error) {
		if err := authorized(ctx, "mcp.models"); err != nil {
			return nil, nil, err
		}
		if in.ID == "" || in.ConfirmModelID != in.ID {
			return nil, nil, errors.New("confirm_model_id must exactly match id")
		}
		if d.Switch != nil && d.Switch.Active() == in.ID {
			return nil, nil, errors.New("refusing to delete the running model")
		}
		err := d.Models.Delete(ctx, in.ID, in.DeleteFiles)
		return nil, map[string]bool{"deleted": err == nil}, toolError(err)
	})
	read("runtime.status", "Return vLLM runtime state.", func(ctx context.Context, _ *sdk.CallToolRequest, _ empty) (*sdk.CallToolResult, any, error) {
		if err := authorized(ctx, "mcp.read"); err != nil {
			return nil, nil, err
		}
		return nil, runtimeOutput(d.Runtime.State()), nil
	})
	type runtimeInput struct {
		Options vruntime.Options `json:"options"`
	}
	for _, spec := range []struct {
		name string
		fn   func(context.Context, vruntime.Options, string) error
	}{{"runtime.start", d.Runtime.Start}, {"runtime.restart", d.Runtime.Restart}} {
		sp := spec
		sdk.AddTool(s, &sdk.Tool{Name: sp.name, Description: "Change vLLM runtime state.", Annotations: annotations(false, true, false, false)}, func(ctx context.Context, _ *sdk.CallToolRequest, in runtimeInput) (*sdk.CallToolResult, any, error) {
			if err := authorized(ctx, "mcp.runtime"); err != nil {
				return nil, nil, err
			}
			err := sp.fn(ctx, in.Options, "")
			return nil, runtimeOutput(d.Runtime.State()), toolError(err)
		})
	}
	type switchInput struct {
		ModelID string           `json:"model_id"`
		Options vruntime.Options `json:"options"`
	}
	sdk.AddTool(s, &sdk.Tool{Name: "runtime.switch", Description: "Stop the active model and start a different model configuration.", Annotations: annotations(false, true, false, false)}, func(ctx context.Context, _ *sdk.CallToolRequest, in switchInput) (*sdk.CallToolResult, any, error) {
		if err := authorized(ctx, "mcp.runtime"); err != nil {
			return nil, nil, err
		}
		if d.Switch == nil {
			return nil, nil, errors.New("runtime switching unavailable")
		}
		if strings.TrimSpace(in.ModelID) == "" {
			return nil, nil, errors.New("model_id is required")
		}
		err := d.Switch.Switch(ctx, in.ModelID, in.Options, "")
		return nil, runtimeOutput(d.Runtime.State()), toolError(err)
	})
	sdk.AddTool(s, &sdk.Tool{Name: "runtime.stop", Description: "Stop the vLLM runtime.", Annotations: annotations(false, true, true, false)}, func(ctx context.Context, _ *sdk.CallToolRequest, _ empty) (*sdk.CallToolResult, any, error) {
		if err := authorized(ctx, "mcp.runtime"); err != nil {
			return nil, nil, err
		}
		err := d.Runtime.Stop(ctx)
		return nil, map[string]bool{"stopped": err == nil}, toolError(err)
	})
	read("gpu.list", "List detected NVIDIA GPUs.", func(ctx context.Context, _ *sdk.CallToolRequest, _ empty) (*sdk.CallToolResult, any, error) {
		if err := authorized(ctx, "mcp.read"); err != nil {
			return nil, nil, err
		}
		v, err := d.GPU.List(ctx)
		return nil, v, toolError(err)
	})
	read("gpu.status", "Return current NVIDIA GPU status.", func(ctx context.Context, _ *sdk.CallToolRequest, _ empty) (*sdk.CallToolResult, any, error) {
		if err := authorized(ctx, "mcp.read"); err != nil {
			return nil, nil, err
		}
		v, err := d.GPU.List(ctx)
		return nil, v, toolError(err)
	})
	read("downloads.list", "List model download jobs.", func(ctx context.Context, _ *sdk.CallToolRequest, _ empty) (*sdk.CallToolResult, any, error) {
		if err := authorized(ctx, "mcp.read"); err != nil {
			return nil, nil, err
		}
		jobs := d.Downloads.List()
		out := make([]publicJob, 0, len(jobs))
		for _, j := range jobs {
			out = append(out, jobOutput(j))
		}
		return nil, out, nil
	})
	type jobID struct {
		ID string `json:"id"`
	}
	sdk.AddTool(s, &sdk.Tool{Name: "downloads.status", Description: "Return one model download job.", Annotations: annotations(true, false, true, false)}, func(ctx context.Context, _ *sdk.CallToolRequest, in jobID) (*sdk.CallToolResult, any, error) {
		if err := authorized(ctx, "mcp.read"); err != nil {
			return nil, nil, err
		}
		j, ok := d.Downloads.Status(in.ID)
		if !ok {
			return nil, nil, errors.New("not found")
		}
		return nil, jobOutput(j), nil
	})
	read("system.status", "Return manager process and platform status.", func(ctx context.Context, _ *sdk.CallToolRequest, _ empty) (*sdk.CallToolResult, any, error) {
		if err := authorized(ctx, "mcp.read"); err != nil {
			return nil, nil, err
		}
		return nil, map[string]any{"go_version": runtime.Version(), "goos": runtime.GOOS, "goarch": runtime.GOARCH, "cpus": runtime.NumCPU()}, nil
	})
}
