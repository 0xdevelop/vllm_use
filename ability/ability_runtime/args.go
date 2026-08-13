package ability_runtime

import (
	"errors"
	"net"
	"strconv"
	"strings"
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
	if o.Model == "" || strings.HasPrefix(o.Model, "-") || strings.ContainsAny(o.Model, "\x00\n\r") {
		return nil, errors.New("model is required")
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
	a := []string{"serve", o.Model, "--host", o.Host, "--port", strconv.Itoa(o.Port)}
	if o.TensorParallel > 0 {
		a = append(a, "--tensor-parallel-size", strconv.Itoa(o.TensorParallel))
	}
	if o.TensorParallel < 0 || o.PipelineParallelSize < 0 {
		return nil, errors.New("parallel sizes must not be negative")
	}
	if o.PipelineParallelSize > 0 {
		a = append(a, "--pipeline-parallel-size", strconv.Itoa(o.PipelineParallelSize))
	}
	if len(o.GPUDevices) > 0 {
		seen := make(map[int]bool, len(o.GPUDevices))
		for _, device := range o.GPUDevices {
			if device < 0 || seen[device] {
				return nil, errors.New("gpu devices must be unique non-negative indexes")
			}
			seen[device] = true
		}
	}
	if o.GPUMemoryUtilization < 0 || o.GPUMemoryUtilization > 1 {
		return nil, errors.New("gpu memory utilization must be between 0 and 1")
	}
	if o.GPUMemoryUtilization > 0 {
		a = append(a, "--gpu-memory-utilization", strconv.FormatFloat(o.GPUMemoryUtilization, 'g', -1, 64))
	}
	if o.MaxModelLen < 0 {
		return nil, errors.New("max model length must not be negative")
	}
	if o.MaxModelLen > 0 {
		a = append(a, "--max-model-len", strconv.Itoa(o.MaxModelLen))
	}
	for _, value := range []struct{ flag, value string }{{"dtype", o.DType}, {"quantization", o.Quantization}, {"tool-call-parser", o.ToolCallParser}, {"reasoning-parser", o.ReasoningParser}} {
		if value.value == "" {
			continue
		}
		if strings.HasPrefix(value.value, "-") || strings.ContainsAny(value.value, "\x00\n\r\t ") {
			return nil, errors.New("invalid " + value.flag)
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
		a = append(a, "--served-model-name", o.ServedModelName)
	}
	reserved := map[string]bool{"model": true, "host": true, "port": true, "tensor-parallel-size": true, "pipeline-parallel-size": true, "gpu-memory-utilization": true, "max-model-len": true, "dtype": true, "quantization": true, "trust-remote-code": true, "tool-call-parser": true, "reasoning-parser": true, "enable-auto-tool-choice": true, "served-model-name": true}
	for _, x := range o.ExtraArgs {
		n := strings.TrimPrefix(x.Name, "--")
		if n == "" || strings.ContainsAny(n, " =\t\r\n") || reserved[n] {
			return nil, errors.New("invalid or reserved extra argument: " + x.Name)
		}
		a = append(a, "--"+n)
		a = append(a, x.Values...)
	}
	return a, nil
}
