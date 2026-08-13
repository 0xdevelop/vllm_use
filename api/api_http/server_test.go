package api_http

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/0xdevelop/vllm-use/ability/ability_api_key"
	"github.com/0xdevelop/vllm-use/ability/ability_download"
	"github.com/0xdevelop/vllm-use/ability/ability_gpu"
	"github.com/0xdevelop/vllm-use/ability/ability_model"
	"github.com/0xdevelop/vllm-use/ability/ability_runtime"
	"github.com/0xdevelop/vllm-use/db/sqlite"
)

type apiGPU struct{}

func (apiGPU) Output(context.Context, string, ...string) ([]byte, error) { return nil, nil }

type apiDownloadRunner struct{}
type apiDownloadCommand struct{}

func (apiDownloadRunner) CommandContext(context.Context, string, ...string) ability_download.Command {
	return apiDownloadCommand{}
}
func (apiDownloadCommand) StdoutPipe() (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("")), nil
}
func (apiDownloadCommand) StderrPipe() (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("")), nil
}
func (apiDownloadCommand) Start() error { return nil }
func (apiDownloadCommand) Wait() error  { return nil }

func testServer(t *testing.T) (*Server, string) {
	t.Helper()
	root := t.TempDir()
	st, err := sqlite.Open(filepath.Join(root, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	modelsRoot := filepath.Join(root, "models")
	if err := os.Mkdir(modelsRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	dl := ability_download.NewWithOptions("unused", apiDownloadRunner{}, 1, 10)
	dl.SetRoot(modelsRoot)
	dl.SetStore(st)
	sup := ability_runtime.NewSupervisor("unused", time.Millisecond, time.Millisecond)
	s := &Server{Models: ability_model.New(st, modelsRoot), Keys: ability_api_key.New(st), GPU: ability_gpu.New(apiGPU{}), Runtime: sup, Switch: ability_runtime.NewSwitchService(sup), Downloads: dl, Store: st, AdminToken: "admin", RequireAdmin: true, Web: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("web")) })}
	return s, modelsRoot
}

func request(t *testing.T, h http.Handler, method, path, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func decodeObject(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var got map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON response %d %q: %v", w.Code, w.Body.String(), err)
	}
	return got
}

func TestAdminAuthErrorsAndWebNamespace(t *testing.T) {
	s, _ := testServer(t)
	h := s.Handler()
	for _, token := range []string{"", "wrong"} {
		w := request(t, h, http.MethodGet, "/api/models", token, "")
		if w.Code != http.StatusUnauthorized || decodeObject(t, w)["error"] != "unauthorized" {
			t.Fatalf("auth response: %d %s", w.Code, w.Body.String())
		}
	}
	w := request(t, h, http.MethodPost, "/api/models/huggingface", "admin", `{"repository":"owner/model","unknown":true}`)
	if w.Code != http.StatusBadRequest || decodeObject(t, w)["error"] != "invalid JSON" {
		t.Fatalf("decode response: %d %s", w.Code, w.Body.String())
	}
	w = request(t, h, http.MethodGet, "/api/not-real", "admin", "")
	if w.Code != http.StatusNotFound || decodeObject(t, w)["error"] != "not found" {
		t.Fatalf("not found response: %d %s", w.Code, w.Body.String())
	}
	w = request(t, h, http.MethodGet, "/dashboard", "", "")
	if w.Code != http.StatusOK || w.Body.String() != "web" {
		t.Fatalf("web fallback: %d %q", w.Code, w.Body.String())
	}
}

