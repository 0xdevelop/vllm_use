package api_gateway

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"
)

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestRoutingAuthStreamingAndErrors(t *testing.T) {
	u, _ := url.Parse("http://127.0.0.1:8000")
	g := New(u, VerifyFunc(func(_ context.Context, k, s string) (Principal, error) {
		if k != "ok" || s != "inference" {
			return Principal{}, errors.New("bad")
		}
		return Principal{KeyID: "key-routing"}, nil
	}))
	g.proxy.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Authorization") != "" {
			t.Error("auth forwarded")
		}
		return &http.Response{StatusCode: 200, Status: "200 OK", Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("data: one\n\ndata: two\n\n")), ContentLength: -1, Request: r}, nil
	})
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer ok")
	rr := httptest.NewRecorder()
	g.ServeHTTP(rr, req)
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), "data: two") {
		t.Fatalf("%d %s", rr.Code, rr.Body.String())
	}
	rr = httptest.NewRecorder()
	g.ServeHTTP(rr, httptest.NewRequest("POST", "/v1/nope", nil))
	if rr.Code != 404 {
		t.Fatalf("route %d", rr.Code)
	}
	g.proxy.Transport = roundTrip(func(*http.Request) (*http.Response, error) { return nil, errors.New("down") })
	rr = httptest.NewRecorder()
	req = httptest.NewRequest("POST", "/v1/completions", nil)
	req.Header.Set("Authorization", "Bearer ok")
	g.ServeHTTP(rr, req)
	if rr.Code != 502 {
		t.Fatalf("upstream %d", rr.Code)
	}
}

