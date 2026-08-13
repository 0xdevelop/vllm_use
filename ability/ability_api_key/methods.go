package ability_api_key

import (
	"context"
	"errors"

	"github.com/0xdevelop/vllm-use/api/api_supported_methods"
)

const (
	MethodCreate  = "api_keys.create"
	MethodList    = "api_keys.list"
	MethodEnable  = "api_keys.enable"
	MethodDisable = "api_keys.disable"
	MethodDelete  = "api_keys.delete"
)

var currentManager *Manager

func Setup(manager *Manager) { currentManager = manager }

func LoadAPIMethods() {
	add(MethodCreate, "创建 API Key", map[string]interface{}{"name": str(), "scopes": map[string]interface{}{"type": "array", "items": str()}}, []string{"scopes"}, func(ctx context.Context, input interface{}) (interface{}, error) {
		var in struct {
			Name   string   `json:"name"`
			Scopes []string `json:"scopes"`
		}
		if err := api_supported_methods.DecodeArguments(input, &in); err != nil {
			return nil, err
		}
		key, secret, err := manager().CreateNamed(ctx, in.Name, in.Scopes)
		return map[string]interface{}{"key": key, "secret": secret}, err
	})
	add(MethodList, "列出 API Key", nil, nil, func(ctx context.Context, _ interface{}) (interface{}, error) { return manager().List(ctx) })
	setEnabled := func(enabled bool) func(context.Context, interface{}) (interface{}, error) {
		return func(ctx context.Context, input interface{}) (interface{}, error) {
			var in struct {
				ID string `json:"id"`
			}
			if err := api_supported_methods.DecodeArguments(input, &in); err != nil {
				return nil, err
			}
			err := manager().SetEnabled(ctx, in.ID, enabled)
			return map[string]bool{map[bool]string{true: "enabled", false: "disabled"}[enabled]: err == nil}, err
		}
	}
	add(MethodEnable, "启用 API Key", map[string]interface{}{"id": str()}, []string{"id"}, setEnabled(true))
	add(MethodDisable, "禁用 API Key", map[string]interface{}{"id": str()}, []string{"id"}, setEnabled(false))
	add(MethodDelete, "删除 API Key", map[string]interface{}{"id": str()}, []string{"id"}, func(ctx context.Context, input interface{}) (interface{}, error) {
		var in struct {
			ID string `json:"id"`
		}
		if err := api_supported_methods.DecodeArguments(input, &in); err != nil {
			return nil, err
		}
		err := manager().Delete(ctx, in.ID)
		return map[string]bool{"deleted": err == nil}, err
	})
}

func manager() *Manager {
	if currentManager == nil {
		panic(errors.New("API key ability is not initialized"))
	}
	return currentManager
}
func add(name, description string, properties map[string]interface{}, required []string, execute func(context.Context, interface{}) (interface{}, error)) {
	api_supported_methods.AddMethod(&api_supported_methods.SupportedMethod{Name: name, Description: description, Scope: "mcp.admin", InputSchema: api_supported_methods.ObjectSchema(properties, required), Execute: execute})
}
func str() map[string]interface{} { return map[string]interface{}{"type": "string"} }
