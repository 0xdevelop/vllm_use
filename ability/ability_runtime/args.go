package ability_runtime

import (
	"errors"
	"math"
	"net"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	maxRuntimeModelBytes    = 4096
	maxRuntimeScalarBytes   = 256
	maxServedModelNameBytes = 512
	maxParallelSize         = 1024
	maxGPUDevices           = 64
	maxGPUDeviceIndex       = 1023
	maxModelLength          = 2_147_483_647
	maxExtraArgs            = 64
	maxExtraArgNameBytes    = 128
	maxExtraArgValues       = 64
	maxExtraArgValueBytes   = 4096
	maxExtraArgumentsBytes  = 64 << 10
)

type ExtraArg struct {
	Name   string   `json:"name"`
	Values []string `json:"values"`
}
type Options struct {
	Model                string     `json:"model"`
	Host                 string     `json:"host"`
	Port                 int        `json:"port"`
	TensorParallel       int        `json:"tensor_parallel"`
	PipelineParallelSize int        `json:"pipeline_parallel_size"`
	GPUDevices           []int      `json:"gpu_devices"`
	GPUMemoryUtilization float64    `json:"gpu_memory_utilization"`
	MaxModelLen          int        `json:"max_model_len"`
	DType                string     `json:"dtype"`
	Quantization         string     `json:"quantization"`
	TrustRemoteCode      bool       `json:"trust_remote_code"`
	ToolCallParser       string     `json:"tool_call_parser"`
	ReasoningParser      string     `json:"reasoning_parser"`
	EnableAutoToolChoice bool       `json:"enable_auto_tool_choice"`
	ServedModelName      string     `json:"served_model_name"`
	ExtraArgs            []ExtraArg `json:"extra_args"`
}

func BuildArgs(o Options) ([]string, error) {
	if err := validateBoundedArgument("model", o.Model, maxRuntimeModelBytes, false); err != nil {
		return nil, err
	}
	if o.Port < 1 || o.Port > 65535 {
		return nil, errors.New("invalid port")
	}
	if o.Host == "" {
		o.Host = "127.0.0.1"
	}
	ip := net.ParseIP(o.Host)
	if o.Host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return nil, errors.New("runtime host must be a loopback address")
	}
	if o.TensorParallel < 0 || o.TensorParallel > maxParallelSize || o.PipelineParallelSize < 0 || o.PipelineParallelSize > maxParallelSize {
		return nil, errors.New("parallel sizes must be between 0 and 1024")
	}
	a := []string{"serve", o.Model, "--host", o.Host, "--port", strconv.Itoa(o.Port)}
	if o.TensorParallel > 0 {
		a = append(a, "--tensor-parallel-size", strconv.Itoa(o.TensorParallel))
	}
	if o.PipelineParallelSize > 0 {
		a = append(a, "--pipeline-parallel-size", strconv.Itoa(o.PipelineParallelSize))
	}
	if len(o.GPUDevices) > maxGPUDevices {
		return nil, errors.New("gpu devices exceed 64 entries")
	}
	if len(o.GPUDevices) > 0 {
		seen := make(map[int]bool, len(o.GPUDevices))
		for _, device := range o.GPUDevices {
			if device < 0 || device > maxGPUDeviceIndex || seen[device] {
				return nil, errors.New("gpu devices must be unique indexes between 0 and 1023")
			}
			seen[device] = true
		}
	}
	if math.IsNaN(o.GPUMemoryUtilization) || math.IsInf(o.GPUMemoryUtilization, 0) || o.GPUMemoryUtilization < 0 || o.GPUMemoryUtilization > 1 {
		return nil, errors.New("gpu memory utilization must be between 0 and 1")
	}
	if o.GPUMemoryUtilization > 0 {
		a = append(a, "--gpu-memory-utilization", strconv.FormatFloat(o.GPUMemoryUtilization, 'g', -1, 64))
	}
	if o.MaxModelLen < 0 || o.MaxModelLen > maxModelLength {
		return nil, errors.New("max model length must be between 0 and 2147483647")
	}
	if o.MaxModelLen > 0 {
		a = append(a, "--max-model-len", strconv.Itoa(o.MaxModelLen))
	}
	for _, value := range []struct{ flag, value string }{{"dtype", o.DType}, {"quantization", o.Quantization}, {"tool-call-parser", o.ToolCallParser}, {"reasoning-parser", o.ReasoningParser}} {
		if value.value == "" {
			continue
		}
		if err := validateBoundedArgument(value.flag, value.value, maxRuntimeScalarBytes, true); err != nil {
			return nil, err
		}
		a = append(a, "--"+value.flag, value.value)
	}
	if o.TrustRemoteCode {
		a = append(a, "--trust-remote-code")
	}
	if o.EnableAutoToolChoice {
		a = append(a, "--enable-auto-tool-choice")
	}
	if o.ServedModelName != "" {
		if err := validateBoundedArgument("served model name", o.ServedModelName, maxServedModelNameBytes, false); err != nil {
			return nil, err
		}
		a = append(a, "--served-model-name", o.ServedModelName)
	}
	reserved := map[string]bool{"model": true, "host": true, "port": true, "tensor-parallel-size": true, "pipeline-parallel-size": true, "gpu-memory-utilization": true, "max-model-len": true, "dtype": true, "quantization": true, "trust-remote-code": true, "tool-call-parser": true, "reasoning-parser": true, "enable-auto-tool-choice": true, "served-model-name": true}
	if len(o.ExtraArgs) > maxExtraArgs {
		return nil, errors.New("extra arguments exceed 64 entries")
	}
	seenExtra := make(map[string]bool, len(o.ExtraArgs))
	extraBytes := 0
	for _, x := range o.ExtraArgs {
		n := strings.TrimPrefix(x.Name, "--")
		if len(n) > maxExtraArgNameBytes || !validExtraArgumentName(n) || reserved[n] || seenExtra[n] {
			return nil, errors.New("invalid or reserved extra argument: " + x.Name)
		}
		seenExtra[n] = true
		if len(x.Values) > maxExtraArgValues {
			return nil, errors.New("extra argument values exceed 64 entries")
		}
		extraBytes += len(n) + 2
		for _, value := range x.Values {
			if err := validateBoundedArgument("extra argument value", value, maxExtraArgValueBytes, false); err != nil {
				return nil, err
			}
			extraBytes += len(value)
			if extraBytes > maxExtraArgumentsBytes {
				return nil, errors.New("extra arguments exceed 64 KiB")
			}
		}
		a = append(a, "--"+n)
		a = append(a, x.Values...)
	}
	return a, nil
}

func validExtraArgumentName(name string) bool {
	if name == "" || strings.HasPrefix(name, "-") {
		return false
	}
	for i, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (i > 0 && r >= '0' && r <= '9') || (i > 0 && (r == '-' || r == '_')) {
			continue
		}
		return false
	}
	return true
}

func validateBoundedArgument(name, value string, maxBytes int, rejectWhitespace bool) error {
	if value == "" || len(value) > maxBytes || !utf8.ValidString(value) || strings.HasPrefix(value, "-") {
		return errors.New("invalid " + name)
	}
	for _, r := range value {
		if unicode.IsControl(r) || (rejectWhitespace && unicode.IsSpace(r)) {
			return errors.New("invalid " + name)
		}
	}
	return nil
}