func TestMajorAdminRoutesAndJSONContract(t *testing.T) {
	s, modelsRoot := testServer(t)
	h := s.Handler()
	w := request(t, h, http.MethodPost, "/api/models/huggingface", "admin", `{"repository":"owner/model","revision":"main"}`)
	model := decodeObject(t, w)
	if w.Code != http.StatusOK || model["id"] == nil || model["ID"] != nil || model["repository"] != "owner/model" {
		t.Fatalf("model contract: %d %#v", w.Code, model)
	}
	modelID := model["id"].(string)
	w = request(t, h, http.MethodGet, "/api/models/"+modelID, "admin", "")
	if w.Code != http.StatusOK || decodeObject(t, w)["id"] != modelID {
		t.Fatalf("model get: %d %s", w.Code, w.Body.String())
	}
	w = request(t, h, http.MethodGet, "/api/models", "admin", "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"size_bytes"`) {
		t.Fatalf("model list: %d %s", w.Code, w.Body.String())
	}
	w = request(t, h, http.MethodPost, "/api/keys", "admin", `{"name":"client","scopes":["inference","mcp.read"]}`)
	keyResult := decodeObject(t, w)
	key, _ := keyResult["key"].(map[string]any)
	if w.Code != http.StatusOK || key["id"] == nil || keyResult["secret"] == "" {
		t.Fatalf("key create: %d %#v", w.Code, keyResult)
	}
	keyID := key["id"].(string)
	for _, action := range []string{"disable", "enable"} {
		w = request(t, h, http.MethodPost, "/api/keys/"+keyID+"/"+action, "admin", "")
		if w.Code != http.StatusOK {
			t.Fatalf("key %s: %d %s", action, w.Code, w.Body.String())
		}
	}
	w = request(t, h, http.MethodPost, "/api/keys", "admin", `{"name":"bad","scopes":["models.read"]}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("invalid scope accepted: %d %s", w.Code, w.Body.String())
	}
	w = request(t, h, http.MethodGet, "/api/gpus", "admin", "")
	if w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != "[]" {
		t.Fatalf("gpu list: %d %s", w.Code, w.Body.String())
	}
	destination := filepath.Join(modelsRoot, "owner--model")
	w = request(t, h, http.MethodPost, "/api/downloads", "admin", `{"id":"job","repository":"owner/model","destination":`+jsonString(destination)+`,"token":"x","destination_path":"bad"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("unknown download field accepted: %d %s", w.Code, w.Body.String())
	}
	w = request(t, h, http.MethodPost, "/api/downloads", "admin", `{"id":"linked","model_id":`+jsonString(modelID)+`,"repository":"owner/model","revision":"main","destination":`+jsonString(destination)+`}`)
	linked := decodeObject(t, w)
	if w.Code != http.StatusOK || linked["model_id"] != modelID || linked["revision"] != "main" || linked["repository"] != "owner/model" {
		t.Fatalf("linked download contract: %d %#v", w.Code, linked)
	}
	for deadline := time.Now().Add(time.Second); ; time.Sleep(time.Millisecond) {
		job, _ := s.Downloads.Status("linked")
		if job.State != ability_download.Running {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("linked download did not finish")
		}
	}
	w = request(t, h, http.MethodPost, "/api/runtime/start", "admin", `{"options":{"model":"m","host":"127.0.0.1","port":8000,"tensor_parallel":1,"served_model_name":"m","extra_args":[],"TensorParallel":2},"health_url":""}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("unknown runtime field accepted: %d %s", w.Code, w.Body.String())
	}
	w = request(t, h, http.MethodPost, "/api/runtime/start", "admin", `{"options":{"model":"m","host":"127.0.0.1","port":8000,"tensor_parallel":1,"pipeline_parallel_size":1,"gpu_devices":[0],"gpu_memory_utilization":0.9,"max_model_len":4096,"dtype":"auto","quantization":"awq","trust_remote_code":true,"tool_call_parser":"hermes","reasoning_parser":"deepseek_r1","enable_auto_tool_choice":true,"served_model_name":"m","extra_args":[]},"health_url":"http://127.0.0.1:8000/health"}`)
	if w.Code == http.StatusBadRequest && decodeObject(t, w)["error"] == "invalid JSON" {
		t.Fatalf("Web runtime contract rejected: %s", w.Body.String())
	}
	w = request(t, h, http.MethodDelete, "/api/keys/"+keyID, "admin", "")
	if w.Code != http.StatusOK {
		t.Fatalf("key delete: %d %s", w.Code, w.Body.String())
	}
	w = request(t, h, http.MethodDelete, "/api/models/"+modelID, "admin", "")
	if w.Code != http.StatusOK {
		t.Fatalf("model delete: %d %s", w.Code, w.Body.String())
	}
}

func TestSettingsSecretRedaction(t *testing.T) {
	s, _ := testServer(t)
	h := s.Handler()
	w := request(t, h, http.MethodPut, "/api/settings", "admin", `[{"key":"plain","value":"visible","secret":false,"updated_at":"0001-01-01T00:00:00Z"},{"key":"secret","value":"never-return-this","secret":true,"updated_at":"0001-01-01T00:00:00Z"}]`)
	if w.Code != http.StatusOK {
		t.Fatalf("put settings: %d %s", w.Code, w.Body.String())
	}
	w = request(t, h, http.MethodGet, "/api/settings", "admin", "")
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), "never-return-this") || !strings.Contains(w.Body.String(), `"key":"secret","secret":true`) {
		t.Fatalf("settings redaction: %d %s", w.Code, w.Body.String())
	}
}

func jsonString(v string) string {
	b, _ := json.Marshal(v)
	return string(b)
}
