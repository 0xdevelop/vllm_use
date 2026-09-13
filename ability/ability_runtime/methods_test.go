package ability_runtime

import (
	"context"
	"strings"
	"testing"

	"github.com/0xdevelop/vllm-use/api/api_supported_methods"
)

func TestRuntimeMethodSchemasDescribeBoundedOptions(t *testing.T) {
	api_supported_methods.SupportedMethodsSetup()
	LoadAPIMethods()
	t.Cleanup(api_supported_methods.SupportedMethodsSetup)

	for _, name := range []string{MethodStart, MethodRestart, MethodSwitch} {
		method, ok := api_supported_methods.Method(name)
		if !ok {
			t.Fatalf("method %q not registered", name)
		}
		properties := method.InputSchema["properties"].(map[string]interface{})
		options := properties["options"].(map[string]interface{})
		if options["additionalProperties"] != false {
			t.Fatalf("%s options allow unknown fields: %#v", name, options)
		}
		optionProperties := options["properties"].(map[string]interface{})
		if optionProperties["model"].(map[string]interface{})["maxLength"] != maxRuntimeModelBytes {
			t.Fatalf("%s model boundary missing: %#v", name, optionProperties["model"])
		}
		extra := optionProperties["extra_args"].(map[string]interface{})
		if extra["maxItems"] != maxExtraArgs {
			t.Fatalf("%s extra_args boundary missing: %#v", name, extra)
		}
	}
}

func TestRuntimeAbilityRejectsOversizedOptionsBeforeLaunching(t *testing.T) {
	api_supported_methods.SupportedMethodsSetup()
	s := NewSupervisor("must-not-run", 1, 1)
	Setup(s, NewSwitchService(s))
	LoadAPIMethods()
	t.Cleanup(api_supported_methods.SupportedMethodsSetup)

	method, ok := api_supported_methods.Method(MethodStart)
	if !ok {
		t.Fatal("runtime.start not registered")
	}
	_, executeErr := method.Execute(context.Background(), map[string]interface{}{
		"options": map[string]interface{}{"model": "m", "port": 8000, "extra_args": []interface{}{map[string]interface{}{"name": "template", "values": []interface{}{strings.Repeat("v", maxExtraArgValueBytes+1)}}}},
	})
	if executeErr == nil {
		t.Fatal("oversized runtime argument reached launch boundary")
	}
}
