package ability

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/0xdevelop/vllm-use/ability/ability_gpu"
	"github.com/0xdevelop/vllm-use/ability/ability_model"
	"github.com/0xdevelop/vllm-use/api/api_executer"
	"github.com/0xdevelop/vllm-use/api/api_supported_methods"
	"github.com/0xdevelop/vllm-use/db/sqlite"
)

type gpuRunner struct{ output []byte }

func (r gpuRunner) Output(context.Context, string, ...string) ([]byte, error) { return r.output, nil }

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
