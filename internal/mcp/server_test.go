package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/0xdevelop/vllm-use/internal/auth"
	"github.com/0xdevelop/vllm-use/internal/download"
	"github.com/0xdevelop/vllm-use/internal/gpu"
	"github.com/0xdevelop/vllm-use/internal/models"
	vruntime "github.com/0xdevelop/vllm-use/internal/runtime"
	"github.com/0xdevelop/vllm-use/internal/store"
)

type verifier struct {
	scopes []string
	wait   bool
}

func (v verifier) Verify(ctx context.Context, token, _ string) (*auth.Key, error) {
	if v.wait {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if token != "valid" {
		return nil, auth.ErrInvalidKey
	}
	return &auth.Key{ID: "test-key", Scopes: v.scopes}, nil
}

type noGPU struct{}

func (noGPU) Output(context.Context, string, ...string) ([]byte, error) { return nil, nil }

func testHandler(t *testing.T, scopes ...string) *Handler {
	return testHandlerMode(t, true, scopes...)
}

func testHandlerMode(t *testing.T, jsonResponse bool, scopes ...string) *Handler {
	t.Helper()
	root := t.TempDir()
	st, err := store.Open(filepath.Join(root, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	modelsRoot := filepath.Join(root, "models")
	if err := os.MkdirAll(modelsRoot, 0700); err != nil {
		t.Fatal(err)
	}
	dl := download.NewWithOptions("unused", nil, 1, 10)
	dl.SetRoot(modelsRoot)
	return New(Dependencies{Models: models.New(st, modelsRoot), Keys: verifier{scopes: scopes}, GPU: gpu.New(noGPU{}), Runtime: vruntime.NewSupervisor("unused", time.Millisecond, time.Millisecond), Downloads: dl}, Options{JSONResponse: jsonResponse})
}

func rpcBody(id int, method, name, args string) string {
	meta := `"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{}}`
	params := `{` + meta + `}`
	if method == "tools/call" {
		params = `{"name":` + strconv.Quote(name) + `,"arguments":` + args + `,` + meta + `}`
	}
	return `{"jsonrpc":"2.0","id":` + strconv.Itoa(id) + `,"method":` + strconv.Quote(method) + `,"params":` + params + `}`
}
func request(method, rpcMethod, name, version, body string) *http.Request {
	r := httptest.NewRequest(method, "http://127.0.0.1/mcp", strings.NewReader(body))
	r.Host = "127.0.0.1"
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Accept", "application/json, text/event-stream")
	r.Header.Set("Authorization", "Bearer valid")
	if version != "" {
		r.Header.Set("Mcp-Protocol-Version", version)
	}
	if rpcMethod != "" {
		r.Header.Set("Mcp-Method", rpcMethod)
	}
	if name != "" {
		r.Header.Set("Mcp-Name", name)
	}
	return r
}

func TestTransportValidation(t *testing.T) {
	h := testHandler(t, "mcp.read")
	cases := []struct {
		name   string
		mutate func(*http.Request)
		want   int
	}{
		{"reject GET", func(r *http.Request) { r.Method = http.MethodGet }, 405},
		{"missing version", func(r *http.Request) { r.Header.Del("Mcp-Protocol-Version") }, 400},
		{"wrong version", func(r *http.Request) { r.Header.Set("Mcp-Protocol-Version", "2025-11-25") }, 400},
		{"session", func(r *http.Request) { r.Header.Set("Mcp-Session-Id", "x") }, 400},
		{"origin", func(r *http.Request) { r.Header.Set("Origin", "https://evil.example") }, 403},
		{"host", func(r *http.Request) { r.Host = "evil.example" }, 403},
		{"auth", func(r *http.Request) { r.Header.Del("Authorization") }, 401},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := request("POST", "tools/list", "", ProtocolVersion, rpcBody(1, "tools/list", "", "{}"))
			tc.mutate(r)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Fatalf("got %d want %d: %s", w.Code, tc.want, w.Body.String())
			}
		})
	}
}

