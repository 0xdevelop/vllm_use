package ability_runtime

import (
	"context"
	"errors"

	"github.com/0xdevelop/vllm-use/api/api_supported_methods"
)

const (
	MethodStatus  = "runtime.status"
	MethodStart   = "runtime.start"
	MethodRestart = "runtime.restart"
	MethodSwitch  = "runtime.switch"
	MethodStop    = "runtime.stop"
)

var currentSupervisor *Supervisor
var currentSwitch *SwitchService

func Setup(supervisor *Supervisor, switchService *SwitchService) {
	currentSupervisor, currentSwitch = supervisor, switchService
}

func LoadAPIMethods() {
	add(MethodStatus, "读取 vLLM Runtime 状态", nil, nil, func(context.Context, interface{}) (interface{}, error) { return supervisor().State(), nil })
	start := func(restart bool) func(context.Context, interface{}) (interface{}, error) {
		return func(ctx context.Context, input interface{}) (interface{}, error) {
			var in struct {
				Options   Options `json:"options"`
				HealthURL string  `json:"health_url,omitempty"`
			}
			if err := api_supported_methods.DecodeArguments(input, &in); err != nil {
				return nil, err
			}
			var err error
			if restart {
				err = supervisor().Restart(ctx, in.Options, in.HealthURL)
			} else {
				err = supervisor().Start(ctx, in.Options, in.HealthURL)
			}
			return supervisor().State(), err
		}
	}
	props := map[string]interface{}{"options": map[string]interface{}{"type": "object"}, "health_url": str()}
	add(MethodStart, "启动 vLLM Runtime", props, []string{"options"}, start(false))
	add(MethodRestart, "重启 vLLM Runtime", props, []string{"options"}, start(true))
	add(MethodSwitch, "切换活动模型", map[string]interface{}{"model_id": str(), "options": map[string]interface{}{"type": "object"}, "health_url": str()}, []string{"model_id", "options"}, func(ctx context.Context, input interface{}) (interface{}, error) {
		var in struct {
			ModelID   string  `json:"model_id"`
			Options   Options `json:"options"`
			HealthURL string  `json:"health_url,omitempty"`
		}
		if err := api_supported_methods.DecodeArguments(input, &in); err != nil {
			return nil, err
		}
		if currentSwitch == nil {
			return nil, errors.New("runtime switching unavailable")
		}
		if in.ModelID == "" {
			return nil, errors.New("model_id is required")
		}
		err := currentSwitch.Switch(ctx, in.ModelID, in.Options, in.HealthURL)
		return supervisor().State(), err
	})
	add(MethodStop, "停止 vLLM Runtime", nil, nil, func(ctx context.Context, _ interface{}) (interface{}, error) {
		err := supervisor().Stop(ctx)
		return map[string]bool{"stopped": err == nil}, err
	})
}

func supervisor() *Supervisor {
	if currentSupervisor == nil {
		panic(errors.New("runtime ability is not initialized"))
	}
	return currentSupervisor
}
func add(name, description string, properties map[string]interface{}, required []string, execute func(context.Context, interface{}) (interface{}, error)) {
	api_supported_methods.AddMethod(&api_supported_methods.SupportedMethod{Name: name, Description: description, Scope: "mcp.runtime", InputSchema: api_supported_methods.ObjectSchema(properties, required), Execute: execute})
}
func str() map[string]interface{} { return map[string]interface{}{"type": "string"} }
