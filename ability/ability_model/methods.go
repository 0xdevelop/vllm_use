package ability_model

import (
	"context"
	"errors"

	"github.com/0xdevelop/vllm-use/api/api_supported_methods"
)

const (
	MethodScan          = "models.scan"
	MethodGet           = "models.get"
	MethodRegisterHF    = "models.register_huggingface"
	MethodRegisterLocal = "models.register_local"
	MethodDelete        = "models.delete"
)

var activeModel func() string

func SetupActiveModel(check func() string) { activeModel = check }

func LoadManagementMethods() {
	add(MethodScan, "扫描模型目录", nil, nil, func(ctx context.Context, _ interface{}) (interface{}, error) {
		return registry().Scan(ctx)
	})
	add(MethodGet, "读取模型", map[string]interface{}{"id": stringSchema()}, []string{"id"}, func(ctx context.Context, input interface{}) (interface{}, error) {
		var in struct {
			ID string `json:"id"`
		}
		if err := api_supported_methods.DecodeArguments(input, &in); err != nil {
			return nil, err
		}
		return registry().Get(ctx, in.ID)
	})
	add(MethodRegisterHF, "注册 Hugging Face 模型", map[string]interface{}{"repository": stringSchema(), "revision": stringSchema()}, []string{"repository"}, func(ctx context.Context, input interface{}) (interface{}, error) {
		var in struct {
			Repository string `json:"repository"`
			Revision   string `json:"revision"`
		}
		if err := api_supported_methods.DecodeArguments(input, &in); err != nil {
			return nil, err
		}
		return registry().RegisterHuggingFace(ctx, in.Repository, in.Revision)
	})
	add(MethodRegisterLocal, "注册本地模型", map[string]interface{}{"name": stringSchema(), "path": stringSchema()}, []string{"name", "path"}, func(ctx context.Context, input interface{}) (interface{}, error) {
		var in struct {
			Name string `json:"name"`
			Path string `json:"path"`
		}
		if err := api_supported_methods.DecodeArguments(input, &in); err != nil {
			return nil, err
		}
		return registry().RegisterLocal(ctx, in.Name, in.Path)
	})
	add(MethodDelete, "删除模型", map[string]interface{}{"id": stringSchema(), "files": map[string]interface{}{"type": "boolean"}}, []string{"id"}, func(ctx context.Context, input interface{}) (interface{}, error) {
		var in struct {
			ID    string `json:"id"`
			Files bool   `json:"files"`
		}
		if err := api_supported_methods.DecodeArguments(input, &in); err != nil {
			return nil, err
		}
		if activeModel != nil && activeModel() == in.ID {
			return nil, errors.New("refusing to delete the running model")
		}
		err := registry().Delete(ctx, in.ID, in.Files)
		return map[string]bool{"deleted": err == nil}, err
	})
}

func registry() *Registry {
	if currentRegistry == nil {
		panic("model ability is not initialized")
	}
	return currentRegistry
}

func add(name, description string, properties map[string]interface{}, required []string, execute func(context.Context, interface{}) (interface{}, error)) {
	api_supported_methods.AddMethod(&api_supported_methods.SupportedMethod{Name: name, Description: description, Scope: "mcp.models", InputSchema: api_supported_methods.ObjectSchema(properties, required), Execute: execute})
}
func stringSchema() map[string]interface{} { return map[string]interface{}{"type": "string"} }
