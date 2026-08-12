//go:build linux

package runtime

import (
	"context"
	"os"
	"path/filepath"
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
	o := Options{Model: "model", Port: 8000}
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
	if err := s.Start(context.Background(), Options{Model: "model", Port: 8000}, ""); err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool { return s.State().Status == Failed })
	st := s.State()
	if st.ExitError == "" || st.StoppedAt == nil {
		t.Fatalf("state %#v", st)
	}
}

func TestReadinessTimeoutStopsProcess(t *testing.T) {
	s := NewSupervisor(script(t, `trap 'exit 0' TERM; while :; do sleep 1; done`), 100*time.Millisecond, 40*time.Millisecond)
	err := s.Start(context.Background(), Options{Model: "model", Port: 8000}, "http://127.0.0.1:1/health")
	if err == nil {
		t.Fatal("expected readiness timeout")
	}
	eventually(t, func() bool { st := s.State().Status; return st == Stopped || st == Failed })
}
