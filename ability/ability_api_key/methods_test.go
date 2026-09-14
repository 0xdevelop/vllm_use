package ability_api_key

import (
	"testing"

	"github.com/0xdevelop/vllm-use/api/api_supported_methods"
)

func TestMutationMethodsPublishExactAPIKeyIDSchema(t *testing.T) {
	api_supported_methods.SupportedMethodsSetup()
	LoadAPIMethods()

	for _, name := range []string{MethodEnable, MethodDisable, MethodDelete} {
		method, ok := api_supported_methods.Method(name)
		if !ok {
			t.Fatalf("method %q is not registered", name)
		}
		properties, ok := method.InputSchema["properties"].(map[string]interface{})
		if !ok {
			t.Fatalf("method %q properties = %#v", name, method.InputSchema["properties"])
		}
		id, ok := properties["id"].(map[string]interface{})
		if !ok {
			t.Fatalf("method %q ID schema = %#v", name, properties["id"])
		}
		if id["minLength"] != keyIDLength || id["maxLength"] != keyIDLength || id["pattern"] != "^[A-Za-z0-9]+$" {
			t.Fatalf("method %q publishes incomplete ID bounds: %#v", name, id)
		}
	}
}
