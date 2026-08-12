package gateway

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestRoutingAuthStreamingAndErrors(t *testing.T) {
	u, _ := url.Parse("http://127.0.0.1:8000")
	g := New(u, VerifyFunc(func(_ context.Context, k, s string) error {
		if k != "ok" || s != "inference" {
			return errors.New("bad")
		}
		return nil
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

func TestCancellationPropagates(t *testing.T) {
	u, _ := url.Parse("http://127.0.0.1:8000")
	g := New(u, VerifyFunc(func(context.Context, string, string) error { return nil }))
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