func TestResponsesSuffixAnthropicAliasAndRecording(t *testing.T) {
	u, _ := url.Parse("http://127.0.0.1:8000")
	var mu sync.Mutex
	var records []RequestMetadata
	g := NewWithOptions(u, VerifyFunc(func(_ context.Context, key, scope string) (Principal, error) {
		if key != "ok" || scope != "inference" {
			return Principal{}, errors.New("bad auth")
		}
		return Principal{KeyID: "key-audit"}, nil
	}), Options{Aliases: map[string]string{"friendly": "actual"}, Record: func(_ context.Context, m RequestMetadata) {
		mu.Lock()
		records = append(records, m)
		mu.Unlock()
	}})
	g.proxy.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
		var body []byte
		if r.Body != nil {
			body, _ = io.ReadAll(r.Body)
		}
		if r.URL.Path == "/v1/messages" && string(body) != `{"model":"actual"}` {
			t.Errorf("alias body = %s", body)
		}
		if r.Header.Get("Authorization") != "" || r.Header.Get("X-API-Key") != "" {
			t.Error("client credentials forwarded")
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{}`)), Request: r}, nil
	})
	for _, tc := range []struct{ method, path, header string }{
		{http.MethodPost, "/v1/messages", "x-api-key"},
		{http.MethodGet, "/v1/responses/resp_1", "authorization"},
		{http.MethodDelete, "/v1/responses/resp_1", "authorization"},
		{http.MethodPost, "/v1/responses/resp_1/cancel", "authorization"},
	} {
		body := ""
		if tc.path == "/v1/messages" {
			body = `{"model":"friendly"}`
		}
		req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if tc.header == "x-api-key" {
			req.Header.Set("X-API-Key", "ok")
		} else {
			req.Header.Set("Authorization", "Bearer ok")
		}
		w := httptest.NewRecorder()
		g.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("%s %s: %d %s", tc.method, tc.path, w.Code, w.Body.String())
		}
	}
	bad := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	g.ServeHTTP(httptest.NewRecorder(), bad)
	deadline := time.Now().Add(time.Second)
	for {
		mu.Lock()
		n := len(records)
		mu.Unlock()
		if n == 5 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for records: %d", n)
		}
		time.Sleep(time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	var modelSeen, unauthorized bool
	for _, record := range records {
		modelSeen = modelSeen || record.Model == "friendly"
		if record.StatusCode == http.StatusUnauthorized {
			unauthorized = true
			if record.KeyID != "" {
				t.Fatalf("unauthorized request attributed to %q", record.KeyID)
			}
		} else if record.KeyID != "key-audit" {
			t.Fatalf("authenticated request missing attribution: %#v", record)
		}
	}
	if len(records) != 5 || !modelSeen || !unauthorized {
		t.Fatalf("records %#v", records)
	}
}

func TestClientControlledAuditMetadataIsBoundedWithoutChangingInferenceBody(t *testing.T) {
	u, _ := url.Parse("http://127.0.0.1:8000")
	recorded := make(chan RequestMetadata, 1)
	g := NewWithOptions(u, VerifyFunc(func(context.Context, string, string) (Principal, error) {
		return Principal{KeyID: "key-bounded"}, nil
	}), Options{Record: func(_ context.Context, metadata RequestMetadata) {
		recorded <- metadata
	}})

	model := strings.Repeat("模", 200)
	body := `{"model":` + strconv.Quote(model) + `,"prompt":"unchanged"}`
	forwarded := ""
	g.proxy.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
		got, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		forwarded = string(got)
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{}`)), Request: r}, nil
	})

	path := "/v1/responses/" + strings.Repeat("p", maxAuditPathBytes)
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.RemoteAddr = strings.Repeat("a", maxAuditRemoteAddrBytes+1)
	req.Header.Set("Authorization", "Bearer ok")
	req.Header.Set("X-Request-ID", strings.Repeat("r", 1024))
	w := httptest.NewRecorder()
	g.ServeHTTP(w, req)

	if w.Code != http.StatusOK || forwarded != body {
		t.Fatalf("status=%d forwarded body changed=%v", w.Code, forwarded != body)
	}
	requestID := w.Header().Get("X-Request-ID")
	if requestID == "" || len(requestID) > maxAuditRequestIDBytes || requestID == strings.Repeat("r", 1024) {
		t.Fatalf("unsafe response request id %q", requestID)
	}
	select {
	case metadata := <-recorded:
		if metadata.RequestID != requestID {
			t.Fatalf("recorded request id %q != response id %q", metadata.RequestID, requestID)
		}
		if len(metadata.Model) > maxAuditModelBytes || !utf8.ValidString(metadata.Model) {
			t.Fatalf("unsafe recorded model: bytes=%d valid_utf8=%v", len(metadata.Model), utf8.ValidString(metadata.Model))
		}
		if len(metadata.Path) != maxAuditPathBytes || len(metadata.RemoteAddr) != maxAuditRemoteAddrBytes {
			t.Fatalf("audit bounds path=%d remote_addr=%d", len(metadata.Path), len(metadata.RemoteAddr))
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for audit metadata")
	}
}

func TestAuditRequestIDAcceptsOnlyBoundedVisibleASCII(t *testing.T) {
	valid := "req_01HZX-abc.def:123"
	if got := auditRequestID(valid); got != valid {
		t.Fatalf("valid request id changed to %q", got)
	}
	for _, invalid := range []string{"", "contains space", "中文", strings.Repeat("x", maxAuditRequestIDBytes+1)} {
		got := auditRequestID(invalid)
		if got == "" || got == invalid || len(got) > maxAuditRequestIDBytes {
			t.Fatalf("invalid request id %q produced %q", invalid, got)
		}
	}
}

