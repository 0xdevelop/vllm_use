package ability_gpu

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

type fake struct {
	out string
	err error
}

func (f fake) Output(context.Context, string, ...string) ([]byte, error) { return []byte(f.out), f.err }
func TestList(t *testing.T) {
	g, e := New(fake{out: "0, RTX 4090, uuid, 24564, 100\n"}).List(context.Background())
	if e != nil || len(g) != 1 || g[0].MemoryTotalMiB != 24564 {
		t.Fatalf("%v %v", g, e)
	}
	g, e = New(fake{err: exec.ErrNotFound}).List(context.Background())
	if e != nil || len(g) != 0 {
		t.Fatalf("degrade: %v %v", g, e)
	}
}

func TestListReturnsOperationalNVIDIAErrors(t *testing.T) {
	g, err := New(fake{err: errors.New("driver unavailable")}).List(context.Background())
	if err == nil || g != nil {
		t.Fatalf("operational failure must be explicit: gpus=%v err=%v", g, err)
	}
}

func TestHostNVIDIASMIReceivesLeastPrivilegeEnvironment(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nvidia-smi")
	script := `#!/bin/sh
if [ "${VLLM_USE_ADMIN_TOKEN+x}" = x ] || [ "${VLLM_USE_UPSTREAM_API_KEY+x}" = x ]; then
  exit 41
fi
if [ "$VLLM_USE_GPU_TEST_MARKER" != retained ]; then
  exit 42
fi
printf '0, Test GPU, test-uuid, 1024, 128\n'
`
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("VLLM_USE_ADMIN_TOKEN", "synthetic-admin-secret")
	t.Setenv("VLLM_USE_UPSTREAM_API_KEY", "synthetic-upstream-secret")
	t.Setenv("VLLM_USE_GPU_TEST_MARKER", "retained")

	gpus, err := New(nil).List(context.Background())
	if err != nil || len(gpus) != 1 || gpus[0].UUID != "test-uuid" {
		t.Fatalf("sanitized nvidia-smi execution failed: gpus=%v err=%v", gpus, err)
	}
}
