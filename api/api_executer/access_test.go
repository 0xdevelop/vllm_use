package api_executer

import (
	"context"
	"errors"
	"testing"

	"github.com/0xdevelop/vllm-use/api/api_supported_methods"
)

func TestExecuteAbilityEnforcesAdapterAccessContext(t *testing.T) {
	api_supported_methods.SupportedMethodsSetup()
	api_supported_methods.AddMethod(&api_supported_methods.SupportedMethod{
		Name: "runtime.status", Scope: "mcp.runtime",
		InputSchema: api_supported_methods.ObjectSchema(nil, nil),
		Execute:     func(context.Context, interface{}) (interface{}, error) { return "ok", nil },
	})

	if _, err := ExecuteAbility(context.Background(), "runtime.status", map[string]interface{}{}); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("missing context was not denied: %v", err)
	}
	if _, err := ExecuteAbility(WithScopes(context.Background(), []string{"mcp.models"}), "runtime.status", map[string]interface{}{}); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("wrong scope was not denied: %v", err)
	}
	for name, ctx := range map[string]context.Context{
		"matching scope": WithScopes(context.Background(), []string{"mcp.runtime"}),
		"mcp admin":      WithScopes(context.Background(), []string{"mcp.admin"}),
		"http admin":     WithAdmin(context.Background()),
	} {
		value, err := ExecuteAbility(ctx, "runtime.status", map[string]interface{}{})
		if err != nil || value != "ok" {
			t.Fatalf("%s failed: value=%#v err=%v", name, value, err)
		}
	}
}
