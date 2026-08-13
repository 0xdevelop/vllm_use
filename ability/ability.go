// Package ability ability/ability.go
package ability

import (
	"context"
	"runtime"

	"github.com/0xdevelop/vllm-use/ability/ability_api_key"
	"github.com/0xdevelop/vllm-use/ability/ability_download"
	"github.com/0xdevelop/vllm-use/ability/ability_gpu"
	"github.com/0xdevelop/vllm-use/ability/ability_model"
	"github.com/0xdevelop/vllm-use/ability/ability_runtime"
	"github.com/0xdevelop/vllm-use/ability/ability_settings"
	"github.com/0xdevelop/vllm-use/api/api_executer"
	"github.com/0xdevelop/vllm-use/api/api_supported_methods"
)

const MethodTest = "test"

const (
	MethodSystem    = "system.get"
	MethodMCPStatus = "mcp.status"
	MethodDashboard = "dashboard.get"
)

func Test(context.Context, interface{}) (interface{}, error) {
	return "this is test method, request is success", nil
}

func LoadAbilityAPIMethods() {
	api_supported_methods.SupportedMethodsSetup()
	api_supported_methods.AddMethod(
		&api_supported_methods.SupportedMethod{
			Name:        MethodTest,
			Description: "检查统一 API 调用链是否可用",
			Public:      true,
			InputSchema: map[string]interface{}{
				"type":                 "object",
				"additionalProperties": false,
			},
			Execute: Test,
		})
	ability_model.LoadAPIMethods()
	ability_model.LoadManagementMethods()
	ability_gpu.LoadAPIMethods()
	ability_download.LoadAPIMethods()
	ability_runtime.LoadAPIMethods()
	ability_api_key.LoadAPIMethods()
	ability_settings.LoadAPIMethods()
	addParentMethod(MethodSystem, "读取系统信息", "mcp.read", func(context.Context, interface{}) (interface{}, error) {
		return map[string]interface{}{"go_version": runtime.Version(), "goos": runtime.GOOS, "goarch": runtime.GOARCH, "cpus": runtime.NumCPU()}, nil
	})
	addParentMethod(MethodMCPStatus, "读取 MCP 状态", "mcp.read", func(context.Context, interface{}) (interface{}, error) {
		return map[string]interface{}{"enabled": true, "transport": "streamable-http", "path": "/mcp", "tools": len(api_supported_methods.Methods())}, nil
	})
	addParentMethod(MethodDashboard, "读取管理面板摘要", "mcp.admin", dashboard)
}

func addParentMethod(name, description, scope string, execute func(context.Context, interface{}) (interface{}, error)) {
	api_supported_methods.AddMethod(&api_supported_methods.SupportedMethod{Name: name, Description: description, Scope: scope, InputSchema: api_supported_methods.ObjectSchema(nil, nil), Execute: execute})
}

func dashboard(ctx context.Context, _ interface{}) (interface{}, error) {
	models, err := api_executer.ExecuteAbility(ctx, ability_model.MethodList, map[string]interface{}{})
	if err != nil {
		return nil, err
	}
	modelList, _ := models.([]ability_model.Model)
	runtimeState, err := api_executer.ExecuteAbility(ctx, ability_runtime.MethodStatus, map[string]interface{}{})
	if err != nil {
		return nil, err
	}
	downloads, err := api_executer.ExecuteAbility(ctx, ability_download.MethodList, map[string]interface{}{})
	if err != nil {
		return nil, err
	}
	recent, err := api_executer.ExecuteAbility(ctx, ability_settings.MethodRecentRequests, map[string]interface{}{"limit": 10})
	return map[string]interface{}{"models": len(modelList), "runtime": runtimeState, "downloads": downloads, "recent_requests": recent}, err
}