func TestOversizeBodyRejectedWithoutForwardingRegardlessOfContentType(t *testing.T) {
	u, _ := url.Parse("http://127.0.0.1:8000")
	g := New(u, VerifyFunc(func(context.Context, string, string) (Principal, error) { return Principal{}, nil }))
	for _, contentType := range []string{"application/json", "text/plain", ""} {
		t.Run(contentType, func(t *testing.T) {
			forwarded := false
			g.proxy.Transport = roundTrip(func(r *http.Request) (*http.Response, error) { forwarded = true; return nil, errors.New("unexpected") })
			req := httptest.NewRequest(http.MethodPost, "/v1/completions", io.LimitReader(strings.NewReader(strings.Repeat("x", maxJSONBody+2)), maxJSONBody+1))
			req.Header.Set("Authorization", "Bearer ok")
			if contentType != "" {
				req.Header.Set("Content-Type", contentType)
			}
			w := httptest.NewRecorder()
			g.ServeHTTP(w, req)
			if w.Code != http.StatusRequestEntityTooLarge || forwarded {
				t.Fatalf("status=%d forwarded=%v", w.Code, forwarded)
			}
		})
	}
}

func TestRequestBodyReadFailureRejectedWithoutForwarding(t *testing.T) {
	u, _ := url.Parse("http://127.0.0.1:8000")
	g := New(u, VerifyFunc(func(context.Context, string, string) (Principal, error) { return Principal{}, nil }))
	forwarded := false
	g.proxy.Transport = roundTrip(func(r *http.Request) (*http.Response, error) { forwarded = true; return nil, errors.New("unexpected") })
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	req.Body = &failingBody{}
	req.Header.Set("Authorization", "Bearer ok")
	w := httptest.NewRecorder()
	g.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest || forwarded {
		t.Fatalf("status=%d forwarded=%v body=%s", w.Code, forwarded, w.Body.String())
	}
}

type failingBody struct{}

func (*failingBody) Read([]byte) (int, error) { return 0, errors.New("broken request stream") }
func (*failingBody) Close() error             { return nil }

func TestAliasRewriteDoesNotTrustContentType(t *testing.T) {
	u, _ := url.Parse("http://127.0.0.1:8000")
	g := NewWithOptions(u, VerifyFunc(func(context.Context, string, string) (Principal, error) {
		return Principal{}, nil
	}), Options{Aliases: map[string]string{"friendly": "actual"}})
	forwarded := ""
	g.proxy.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		forwarded = string(body)
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{}`)), Request: r}, nil
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"friendly"}`))
	req.Header.Set("Authorization", "Bearer ok")
	w := httptest.NewRecorder()
	g.ServeHTTP(w, req)
	if w.Code != http.StatusOK || !strings.Contains(forwarded, `"model":"actual"`) {
		t.Fatalf("status=%d forwarded=%s", w.Code, forwarded)
	}
}

func TestAliasRewritePreservesJSONNumbers(t *testing.T) {
	u, _ := url.Parse("http://127.0.0.1:8000")
	g := NewWithOptions(u, VerifyFunc(func(context.Context, string, string) (Principal, error) {
		return Principal{}, nil
	}), Options{Aliases: map[string]string{"friendly": "actual"}})
	forwarded := ""
	g.proxy.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		forwarded = string(body)
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{}`)), Request: r}, nil
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"friendly","seed":9007199254740993,"nested":{"id":18446744073709551615}}`))
	req.Header.Set("Authorization", "Bearer ok")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	g.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(forwarded, `"model":"actual"`) || !strings.Contains(forwarded, `"seed":9007199254740993`) || !strings.Contains(forwarded, `"id":18446744073709551615`) {
		t.Fatalf("alias rewrite changed numeric values: %s", forwarded)
	}
}

func TestStatusWriterKeepsFirstStatus(t *testing.T) {
	w := httptest.NewRecorder()
	s := &statusWriter{ResponseWriter: w, status: http.StatusOK}
	s.WriteHeader(http.StatusCreated)
	s.WriteHeader(http.StatusInternalServerError)
	if s.status != http.StatusCreated || w.Code != http.StatusCreated {
		t.Fatalf("status=%d underlying=%d", s.status, w.Code)
	}
}

