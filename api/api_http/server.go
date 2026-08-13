package api_http

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/0xdevelop/vllm-use/ability"
	"github.com/0xdevelop/vllm-use/ability/ability_api_key"
	"github.com/0xdevelop/vllm-use/ability/ability_download"
	"github.com/0xdevelop/vllm-use/ability/ability_gpu"
	"github.com/0xdevelop/vllm-use/ability/ability_model"
	"github.com/0xdevelop/vllm-use/ability/ability_runtime"
	"github.com/0xdevelop/vllm-use/ability/ability_settings"
	"github.com/0xdevelop/vllm-use/api/api_executer"
	"github.com/0xdevelop/vllm-use/db/sqlite"
)

type Server struct {
	Keys         *ability_api_key.Manager
	AdminToken   string
	RequireAdmin bool
	MCP          http.Handler
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
		next.ServeHTTP(w, r.WithContext(api_executer.WithScopes(r.Context(), key.Scopes)))
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
			next.ServeHTTP(w, r.WithContext(api_executer.WithAdmin(r.Context())))
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
		next.ServeHTTP(w, r.WithContext(api_executer.WithAdmin(r.Context())))
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
		execute(w, r, ability_model.MethodList, map[string]interface{}{})
	case r.Method == "POST" && p == "/api/models/scan":
		execute(w, r, ability_model.MethodScan, map[string]interface{}{})
	case r.Method == "GET" && strings.HasPrefix(p, "/api/models/"):
		execute(w, r, ability_model.MethodGet, map[string]interface{}{"id": strings.TrimPrefix(p, "/api/models/")})
	case r.Method == "POST" && p == "/api/models/huggingface":
		var in struct {
			Repository string `json:"repository"`
			Revision   string `json:"revision"`
		}
		if !decode(w, r, &in) {
			return
		}
		execute(w, r, ability_model.MethodRegisterHF, map[string]interface{}{"repository": in.Repository, "revision": in.Revision})
	case r.Method == "POST" && p == "/api/models/local":
		var in struct {
			Name string `json:"name"`
			Path string `json:"path"`
		}
		if !decode(w, r, &in) {
			return
		}
		execute(w, r, ability_model.MethodRegisterLocal, map[string]interface{}{"name": in.Name, "path": in.Path})
	case r.Method == "DELETE" && strings.HasPrefix(p, "/api/models/"):
		execute(w, r, ability_model.MethodDelete, map[string]interface{}{"id": strings.TrimPrefix(p, "/api/models/"), "files": r.URL.Query().Get("files") == "true"})
	case r.Method == "POST" && p == "/api/keys":
		var in struct {
			Name   string   `json:"name"`
			Scopes []string `json:"scopes"`
		}
		if !decode(w, r, &in) {
			return
		}
		execute(w, r, ability_api_key.MethodCreate, map[string]interface{}{"name": in.Name, "scopes": in.Scopes})
	case r.Method == "GET" && p == "/api/keys":
		execute(w, r, ability_api_key.MethodList, map[string]interface{}{})
	case r.Method == "POST" && strings.HasSuffix(p, "/enable") && strings.HasPrefix(p, "/api/keys/"):
		id := strings.TrimSuffix(strings.TrimPrefix(p, "/api/keys/"), "/enable")
		execute(w, r, ability_api_key.MethodEnable, map[string]interface{}{"id": id})
	case r.Method == "POST" && strings.HasSuffix(p, "/disable") && strings.HasPrefix(p, "/api/keys/"):
		id := strings.TrimSuffix(strings.TrimPrefix(p, "/api/keys/"), "/disable")
		execute(w, r, ability_api_key.MethodDisable, map[string]interface{}{"id": id})
	case r.Method == "DELETE" && strings.HasPrefix(p, "/api/keys/"):
		execute(w, r, ability_api_key.MethodDelete, map[string]interface{}{"id": strings.TrimPrefix(p, "/api/keys/")})
	case r.Method == "GET" && p == "/api/downloads":
		execute(w, r, ability_download.MethodList, map[string]interface{}{})
	case r.Method == "POST" && p == "/api/downloads":
		var in ability_download.Request
		if !decode(w, r, &in) {
			return
		}
		execute(w, r, ability_download.MethodStart, toArguments(in))
	case strings.HasPrefix(p, "/api/downloads/"):
		s.downloadAPI(w, r)
	case r.Method == "GET" && p == "/api/gpus":
		execute(w, r, ability_gpu.MethodList, map[string]interface{}{})
	case r.Method == "GET" && p == "/api/system":
		execute(w, r, ability.MethodSystem, map[string]interface{}{})
	case r.Method == "GET" && p == "/api/mcp":
		execute(w, r, ability.MethodMCPStatus, map[string]interface{}{})
	case r.Method == "GET" && (p == "/api/runtime" || p == "/api/runtime/status" || p == "/api/runtime/logs"):
		execute(w, r, ability_runtime.MethodStatus, map[string]interface{}{})
	case r.Method == "POST" && (p == "/api/runtime/start" || p == "/api/runtime/restart"):
		var in struct {
			Options   ability_runtime.Options `json:"options"`
			HealthURL string                  `json:"health_url,omitempty"`
		}
		if !decode(w, r, &in) {
			return
		}
		method := ability_runtime.MethodStart
		if p == "/api/runtime/restart" {
			method = ability_runtime.MethodRestart
		}
		execute(w, r, method, map[string]interface{}{"options": in.Options, "health_url": in.HealthURL})
	case r.Method == "POST" && p == "/api/runtime/switch":
		var in struct {
			ModelID   string                  `json:"model_id"`
			Options   ability_runtime.Options `json:"options"`
			HealthURL string                  `json:"health_url,omitempty"`
		}
		if !decode(w, r, &in) {
			return
		}
		execute(w, r, ability_runtime.MethodSwitch, map[string]interface{}{"model_id": in.ModelID, "options": in.Options, "health_url": in.HealthURL})
	case r.Method == "POST" && p == "/api/runtime/stop":
		execute(w, r, ability_runtime.MethodStop, map[string]interface{}{})
	case r.Method == "GET" && p == "/api/settings":
		execute(w, r, ability_settings.MethodList, map[string]interface{}{})
	case r.Method == "PUT" && p == "/api/settings":
		var in []sqlite.Setting
		if !decode(w, r, &in) {
			return
		}
		execute(w, r, ability_settings.MethodUpdate, map[string]interface{}{"settings": in})
	case r.Method == "GET" && p == "/api/requests/recent":
		n, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		execute(w, r, ability_settings.MethodRecentRequests, map[string]interface{}{"limit": n})
	case r.Method == "GET" && p == "/api/dashboard":
		execute(w, r, ability.MethodDashboard, map[string]interface{}{})
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
		execute(w, r, ability_download.MethodStatus, map[string]interface{}{"id": id})
	case r.Method == "GET" && action == "logs":
		execute(w, r, ability_download.MethodLogs, map[string]interface{}{"id": id})
	case r.Method == "POST" && action == "cancel":
		execute(w, r, ability_download.MethodCancel, map[string]interface{}{"id": id})
	case r.Method == "POST" && action == "retry":
		var in struct {
			Token string `json:"token"`
		}
		if !decode(w, r, &in) {
			return
		}
		execute(w, r, ability_download.MethodRetry, map[string]interface{}{"id": id, "token": in.Token})
	default:
		writeError(w, http.StatusNotFound, "not found")
	}
}

func execute(w http.ResponseWriter, r *http.Request, method string, arguments map[string]interface{}) {
	value, err := api_executer.ExecuteAbility(r.Context(), method, arguments)
	respond(w, value, err)
}

func toArguments(value interface{}) map[string]interface{} {
	payload, err := json.Marshal(value)
	if err != nil {
		return map[string]interface{}{}
	}
	arguments := map[string]interface{}{}
	if json.Unmarshal(payload, &arguments) != nil {
		return map[string]interface{}{}
	}
	return arguments
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
