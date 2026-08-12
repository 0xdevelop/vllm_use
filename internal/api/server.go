package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/0xdevelop/vllm-use/internal/auth"
	"github.com/0xdevelop/vllm-use/internal/gpu"
	"github.com/0xdevelop/vllm-use/internal/models"
	vruntime "github.com/0xdevelop/vllm-use/internal/runtime"
)

type Server struct {
	Models       *models.Registry
	Keys         *auth.Manager
	GPU          *gpu.NVIDIA
	Runtime      *vruntime.Supervisor
	AdminToken   string
	RequireAdmin bool
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})
	mux.Handle("/api/", s.admin(http.HandlerFunc(s.api)))
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
func (s *Server) admin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.RequireAdmin {
			h := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			if s.AdminToken == "" || h != s.AdminToken {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
func (s *Server) api(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.Method == "GET" && r.URL.Path == "/api/models":
		v, e := s.Models.List(r.Context())
		respond(w, v, e)
	case r.Method == "POST" && r.URL.Path == "/api/models/huggingface":
		var in struct {
			Repository string `json:"repository"`
		}
		if !decode(w, r, &in) {
			return
		}
		v, e := s.Models.AddHuggingFace(r.Context(), in.Repository)
		respond(w, v, e)
	case r.Method == "POST" && r.URL.Path == "/api/models/local":
		var in struct {
			Path string `json:"path"`
		}
		if !decode(w, r, &in) {
			return
		}
		v, e := s.Models.AddLocal(r.Context(), in.Path)
		respond(w, v, e)
	case r.Method == "DELETE" && strings.HasPrefix(r.URL.Path, "/api/models/"):
		e := s.Models.Delete(r.Context(), strings.TrimPrefix(r.URL.Path, "/api/models/"), r.URL.Query().Get("files") == "true")
		respond(w, map[string]bool{"deleted": e == nil}, e)
	case r.Method == "POST" && r.URL.Path == "/api/keys":
		var in struct {
			Scopes []string `json:"scopes"`
		}
		if !decode(w, r, &in) {
			return
		}
		k, secret, e := s.Keys.Create(r.Context(), in.Scopes)
		respond(w, map[string]any{"key": k, "secret": secret}, e)
	case r.Method == "GET" && r.URL.Path == "/api/gpus":
		v, e := s.GPU.List(r.Context())
		respond(w, v, e)
	case r.Method == "GET" && r.URL.Path == "/api/runtime":
		respond(w, s.Runtime.State(), nil)
	case r.Method == "POST" && (r.URL.Path == "/api/runtime/start" || r.URL.Path == "/api/runtime/restart"):
		var in struct {
			Options   vruntime.Options `json:"options"`
			HealthURL string           `json:"health_url"`
		}
		if !decode(w, r, &in) {
			return
		}
		var e error
		if strings.HasSuffix(r.URL.Path, "/restart") {
			e = s.Runtime.Restart(r.Context(), in.Options, in.HealthURL)
		} else {
			e = s.Runtime.Start(r.Context(), in.Options, in.HealthURL)
		}
		respond(w, s.Runtime.State(), e)
	case r.Method == "POST" && r.URL.Path == "/api/runtime/stop":
		respond(w, map[string]bool{"stopped": true}, s.Runtime.Stop(r.Context()))
	default:
		http.NotFound(w, r)
	}
}
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	d.DisallowUnknownFields()
	if e := d.Decode(v); e != nil {
		http.Error(w, "invalid JSON", 400)
		return false
	}
	return true
}
func respond(w http.ResponseWriter, v any, e error) {
	if e != nil {
		http.Error(w, e.Error(), 400)
		return
	}
	json.NewEncoder(w).Encode(v)
}
