package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandlerIndexAssetAndFallback(t *testing.T) {
	h := Handler()
	for _, tc := range []struct{ path, contains string }{{"/", "vLLM Use"}, {"/assets/app.css", ":root"}, {"/models/abc", "vLLM Use"}} {
		r := httptest.NewRequest(http.MethodGet, tc.path, nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), tc.contains) {
			t.Fatalf("%s: status=%d body=%q", tc.path, w.Code, w.Body.String())
		}
	}
}

func TestHandlerDoesNotSwallowBackendNamespaces(t *testing.T) {
	h := Handler()
	for _, p := range []string{"/api/models", "/mcp", "/v1/models"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, p, nil))
		if w.Code != http.StatusNotFound {
			t.Fatalf("%s: got %d", p, w.Code)
		}
	}
}