func TestOriginAllowlistAndJSONResponse(t *testing.T) {
	h := testHandler(t, "mcp.read")
	h.origins["https://manager.example"] = struct{}{}
	r := request("POST", "tools/list", "", ProtocolVersion, rpcBody(1, "tools/list", "", "{}"))
	r.Host = "manager.example"
	r.Header.Set("Origin", "https://manager.example")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("%d %q: %s", w.Code, w.Header().Get("Content-Type"), w.Body.String())
	}
}

func TestRequestScopedSSEResponse(t *testing.T) {
	h := testHandlerMode(t, false, "mcp.read")
	r := request("POST", "tools/list", "", ProtocolVersion, rpcBody(1, "tools/list", "", "{}"))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/event-stream") || !strings.Contains(w.Body.String(), "data:") {
		t.Fatalf("expected request-scoped SSE: %d %q %s", w.Code, w.Header().Get("Content-Type"), w.Body.String())
	}
	if w.Header().Get("Mcp-Session-Id") != "" {
		t.Fatal("stateless response issued a session ID")
	}
}

func TestHeaderBodyMismatch(t *testing.T) {
	h := testHandler(t, "mcp.read")
	r := request("POST", "tools/call", "system.status", ProtocolVersion, rpcBody(1, "tools/call", "models.list", "{}"))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if !strings.Contains(w.Body.String(), `-32020`) {
		t.Fatalf("expected -32020: %d %s", w.Code, w.Body.String())
	}
}

func TestScopesAndTools(t *testing.T) {
	call := func(h *Handler, id int, name, args string) map[string]any {
		r := request("POST", "tools/call", name, ProtocolVersion, rpcBody(id, "tools/call", name, args))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		var got map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode %s: %v", w.Body.String(), err)
		}
		return got
	}
	read := testHandler(t, "mcp.read")
	if got := call(read, 1, "models.list", `{}`); got["result"] == nil {
		t.Fatalf("read failed: %#v", got)
	}
	if got := call(read, 2, "models.register", `{"kind":"huggingface","repository":"owner/model"}`); !isToolError(got) {
		t.Fatalf("scope bypass: %#v", got)
	}
	write := testHandler(t, "mcp.models")
	registered := call(write, 3, "models.register", `{"kind":"huggingface","repository":"owner/model"}`)
	id := structuredString(registered, "id")
	if id == "" {
		t.Fatalf("register: %#v", registered)
	}
	if got := call(write, 4, "models.delete", `{"id":`+strconv.Quote(id)+`,"confirm_model_id":"wrong","delete_files":false}`); !isToolError(got) {
		t.Fatalf("unsafe delete: %#v", got)
	}
	if got := call(write, 5, "models.delete", `{"id":`+strconv.Quote(id)+`,"confirm_model_id":`+strconv.Quote(id)+`,"delete_files":false}`); isToolError(got) {
		t.Fatalf("delete: %#v", got)
	}
}
func isToolError(v map[string]any) bool {
	r, _ := v["result"].(map[string]any)
	b, _ := r["isError"].(bool)
	return b
}
func structuredString(v map[string]any, k string) string {
	r, _ := v["result"].(map[string]any)
	s, _ := r["structuredContent"].(map[string]any)
	x, _ := s[k].(string)
	return x
}

func TestRequestCancellation(t *testing.T) {
	h := testHandler(t, "mcp.read")
	h.keys = verifier{wait: true}
	ctx, cancel := context.WithCancel(context.Background())
	r := request("POST", "tools/list", "", ProtocolVersion, rpcBody(1, "tools/list", "", "{}")).WithContext(ctx)
	done := make(chan struct{})
	go func() { h.ServeHTTP(httptest.NewRecorder(), r); close(done) }()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("request cancellation not propagated")
	}
}
