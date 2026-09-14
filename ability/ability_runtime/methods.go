package ability_runtime

import (
	"context"
	"errors"

	"github.com/0xdevelop/vllm-use/api/api_supported_methods"
	"github.com/0xdevelop/vllm-use/internal/modelid"
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
	add(MethodStatus, "读取 vLLM Runtime 状态", nil, nil, func(context.Context, interface{}) (interface{}, error) { return runtimeState(), nil })
	start := func(restart bool) func(context.Context, interface{}) (interface{}, error) {
		return func(ctx context.Context, input interface{}) (interface{}, error) {
			var in struct {
				Options Options `json:"options"`
			}
			if err := api_supported_methods.DecodeArguments(input, &in); err != nil {
				return nil, err
			}
			var err error
			if currentSwitch != nil && restart {
				err = currentSwitch.Restart(ctx, in.Options)
			} else if currentSwitch != nil {
				err = currentSwitch.Start(ctx, in.Options)
			} else if restart {
				err = supervisor().Restart(ctx, in.Options)
			} else {
				err = supervisor().Start(ctx, in.Options)
			}
			return runtimeState(), err
		}
	}
	directOptions := runtimeOptionsSchema(true)
	add(MethodStart, "启动 vLLM Runtime", map[string]interface{}{"options": directOptions}, []string{"options"}, start(false))
	add(MethodRestart, "重启 vLLM Runtime", map[string]interface{}{"options": directOptions}, []string{"options"}, start(true))
	add(MethodSwitch, "切换活动模型", map[string]interface{}{"model_id": modelIDSchema(), "options": runtimeOptionsSchema(false)}, []string{"model_id", "options"}, func(ctx context.Context, input interface{}) (interface{}, error) {
		var in struct {
			ModelID string  `json:"model_id"`
			Options Options `json:"options"`
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
		err := currentSwitch.Switch(ctx, in.ModelID, in.Options)
		return runtimeState(), err
	})
	add(MethodStop, "停止 vLLM Runtime", nil, nil, func(ctx context.Context, _ interface{}) (interface{}, error) {
		var err error
		if currentSwitch != nil {
			err = currentSwitch.Stop(ctx)
		} else {
			err = supervisor().Stop(ctx)
		}
		return map[string]bool{"stopped": err == nil}, err
	})
}

func supervisor() *Supervisor {
	if currentSupervisor == nil {
		panic(errors.New("runtime ability is not initialized"))
	}
	return currentSupervisor
}
func runtimeState() State {
	state := supervisor().State()
	if currentSwitch != nil {
		state.ActiveModelID = currentSwitch.Active()
	}
	return state
}
func add(name, description string, properties map[string]interface{}, required []string, execute func(context.Context, interface{}) (interface{}, error)) {
	api_supported_methods.AddMethod(&api_supported_methods.SupportedMethod{Name: name, Description: description, Scope: "mcp.runtime", InputSchema: api_supported_methods.ObjectSchema(properties, required), Execute: execute})
}
func modelIDSchema() map[string]interface{} {
	return map[string]interface{}{"type": "string", "minLength": modelid.Length, "maxLength": modelid.Length, "pattern": modelid.Pattern}
}

func runtimeOptionsSchema(requireModel bool) map[string]interface{} {
	properties := map[string]interface{}{
		"model":                   map[string]interface{}{"type": "string", "minLength": 1, "maxLength": maxRuntimeModelBytes},
		"host":                    map[string]interface{}{"type": "string", "maxLength": 45},
		"port":                    map[string]interface{}{"type": "integer", "minimum": 1, "maximum": 65535},
		"tensor_parallel":         map[string]interface{}{"type": "integer", "minimum": 0, "maximum": maxParallelSize},
		"pipeline_parallel_size":  map[string]interface{}{"type": "integer", "minimum": 0, "maximum": maxParallelSize},
		"gpu_devices":             map[string]interface{}{"type": "array", "maxItems": maxGPUDevices, "uniqueItems": true, "items": map[string]interface{}{"type": "integer", "minimum": 0, "maximum": maxGPUDeviceIndex}},
		"gpu_memory_utilization":  map[string]interface{}{"type": "number", "minimum": 0, "maximum": 1},
		"max_model_len":           map[string]interface{}{"type": "integer", "minimum": 0, "maximum": maxModelLength},
		"dtype":                   map[string]interface{}{"type": "string", "maxLength": maxRuntimeScalarBytes},
		"quantization":            map[string]interface{}{"type": "string", "maxLength": maxRuntimeScalarBytes},
		"trust_remote_code":       map[string]interface{}{"type": "boolean"},
		"tool_call_parser":        map[string]interface{}{"type": "string", "maxLength": maxRuntimeScalarBytes},
		"reasoning_parser":        map[string]interface{}{"type": "string", "maxLength": maxRuntimeScalarBytes},
		"enable_auto_tool_choice": map[string]interface{}{"type": "boolean"},
		"served_model_name":       map[string]interface{}{"type": "string", "maxLength": maxServedModelNameBytes},
		"extra_args": map[string]interface{}{
			"type": "array", "maxItems": maxExtraArgs,
			"items": api_supported_methods.ObjectSchema(map[string]interface{}{
				"name":   map[string]interface{}{"type": "string", "minLength": 1, "maxLength": maxExtraArgNameBytes},
				"values": map[string]interface{}{"type": "array", "maxItems": maxExtraArgValues, "items": map[string]interface{}{"type": "string", "minLength": 1, "maxLength": maxExtraArgValueBytes}},
			}, []string{"name"}),
		},
	}
	required := []string{"port"}
	if requireModel {
		required = append([]string{"model"}, required...)
	}
	return api_supported_methods.ObjectSchema(properties, required)
}
