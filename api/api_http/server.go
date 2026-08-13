package api_http

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"runtime"
	"strconv"
	"strings"

	"github.com/0xdevelop/vllm-use/ability/ability_api_key"
	"github.com/0xdevelop/vllm-use/ability/ability_download"
	"github.com/0xdevelop/vllm-use/ability/ability_gpu"
	"github.com/0xdevelop/vllm-use/ability/ability_model"
	"github.com/0xdevelop/vllm-use/ability/ability_runtime"
	"github.com/0xdevelop/vllm-use/api/api_executer"
	"github.com/0xdevelop/vllm-use/db/sqlite"
)

type Server struct {
	Models       *ability_model.Registry
	Keys         *ability_api_key.Manager
	GPU          *ability_gpu.NVIDIA
	Runtime      *ability_runtime.Supervisor
	Switch       *ability_runtime.SwitchService
	Downloads    *ability_download.Downloader
	Store        *sqlite.Store
	AdminToken   string
	RequireAdmin bool
	MCP          http.Handler
	MCPStatus    interface{ Status() any }
	Web          http.Handler
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { respond(w, map[string]string{"status": "ok"}, nil) })
	if s.MCP != nil {
		mux.Handle("/mcp", s.mcp(s.MCP))
	}
	mux.Handle("/api/", s.admin(http.HandlerFunc(s.api)))
	if s.Web != nil {
		mux.Handle("/", s.Web)
	}
	return security(mux)
}
func security(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) mcp(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		raw := r.Header.Get("Authorization")
		if !strings.HasPrefix(raw, "Bearer ") || s.Keys == nil {
			unauthorized(w)
			return
		}
		key, err := s.Keys.Verify(r.Context(), strings.TrimPrefix(raw, "Bearer "), "")
		if err != nil || !hasMCPScope(key.Scopes) {
			unauthorized(w)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func hasMCPScope(scopes []string) bool {
	for _, scope := range scopes {
		switch scope {
		case "mcp.read", "mcp.runtime", "mcp.models", "mcp.admin":
			return true
		}
	}
	return false
}
func (s *Server) admin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw := r.Header.Get("Authorization")
		if !strings.HasPrefix(raw, "Bearer ") {
			unauthorized(w)
			return
		}
		token := strings.TrimPrefix(raw, "Bearer ")
		if s.AdminToken != "" && len(token) == len(s.AdminToken) && subtle.ConstantTimeCompare([]byte(token), []byte(s.AdminToken)) == 1 {
			next.ServeHTTP(w, r)
			return
		}
		scope := "admin.read"
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			scope = "admin.write"
		}
		if s.Keys == nil {
			unauthorized(w)
			return
		}
		if _, e := s.Keys.Verify(r.Context(), token, scope); e != nil {
			unauthorized(w)
			return
		}
		next.ServeHTTP(w, r)
	})
}
func unauthorized(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Bearer realm="admin"`)
	writeError(w, http.StatusUnauthorized, "unauthorized")
}
func (s *Server) api(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Path
	switch {
	case r.Method == "GET" && p == "/api/models":
		v, e := api_executer.ExecuteAbility(r.Context(), ability_model.MethodList, map[string]interface{}{})
		respond(w, v, e)
	case r.Method == "POST" && p == "/api/models/scan":
		v, e := s.Models.Scan(r.Context())
		respond(w, v, e)
	case r.Method == "GET" && strings.HasPrefix(p, "/api/models/"):
		v, e := s.Models.Get(r.Context(), strings.TrimPrefix(p, "/api/models/"))
		respond(w, v, e)
	case r.Method == "POST" && p == "/api/models/huggingface":
		var in struct {
			Repository string `json:"repository"`
			Revision   string `json:"revision"`
		}
		if !decode(w, r, &in) {
			return
		}
		v, e := s.Models.RegisterHuggingFace(r.Context(), in.Repository, in.Revision)
		respond(w, v, e)
	case r.Method == "POST" && p == "/api/models/local":
		var in struct {
			Name string `json:"name"`
			Path string `json:"path"`
		}
		if !decode(w, r, &in) {
			return
		}
		v, e := s.Models.RegisterLocal(r.Context(), in.Name, in.Path)
		respond(w, v, e)
	case r.Method == "DELETE" && strings.HasPrefix(p, "/api/models/"):
		id := strings.TrimPrefix(p, "/api/models/")
		if s.Switch != nil && s.Switch.Active() == id {
			respond(w, nil, errors.New("refusing to delete the running model"))
			return
		}
		e := s.Models.Delete(r.Context(), id, r.URL.Query().Get("files") == "true")
		respond(w, map[string]bool{"deleted": e == nil}, e)
	case r.Method == "POST" && p == "/api/keys":
		var in struct {
			Name   string   `json:"name"`
			Scopes []string `json:"scopes"`
		}
		if !decode(w, r, &in) {
			return
		}
		k, secret, e := s.Keys.CreateNamed(r.Context(), in.Name, in.Scopes)
		respond(w, map[string]any{"key": k, "secret": secret}, e)
	case r.Method == "GET" && p == "/api/keys":
		v, e := s.Keys.List(r.Context())
		respond(w, v, e)
	case r.Method == "POST" && strings.HasSuffix(p, "/enable") && strings.HasPrefix(p, "/api/keys/"):
		id := strings.TrimSuffix(strings.TrimPrefix(p, "/api/keys/"), "/enable")
		e := s.Keys.SetEnabled(r.Context(), id, true)
		respond(w, map[string]bool{"enabled": e == nil}, e)
	case r.Method == "POST" && strings.HasSuffix(p, "/disable") && strings.HasPrefix(p, "/api/keys/"):
		id := strings.TrimSuffix(strings.TrimPrefix(p, "/api/keys/"), "/disable")
		e := s.Keys.SetEnabled(r.Context(), id, false)
		respond(w, map[string]bool{"disabled": e == nil}, e)
	case r.Method == "DELETE" && strings.HasPrefix(p, "/api/keys/"):
		respond(w, map[string]bool{"deleted": true}, s.Keys.Delete(r.Context(), strings.TrimPrefix(p, "/api/keys/")))
	case r.Method == "GET" && p == "/api/downloads":
		respond(w, s.Downloads.List(), nil)
	case r.Method == "POST" && p == "/api/downloads":
		var in ability_download.Request
		if !decode(w, r, &in) {
			return
		}
		v, e := s.Downloads.DownloadRequest(context.WithoutCancel(r.Context()), in)
		respond(w, v, e)
	case strings.HasPrefix(p, "/api/downloads/"):
		s.downloadAPI(w, r)
	case r.Method == "GET" && p == "/api/gpus":
		v, e := api_executer.ExecuteAbility(r.Context(), ability_gpu.MethodList, map[string]interface{}{})
		respond(w, v, e)
	case r.Method == "GET" && p == "/api/system":
		respond(w, map[string]any{"go_version": runtime.Version(), "goos": runtime.GOOS, "goarch": runtime.GOARCH, "cpus": runtime.NumCPU()}, nil)
	case r.Method == "GET" && p == "/api/mcp":
		if s.MCPStatus == nil {
			respond(w, nil, sqlite.ErrNotFound)
			return
		}
		respond(w, s.MCPStatus.Status(), nil)
	case r.Method == "GET" && (p == "/api/runtime" || p == "/api/runtime/status" || p == "/api/runtime/logs"):
		respond(w, s.Runtime.State(), nil)
	case r.Method == "POST" && (p == "/api/runtime/start" || p == "/api/runtime/restart"):
		var in struct {
			Options   ability_runtime.Options `json:"options"`
			HealthURL string                  `json:"health_url,omitempty"`
		}
		if !decode(w, r, &in) {
			return
		}
		var e error
		if p == "/api/runtime/start" {
			e = s.Runtime.Start(r.Context(), in.Options, in.HealthURL)
		} else {
			e = s.Runtime.Restart(r.Context(), in.Options, in.HealthURL)
		}
		respond(w, s.Runtime.State(), e)
	case r.Method == "POST" && p == "/api/runtime/switch":
		var in struct {
			ModelID   string                  `json:"model_id"`
			Options   ability_runtime.Options `json:"options"`
			HealthURL string                  `json:"health_url,omitempty"`
		}
		if !decode(w, r, &in) {
			return
		}
		if s.Switch == nil {
			respond(w, nil, errors.New("runtime switching unavailable"))
			return
		}
		if strings.TrimSpace(in.ModelID) == "" {
			respond(w, nil, errors.New("model_id is required"))
			return
		}
		e := s.Switch.Switch(r.Context(), in.ModelID, in.Options, in.HealthURL)
		respond(w, s.Runtime.State(), e)
	case r.Method == "POST" && p == "/api/runtime/stop":
		e := s.Runtime.Stop(r.Context())
		respond(w, map[string]bool{"stopped": e == nil}, e)
	case r.Method == "GET" && p == "/api/settings":
		v, e := s.Store.Settings(r.Context())
		respond(w, v, e)
	case r.Method == "PUT" && p == "/api/settings":
		var in []sqlite.Setting
		if !decode(w, r, &in) {
			return
		}
		respond(w, map[string]bool{"updated": true}, s.Store.PutSettings(r.Context(), in))
	case r.Method == "GET" && p == "/api/requests/recent":
		n, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		v, e := s.Store.RecentRequests(r.Context(), n)
		respond(w, v, e)
	case r.Method == "GET" && p == "/api/dashboard":
		mods, e := s.Models.List(r.Context())
		if e != nil {
			respond(w, nil, e)
			return
		}
		reqs, e := s.Store.RecentRequests(r.Context(), 10)
		respond(w, map[string]any{"models": len(mods), "runtime": s.Runtime.State(), "downloads": s.Downloads.List(), "recent_requests": reqs}, e)
	default:
		writeError(w, http.StatusNotFound, "not found")
	}
}
func (s *Server) downloadAPI(w http.ResponseWriter, r *http.Request) {
	tail := strings.TrimPrefix(r.URL.Path, "/api/downloads/")
	parts := strings.Split(tail, "/")
	id := parts[0]
	action := ""
	if len(parts) > 1 {
		action = parts[1]
	}
	switch {
	case r.Method == "GET" && action == "":
		v, ok := s.Downloads.Status(id)
		if !ok {
			respond(w, nil, sqlite.ErrNotFound)
			return
		}
		respond(w, v, nil)
	case r.Method == "GET" && action == "logs":
		v, e := s.Downloads.Logs(id)
		respond(w, v, e)
	case r.Method == "POST" && action == "cancel":
		respond(w, map[string]bool{"canceled": true}, s.Downloads.Cancel(id))
	case r.Method == "POST" && action == "retry":
		var in struct {
			Token string `json:"token"`
		}
		if !decode(w, r, &in) {
			return
		}
		v, e := s.Downloads.Retry(r.Context(), id, in.Token)
		respond(w, v, e)
	default:
		writeError(w, http.StatusNotFound, "not found")
	}
}
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	d.DisallowUnknownFields()
	if e := d.Decode(v); e != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return false
	}
	if d.Decode(&struct{}{}) == nil {
		writeError(w, http.StatusBadRequest, "multiple JSON values")
		return false
	}
	return true
}

func writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}

func respond(w http.ResponseWriter, v any, e error) {
	w.Header().Set("Content-Type", "application/json")
	if e != nil {
		status := http.StatusBadRequest
		if errors.Is(e, sqlite.ErrNotFound) {
			status = http.StatusNotFound
		}
		writeError(w, status, e.Error())
		return
	}
	_ = json.NewEncoder(w).Encode(v)
}
