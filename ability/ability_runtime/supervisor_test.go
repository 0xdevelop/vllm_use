//go:build linux

package ability_runtime

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func script(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "fake-vllm")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body+"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	return p
}

type testRoundTrip func(*http.Request) (*http.Response, error)

func (f testRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func readyOptions(t *testing.T, s *Supervisor) Options {
	t.Helper()
	s.client = &http.Client{Transport: testRoundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Scheme != "http" || r.URL.Host != "127.0.0.1:18000" || r.URL.Path != "/health" {
			t.Errorf("unexpected readiness URL %s", r.URL)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("ok")), Header: make(http.Header), Request: r}, nil
	})}
	return Options{Model: "model", Host: "127.0.0.1", Port: 18000}
}

func eventually(t *testing.T, f func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !f() {
		if time.Now().After(deadline) {
			t.Fatal("condition not reached")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestSupervisorLifecycleDuplicateAndIdempotentStop(t *testing.T) {
	s := NewSupervisor(script(t, `trap 'exit 0' TERM; echo ready; while :; do sleep 1; done`), 500*time.Millisecond, time.Second)
	o := readyOptions(t, s)
	if err := s.Start(context.Background(), o, ""); err != nil {
		t.Fatal(err)
	}
	if st := s.State(); st.Status != Running || st.PID == 0 || st.StartedAt == nil || st.ReadyAt == nil {
		t.Fatalf("state %#v", st)
	}
	if err := s.Start(context.Background(), o, ""); err == nil {
		t.Fatal("duplicate start accepted")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := s.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool { return s.State().Status == Stopped })
	if err := s.Stop(ctx); err != nil {
		t.Fatalf("idempotent stop: %v", err)
	}
}

func TestSupervisorAbnormalExit(t *testing.T) {
	s := NewSupervisor(script(t, "echo failure >&2; exit 7"), time.Second, time.Second)
	o := readyOptions(t, s)
	_ = s.Start(context.Background(), o, "")
	eventually(t, func() bool { return s.State().Status == Failed })
	st := s.State()
	if st.ExitError == "" || st.StoppedAt == nil {
		t.Fatalf("state %#v", st)
	}
}

func TestReadinessTimeoutStopsProcess(t *testing.T) {
	s := NewSupervisor(script(t, `trap 'exit 0' TERM; while :; do sleep 1; done`), 100*time.Millisecond, 40*time.Millisecond)
	err := s.Start(context.Background(), Options{Model: "model", Port: 1}, "")
	if err == nil {
		t.Fatal("expected readiness timeout")
	}
	eventually(t, func() bool { st := s.State().Status; return st == Stopped || st == Failed })
}

func TestCallerHealthURLRejectedAndNonLoopbackHostRejected(t *testing.T) {
	s := NewSupervisor(script(t, "exit 0"), time.Second, time.Second)
	if err := s.Start(context.Background(), Options{Model: "model", Port: 8000}, "http://example.com/fake"); err == nil {
		t.Fatal("caller-controlled health URL accepted")
	}
	if _, err := BuildArgs(Options{Model: "model", Host: "0.0.0.0", Port: 8000}); err == nil {
		t.Fatal("non-loopback runtime host accepted")
	}
}

func TestLargeLogLineAndIgnoredTERMRestart(t *testing.T) {
	long := strings.Repeat("x", 128<<10)
	first := script(t, `trap '' TERM; printf '%s\n' "`+long+`"; while :; do sleep 1; done`)
	s := NewSupervisor(first, 30*time.Millisecond, time.Second)
	o := readyOptions(t, s)
	if err := s.Start(context.Background(), o, ""); err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool { return len(s.State().Logs) == 1 })
	if got := len(s.State().Logs[0]); got != len(long) {
		t.Fatalf("large log length %d", got)
	}
	s.binary = script(t, `trap 'exit 0' TERM; while :; do sleep 1; done`)
	if err := s.Restart(context.Background(), o, ""); err != nil {
		t.Fatal(err)
	}
	if len(s.State().Logs) != 0 {
		t.Fatal("old run logs contaminated replacement run")
	}
	if err := s.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestGPUDevicesUseCUDAVisibleDevices(t *testing.T) {
	output := filepath.Join(t.TempDir(), "cuda.txt")
	s := NewSupervisor(script(t, `printf '%s' "$CUDA_VISIBLE_DEVICES" > "`+output+`"; trap 'exit 0' TERM; while :; do sleep 1; done`), time.Second, time.Second)
	o := readyOptions(t, s)
	o.GPUDevices = []int{2, 5}
	if err := s.Start(context.Background(), o, ""); err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool {
		b, err := os.ReadFile(output)
		return err == nil && string(b) == "2,5"
	})
	if err := s.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestUnexpectedLeaderExitCleansChildProcess(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	s := NewSupervisor(script(t, `sleep 30 & echo $! > "`+pidFile+`"; exit 7`), time.Second, time.Second)
	o := readyOptions(t, s)
	_ = s.Start(context.Background(), o, "")
	eventually(t, func() bool { return s.State().Status == Failed })
	b, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool { return syscall.Kill(pid, 0) == syscall.ESRCH })
}

func TestSwitchResolvesModelIDToReadyManagedPath(t *testing.T) {
	argsFile := filepath.Join(t.TempDir(), "args.txt")
	s := NewSupervisor(script(t, `printf '%s\n' "$@" > "`+argsFile+`"; trap 'exit 0' TERM; while :; do sleep 1; done`), time.Second, time.Second)
	o := readyOptions(t, s)
	o.Model = ""
	modelPath := filepath.Join(t.TempDir(), "managed-model")
	x := NewSwitchService(s)
	x.SetModelResolver(func(_ context.Context, id string) (ModelTarget, error) {
		return ModelTarget{ID: id, LocalPath: modelPath, Status: "ready"}, nil
	})
	if err := x.Switch(context.Background(), "model-1", o, ""); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = x.Stop(context.Background()) })
	eventually(t, func() bool {
		got, err := os.ReadFile(argsFile)
		return err == nil && strings.Contains(string(got), "serve\n"+modelPath+"\n")
	})
	if got := x.Active(); got != "model-1" {
		t.Fatalf("active model = %q", got)
	}
}

func TestSwitchRejectsUnreadyOrMismatchedModelBeforeStarting(t *testing.T) {
	s := NewSupervisor(script(t, "exit 99"), time.Second, time.Second)
	x := NewSwitchService(s)
	x.SetModelResolver(func(_ context.Context, id string) (ModelTarget, error) {
		return ModelTarget{ID: id, LocalPath: filepath.Join(t.TempDir(), "model"), Status: "downloading"}, nil
	})
	if err := x.Switch(context.Background(), "model-1", Options{Port: 8000}, ""); err == nil || !strings.Contains(err.Error(), "not ready") {
		t.Fatalf("unready model error = %v", err)
	}
	if s.State().Status != Stopped {
		t.Fatalf("unready model started runtime: %#v", s.State())
	}

	readyPath := filepath.Join(t.TempDir(), "ready")
	x.SetModelResolver(func(_ context.Context, id string) (ModelTarget, error) {
		return ModelTarget{ID: id, LocalPath: readyPath, Status: "ready"}, nil
	})
	if err := x.Switch(context.Background(), "model-1", Options{Model: "/different", Port: 8000}, ""); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("mismatched model error = %v", err)
	}
	if s.State().Status != Stopped {
		t.Fatalf("mismatched model started runtime: %#v", s.State())
	}
}
