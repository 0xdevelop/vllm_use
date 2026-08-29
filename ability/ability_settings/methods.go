package ability_settings

import (
	"context"
	"errors"

	"github.com/0xdevelop/vllm-use/api/api_supported_methods"
	"github.com/0xdevelop/vllm-use/db/sqlite"
)

const (
	MethodList           = "settings.list"
	MethodUpdate         = "settings.update"
	MethodDelete         = "settings.delete"
	MethodRecentRequests = "requests.recent"
)

var currentStore *sqlite.Store

func Setup(store *sqlite.Store) { currentStore = store }

func LoadAPIMethods() {
	add(MethodList, "读取设置", nil, nil, func(ctx context.Context, _ interface{}) (interface{}, error) { return store().Settings(ctx) })
	settingSchema := map[string]interface{}{
		"type":                 "object",
		"properties":           map[string]interface{}{"key": map[string]interface{}{"type": "string", "minLength": 1, "maxLength": 128}, "value": map[string]interface{}{"type": "string", "maxLength": 65536}},
		"required":             []string{"key", "value"},
		"additionalProperties": false,
	}
	add(MethodUpdate, "更新非敏感设置（凭据必须通过环境变量或 CLI flags 提供）", map[string]interface{}{"settings": map[string]interface{}{"type": "array", "items": settingSchema}}, []string{"settings"}, func(ctx context.Context, input interface{}) (interface{}, error) {
		var in struct {
			Settings []sqlite.Setting `json:"settings"`
		}
		if err := api_supported_methods.DecodeArguments(input, &in); err != nil {
			return nil, err
		}
		err := store().PutSettings(ctx, in.Settings)
		return map[string]bool{"updated": err == nil}, err
	})
	add(MethodDelete, "删除非敏感设置", map[string]interface{}{"key": map[string]interface{}{"type": "string", "minLength": 1, "maxLength": 128}}, []string{"key"}, func(ctx context.Context, input interface{}) (interface{}, error) {
		var in struct {
			Key string `json:"key"`
		}
		if err := api_supported_methods.DecodeArguments(input, &in); err != nil {
			return nil, err
		}
		err := store().DeleteSetting(ctx, in.Key)
		return map[string]bool{"deleted": err == nil}, err
	})
	add(MethodRecentRequests, "读取最近请求", map[string]interface{}{"limit": map[string]interface{}{"type": "integer", "minimum": 1, "maximum": 500}}, nil, func(ctx context.Context, input interface{}) (interface{}, error) {
		var in struct {
			Limit int `json:"limit"`
		}
		if err := api_supported_methods.DecodeArguments(input, &in); err != nil {
			return nil, err
		}
		return store().RecentRequests(ctx, in.Limit)
	})
}

func store() *sqlite.Store {
	if currentStore == nil {
		panic(errors.New("settings ability is not initialized"))
	}
	return currentStore
}
func add(name, description string, properties map[string]interface{}, required []string, execute func(context.Context, interface{}) (interface{}, error)) {
	api_supported_methods.AddMethod(&api_supported_methods.SupportedMethod{Name: name, Description: description, Scope: "mcp.admin", InputSchema: api_supported_methods.ObjectSchema(properties, required), Execute: execute})
}
