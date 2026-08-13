package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandlerIndexAssetAndFallback(t *testing.T) {
	h := Handler()
	for _, tc := range []struct{ path, contains string }{{"/", "vLLM Use"}, {"/models/abc", "vLLM Use"}} {
		r := httptest.NewRequest(http.MethodGet, tc.path, nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), tc.contains) {
			t.Fatalf("%s: status=%d body=%q", tc.path, w.Code, w.Body.String())
		}
	}

	index := httptest.NewRecorder()
	h.ServeHTTP(index, httptest.NewRequest(http.MethodGet, "/", nil))
	const marker = `href="`
	start := strings.Index(index.Body.String(), marker)
	if start < 0 {
		t.Fatalf("index has no stylesheet: %q", index.Body.String())
	}
	start += len(marker)
	end := strings.Index(index.Body.String()[start:], `"`)
	if end < 0 {
		t.Fatalf("index has malformed stylesheet link: %q", index.Body.String())
	}
	assetPath := index.Body.String()[start : start+end]
	asset := httptest.NewRecorder()
	h.ServeHTTP(asset, httptest.NewRequest(http.MethodGet, assetPath, nil))
	if asset.Code != http.StatusOK || !strings.Contains(asset.Header().Get("Content-Type"), "text/css") || asset.Body.Len() == 0 {
		t.Fatalf("%s: status=%d content-type=%q body=%q", assetPath, asset.Code, asset.Header().Get("Content-Type"), asset.Body.String())
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
