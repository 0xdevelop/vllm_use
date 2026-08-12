package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
)

type Verifier interface {
	Verify(context.Context, string, string) error
}
type VerifyFunc func(context.Context, string, string) error

func (f VerifyFunc) Verify(c context.Context, k, s string) error { return f(c, k, s) }

type Gateway struct {
	proxy  *httputil.ReverseProxy
	verify Verifier
}

func New(upstream *url.URL, v Verifier) *Gateway {
	p := httputil.NewSingleHostReverseProxy(upstream)
	p.FlushInterval = -1
	p.ErrorHandler = func(w http.ResponseWriter, r *http.Request, e error) {
		write(w, http.StatusBadGateway, "upstream unavailable")
	}
	old := p.Director
	p.Director = func(r *http.Request) {
		old(r)
		r.Header.Del("Authorization")
		r.Header.Del("X-API-Key")
	}
	return &Gateway{p, v}
}
func (g *Gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost && !(r.Method == http.MethodGet && r.URL.Path == "/v1/models") {
		write(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !allowed(r.URL.Path) {
		http.NotFound(w, r)
		return
	}
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, "Bearer ") {
		write(w, http.StatusUnauthorized, "missing bearer token")
		return
	}
	if g.verify == nil {
		write(w, http.StatusUnauthorized, "authentication unavailable")
		return
	}
	if e := g.verify.Verify(r.Context(), strings.TrimPrefix(h, "Bearer "), "inference"); e != nil {
		write(w, http.StatusUnauthorized, "invalid bearer token")
		return
	}
	g.proxy.ServeHTTP(w, r)
}
func allowed(p string) bool {
	switch p {
	case "/v1/chat/completions", "/v1/completions", "/v1/responses", "/v1/embeddings", "/v1/models", "/v1/messages", "/v1/messages/count_tokens":
		return true
	}
	return strings.HasPrefix(p, "/v1/responses/") && len(p) > len("/v1/responses/")
}
func write(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"message": msg}})
}
