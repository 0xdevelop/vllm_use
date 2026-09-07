// Package ability ability/ability.go
package ability

import (
	"context"
	"errors"
	"os/exec"
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

type DependencyStatus struct {
	Name         string `json:"name"`
	Command      string `json:"command"`
	Status       string `json:"status"`
	ResolvedPath string `json:"resolved_path,omitempty"`
	Error        string `json:"error,omitempty"`
	DeviceCount  *int   `json:"device_count,omitempty"`
}

type SystemStatus struct {
	GoVersion    string             `json:"go_version"`
	GOOS         string             `json:"goos"`
	GOARCH       string             `json:"goarch"`
	CPUs         int                `json:"cpus"`
	Dependencies []DependencyStatus `json:"dependencies"`
}

var hostExecutables = struct {
	vllm string
	hf   string
}{vllm: "vllm", hf: "hf"}

// SetupHostExecutables supplies the operator-configured native commands used
// by system.get. The preflight never substitutes a simulated runtime or GPU.
func SetupHostExecutables(vllm, hf string) {
	hostExecutables.vllm = vllm
	hostExecutables.hf = hf
}

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
	addParentMethod(MethodSystem, "读取系统信息与宿主机依赖预检", "mcp.read", func(ctx context.Context, _ interface{}) (interface{}, error) {
		return SystemStatus{GoVersion: runtime.Version(), GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, CPUs: runtime.NumCPU(), Dependencies: hostDependencyStatus(ctx)}, nil
	})
	addParentMethod(MethodMCPStatus, "读取 MCP 状态", "mcp.read", func(context.Context, interface{}) (interface{}, error) {
		return map[string]interface{}{"enabled": true, "transport": "streamable-http", "path": "/mcp", "tools": len(api_supported_methods.Methods())}, nil
	})
	addParentMethod(MethodDashboard, "读取管理面板摘要", "mcp.admin", dashboard)
}

func addParentMethod(name, description, scope string, execute func(context.Context, interface{}) (interface{}, error)) {
	api_supported_methods.AddMethod(&api_supported_methods.SupportedMethod{Name: name, Description: description, Scope: scope, InputSchema: api_supported_methods.ObjectSchema(nil, nil), Execute: execute})
}

func hostDependencyStatus(ctx context.Context) []DependencyStatus {
	dependencies := []DependencyStatus{
		checkExecutable("vllm", hostExecutables.vllm),
		checkExecutable("huggingface-cli", hostExecutables.hf),
		checkExecutable("nvidia-smi", "nvidia-smi"),
	}
	nvidia := &dependencies[2]
	if nvidia.Status != "available" {
		return dependencies
	}
	value, err := api_executer.ExecuteAbility(ctx, ability_gpu.MethodList, map[string]interface{}{})
	if err != nil {
		nvidia.Status = "error"
		nvidia.Error = err.Error()
		return dependencies
	}
	devices, ok := value.([]ability_gpu.GPU)
	if !ok {
		nvidia.Status = "error"
		nvidia.Error = "unexpected GPU diagnostic result"
		return dependencies
	}
	count := len(devices)
	nvidia.DeviceCount = &count
	return dependencies
}

func checkExecutable(name, command string) DependencyStatus {
	status := DependencyStatus{Name: name, Command: command, Status: "missing"}
	path, err := exec.LookPath(command)
	if err == nil {
		status.Status = "available"
		status.ResolvedPath = path
		return status
	}
	if !errors.Is(err, exec.ErrNotFound) {
		status.Status = "error"
	}
	status.Error = err.Error()
	return status
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
