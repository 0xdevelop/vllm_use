package api_gateway

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"
)

type Verifier interface {
	Verify(context.Context, string, string) (Principal, error)
}
type VerifyFunc func(context.Context, string, string) (Principal, error)

func (f VerifyFunc) Verify(c context.Context, k, s string) (Principal, error) { return f(c, k, s) }

// Principal is the non-secret identity attached to an authenticated request.
// It is safe to persist for audit and revocation analysis.
type Principal struct {
	KeyID string
}

type Gateway struct {
	proxy       *httputil.ReverseProxy
	verify      Verifier
	upstreamKey string
	aliases     map[string]string
	record      func(context.Context, RequestMetadata)
	recordSlots chan struct{}
	recordMu    sync.Mutex
	recordCount int
	recordIdle  chan struct{}
}
type RequestMetadata struct {
	RequestID, Method, Path, Model, KeyID, RemoteAddr string
	StatusCode                                        int
	Duration                                          time.Duration
}
type Options struct {
	UpstreamKey string
	Aliases     map[string]string
	Record      func(context.Context, RequestMetadata)
}

const maxJSONBody = 16 << 20

func New(upstream *url.URL, v Verifier) *Gateway {
	return NewWithOptions(upstream, v, Options{})
}
func NewWithOptions(upstream *url.URL, v Verifier, o Options) *Gateway {
	p := httputil.NewSingleHostReverseProxy(upstream)
	p.Transport = &http.Transport{Proxy: http.ProxyFromEnvironment, DialContext: (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext, ForceAttemptHTTP2: true, MaxIdleConns: 100, IdleConnTimeout: 90 * time.Second, TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 30 * time.Second, ExpectContinueTimeout: 1 * time.Second}
	p.FlushInterval = -1
	p.ErrorHandler = func(w http.ResponseWriter, r *http.Request, e error) {
		write(w, http.StatusBadGateway, "upstream unavailable")
	}
	old := p.Director
	p.Director = func(r *http.Request) {
		old(r)
		r.Header.Del("Authorization")
		r.Header.Del("X-API-Key")
		if o.UpstreamKey != "" {
			r.Header.Set("Authorization", "Bearer "+o.UpstreamKey)
		}
	}
	idle := make(chan struct{})
	close(idle)
	return &Gateway{proxy: p, verify: v, upstreamKey: o.UpstreamKey, aliases: o.Aliases, record: o.Record, recordSlots: make(chan struct{}, 64), recordIdle: idle}
}

// WaitRecords waits for audit writes accepted before this call. Call it after
// the HTTP server has stopped accepting requests and before closing the store.
func (g *Gateway) WaitRecords(ctx context.Context) error {
	g.recordMu.Lock()
	idle := g.recordIdle
	g.recordMu.Unlock()
	select {
	case <-idle:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (g *Gateway) beginRecord() {
	g.recordMu.Lock()
	if g.recordCount == 0 {
		g.recordIdle = make(chan struct{})
	}
	g.recordCount++
	g.recordMu.Unlock()
}

func (g *Gateway) finishRecord() {
	g.recordMu.Lock()
	g.recordCount--
	if g.recordCount == 0 {
		close(g.recordIdle)
	}
	g.recordMu.Unlock()
}

func (g *Gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	rid := r.Header.Get("X-Request-ID")
	if rid == "" {
		b := make([]byte, 12)
		if _, e := rand.Read(b); e == nil {
			rid = hex.EncodeToString(b)
		} else {
			rid = "request"
		}
	}
	w.Header().Set("X-Request-ID", rid)
	r.Header.Set("X-Request-ID", rid)
	rw := &statusWriter{ResponseWriter: w, status: 200}
	w = rw
	meta := RequestMetadata{RequestID: rid, Method: r.Method, Path: r.URL.Path, RemoteAddr: r.RemoteAddr}
	defer func() {
		if g.record != nil {
			meta.StatusCode = rw.status
			meta.Duration = time.Since(started)
			select {
			case g.recordSlots <- struct{}{}:
				g.beginRecord()
				go func() {
					defer func() {
						g.finishRecord()
						<-g.recordSlots
					}()
					ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
					defer cancel()
					g.record(ctx, meta)
				}()
			default:
				// Recording is best-effort and must never apply backpressure to inference.
			}
		}
	}()
	responseChild := strings.HasPrefix(r.URL.Path, "/v1/responses/") && len(r.URL.Path) > len("/v1/responses/")
	if r.Method != http.MethodPost && !(r.Method == http.MethodGet && (r.URL.Path == "/v1/models" || responseChild)) && !(r.Method == http.MethodDelete && responseChild) {
		write(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !allowed(r.URL.Path) {
		http.NotFound(w, r)
		return
	}
	token := ""
	h := r.Header.Get("Authorization")
	if strings.HasPrefix(h, "Bearer ") {
		token = strings.TrimPrefix(h, "Bearer ")
	} else if strings.HasPrefix(r.URL.Path, "/v1/messages") {
		token = r.Header.Get("X-API-Key")
	}
	if token == "" {
		write(w, http.StatusUnauthorized, "missing bearer token")
		return
	}
	if g.verify == nil {
		write(w, http.StatusUnauthorized, "authentication unavailable")
		return
	}
	principal, e := g.verify.Verify(r.Context(), token, "inference")
	if e != nil {
		write(w, http.StatusUnauthorized, "invalid bearer token")
		return
	}
	meta.KeyID = principal.KeyID
	if r.Body != nil && strings.Contains(r.Header.Get("Content-Type"), "application/json") {
		body, e := io.ReadAll(io.LimitReader(r.Body, maxJSONBody+1))
		if e == nil {
			r.Body.Close()
			if len(body) > maxJSONBody {
				write(w, http.StatusRequestEntityTooLarge, "JSON body exceeds 16 MiB limit")
				return
			}
			var obj map[string]any
			if json.Unmarshal(body, &obj) == nil {
				if m, ok := obj["model"].(string); ok {
					meta.Model = m
					if actual, exists := g.aliases[m]; exists {
						obj["model"] = actual
						body, _ = json.Marshal(obj)
					}
				}
			}
			r.Body = io.NopCloser(bytes.NewReader(body))
			r.ContentLength = int64(len(body))
		}
	}
	g.proxy.ServeHTTP(w, r)
}

type statusWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (s *statusWriter) Unwrap() http.ResponseWriter { return s.ResponseWriter }

func (s *statusWriter) WriteHeader(n int) {
	if s.wroteHeader {
		return
	}
	s.wroteHeader = true
	s.status = n
	s.ResponseWriter.WriteHeader(n)
}
func (s *statusWriter) Write(p []byte) (int, error) {
	if !s.wroteHeader {
		s.WriteHeader(http.StatusOK)
	}
	return s.ResponseWriter.Write(p)
}
func (s *statusWriter) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
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
