package ability

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/0xdevelop/vllm-use/ability/ability_gpu"
	"github.com/0xdevelop/vllm-use/ability/ability_model"
	"github.com/0xdevelop/vllm-use/api/api_executer"
	"github.com/0xdevelop/vllm-use/api/api_supported_methods"
	"github.com/0xdevelop/vllm-use/db/sqlite"
)

type gpuRunner struct {
	output []byte
	err    error
}

func (r gpuRunner) Output(context.Context, string, ...string) ([]byte, error) {
	return r.output, r.err
}

func TestSystemStatusReportsRealHostDependencyPreflight(t *testing.T) {
	bin := t.TempDir()
	for _, name := range []string{"configured-vllm", "configured-hf", "nvidia-smi"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin)
	SetupHostExecutables("configured-vllm", "configured-hf")
	t.Cleanup(func() { SetupHostExecutables("vllm", "hf") })
	ability_gpu.Setup(ability_gpu.New(gpuRunner{output: []byte("0, NVIDIA Test, GPU-test, 24000, 1024\n")}))
	LoadAbilityAPIMethods()

	value, err := api_executer.ExecuteAbility(api_executer.WithScopes(context.Background(), []string{"mcp.read"}), MethodSystem, map[string]interface{}{})
	if err != nil {
		t.Fatal(err)
	}
	status, ok := value.(SystemStatus)
	if !ok {
		t.Fatalf("system result = %T, want SystemStatus", value)
	}
	if status.GoVersion != runtime.Version() || len(status.Dependencies) != 3 {
		t.Fatalf("unexpected system status: %#v", status)
	}
	for _, dependency := range status.Dependencies {
		if dependency.Status != "available" || dependency.ResolvedPath == "" {
			t.Fatalf("dependency not available: %#v", dependency)
		}
	}
	if status.Dependencies[2].DeviceCount == nil || *status.Dependencies[2].DeviceCount != 1 {
		t.Fatalf("GPU diagnostic = %#v", status.Dependencies[2])
	}
}

func TestSystemStatusReportsMissingHostDependencies(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	SetupHostExecutables("missing-vllm", "missing-hf")
	t.Cleanup(func() { SetupHostExecutables("vllm", "hf") })

	status := hostDependencyStatus(context.Background())
	for _, dependency := range status {
		if dependency.Status != "missing" || dependency.Error == "" {
			t.Fatalf("missing dependency not diagnosed: %#v", dependency)
		}
	}
}

func TestSystemStatusReportsNVIDIADriverFailure(t *testing.T) {
	bin := t.TempDir()
	for _, name := range []string{"vllm", "hf", "nvidia-smi"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin)
	SetupHostExecutables("vllm", "hf")
	ability_gpu.Setup(ability_gpu.New(gpuRunner{err: errors.New("driver unavailable")}))
	LoadAbilityAPIMethods()

	status := hostDependencyStatus(api_executer.WithScopes(context.Background(), []string{"mcp.read"}))
	nvidia := status[2]
	if nvidia.Status != "error" || nvidia.Error == "" || nvidia.DeviceCount != nil {
		t.Fatalf("broken NVIDIA driver not diagnosed: %#v", nvidia)
	}
}

func TestLoadAbilityAPIMethodsExecutesModelAndGPUAbilities(t *testing.T) {
	store, err := sqlite.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	root := t.TempDir()
	ability_model.Setup(ability_model.New(store, root))
	ability_gpu.Setup(ability_gpu.New(gpuRunner{output: []byte("0, NVIDIA Test, GPU-test, 24000, 1024\n")}))

	LoadAbilityAPIMethods()
	methods := api_supported_methods.Methods()
	if len(methods) < 3 || methods[0].Name != MethodTest || methods[1].Name != ability_model.MethodList {
		t.Fatalf("unexpected method prefix: %#v", methods)
	}
	want := map[string]bool{ability_model.MethodList: true, ability_gpu.MethodList: true, MethodDashboard: true}
	seen := map[string]bool{}
	for _, method := range methods {
		if seen[method.Name] {
			t.Fatalf("duplicate method %q", method.Name)
		}
		seen[method.Name] = true
		delete(want, method.Name)
	}
	if len(want) != 0 {
		t.Fatalf("missing methods: %#v", want)
	}
	for _, name := range []string{ability_model.MethodList, ability_gpu.MethodList} {
		result, executeErr := api_executer.APIExecuter(api_executer.WithAdmin(context.Background()), api_executer.ToolsCallMethod, map[string]interface{}{
			"name": name, "arguments": map[string]interface{}{},
		}, "")
		if executeErr != nil {
			t.Fatalf("execute %s: %v", name, executeErr)
		}
		if result == nil || result.IsError {
			t.Fatalf("unexpected %s result: %#v", name, result)
		}
	}
}