func TestRecorderCannotBlockRequest(t *testing.T) {
	u, _ := url.Parse("http://127.0.0.1:8000")
	started := make(chan struct{})
	g := NewWithOptions(u, VerifyFunc(func(context.Context, string, string) (Principal, error) { return Principal{}, nil }), Options{Record: func(ctx context.Context, _ RequestMetadata) {
		close(started)
		<-ctx.Done()
	}})
	g.proxy.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{}`)), Request: r}, nil
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/completions", nil)
	req.Header.Set("Authorization", "Bearer ok")
	done := make(chan struct{})
	go func() { g.ServeHTTP(httptest.NewRecorder(), req); close(done) }()
	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("request blocked on recorder")
	}
	select {
	case <-started:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("recorder was not invoked")
	}
}

func TestWaitRecordsDrainsAcceptedAuditWrites(t *testing.T) {
	u, _ := url.Parse("http://127.0.0.1:8000")
	started := make(chan struct{})
	release := make(chan struct{})
	g := NewWithOptions(u, VerifyFunc(func(context.Context, string, string) (Principal, error) { return Principal{}, nil }), Options{Record: func(context.Context, RequestMetadata) {
		close(started)
		<-release
	}})
	g.proxy.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{}`)), Request: r}, nil
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/completions", nil)
	req.Header.Set("Authorization", "Bearer ok")
	g.ServeHTTP(httptest.NewRecorder(), req)
	<-started

	timeout, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := g.WaitRecords(timeout); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("WaitRecords before release = %v", err)
	}
	close(release)
	ctx, cancelWait := context.WithTimeout(context.Background(), time.Second)
	defer cancelWait()
	if err := g.WaitRecords(ctx); err != nil {
		t.Fatalf("WaitRecords after release = %v", err)
	}
}

func TestSSEIsFlushedBeforeCompletion(t *testing.T) {
	u, _ := url.Parse("http://upstream.invalid")
	g := New(u, VerifyFunc(func(context.Context, string, string) (Principal, error) { return Principal{}, nil }))
	g.proxy.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
		reader, writer := io.Pipe()
		go func() {
			_, _ = io.WriteString(writer, "data: first\n\n")
			time.Sleep(100 * time.Millisecond)
			_, _ = io.WriteString(writer, "data: second\n\n")
			_ = writer.Close()
		}()
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: reader, Request: r}, nil
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	req.Header.Set("Authorization", "Bearer ok")
	w := &flushRecorder{header: make(http.Header), flushed: make(chan struct{}, 1)}
	started := time.Now()
	done := make(chan struct{})
	go func() { g.ServeHTTP(w, req); close(done) }()
	select {
	case <-w.flushed:
		if elapsed := time.Since(started); elapsed >= 80*time.Millisecond {
			t.Fatalf("first event was buffered for %v", elapsed)
		}
	case <-time.After(80 * time.Millisecond):
		t.Fatal("first SSE event was not flushed")
	}
	<-done
}

type flushRecorder struct {
	header  http.Header
	mu      sync.Mutex
	body    bytes.Buffer
	status  int
	flushed chan struct{}
}

func (w *flushRecorder) Header() http.Header    { return w.header }
func (w *flushRecorder) WriteHeader(status int) { w.status = status }
func (w *flushRecorder) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.body.Write(p)
}
func (w *flushRecorder) Flush() {
	select {
	case w.flushed <- struct{}{}:
	default:
	}
}

func TestCancellationPropagates(t *testing.T) {
	u, _ := url.Parse("http://127.0.0.1:8000")
	g := New(u, VerifyFunc(func(context.Context, string, string) (Principal, error) { return Principal{}, nil }))
	seen := make(chan struct{})
	started := make(chan struct{})
	g.proxy.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
		close(started)
		<-r.Context().Done()
		close(seen)
		return nil, r.Context().Err()
	})
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest("POST", "/v1/responses/id/cancel", nil).WithContext(ctx)
	req.Header.Set("Authorization", "Bearer ok")
	done := make(chan struct{})
	go func() { g.ServeHTTP(httptest.NewRecorder(), req); close(done) }()
	<-started
	cancel()
	<-done
	select {
	case <-seen:
	default:
		t.Fatal("upstream context not canceled")
	}
}
