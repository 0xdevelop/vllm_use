package ability_runtime

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestBuildArgs(t *testing.T) {
	got, e := BuildArgs(Options{Model: "m", Host: "127.0.0.1", Port: 8000, TensorParallel: 2, PipelineParallelSize: 3, GPUDevices: []int{0, 2}, GPUMemoryUtilization: .85, MaxModelLen: 8192, DType: "float16", Quantization: "awq", TrustRemoteCode: true, ToolCallParser: "hermes", ReasoningParser: "deepseek_r1", EnableAutoToolChoice: true, ServedModelName: "served", ExtraArgs: []ExtraArg{{Name: "max-num-seqs", Values: []string{"16"}}}})
	if e != nil {
		t.Fatal(e)
	}
	want := []string{"serve", "m", "--host", "127.0.0.1", "--port", "8000", "--tensor-parallel-size", "2", "--pipeline-parallel-size", "3", "--gpu-memory-utilization", "0.85", "--max-model-len", "8192", "--dtype", "float16", "--quantization", "awq", "--tool-call-parser", "hermes", "--reasoning-parser", "deepseek_r1", "--trust-remote-code", "--enable-auto-tool-choice", "--served-model-name", "served", "--max-num-seqs", "16"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%q", got)
	}
	if _, e = BuildArgs(Options{Model: "m", Port: 1, ExtraArgs: []ExtraArg{{Name: "port", Values: []string{"2"}}}}); e == nil {
		t.Fatal("reserved arg accepted")
	}
}

func TestOptionsJSONContract(t *testing.T) {
	raw := `{"model":"m","host":"localhost","port":8000,"tensor_parallel":2,"pipeline_parallel_size":3,"gpu_devices":[0,1],"gpu_memory_utilization":0.9,"max_model_len":4096,"dtype":"auto","quantization":"awq","trust_remote_code":true,"tool_call_parser":"hermes","reasoning_parser":"deepseek_r1","enable_auto_tool_choice":true,"served_model_name":"public","extra_args":[{"name":"disable-log-requests","values":[]}]}`
	var got Options
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatal(err)
	}
	out, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != raw {
		t.Fatalf("JSON contract mismatch:\n got %s\nwant %s", out, raw)
	}
}

func TestBuildArgsValidationAndReservedFlags(t *testing.T) {
	invalid := []Options{
		{Model: "m", Port: 1, PipelineParallelSize: -1},
		{Model: "m", Port: 1, GPUDevices: []int{0, 0}},
		{Model: "m", Port: 1, GPUMemoryUtilization: 1.01},
		{Model: "m", Port: 1, MaxModelLen: -1},
		{Model: "m", Port: 1, DType: "--bad"},
		{Model: "m", Port: 1, ServedModelName: "--port"},
		{Model: "m", Port: 1, ServedModelName: "bad\nname"},
		{Model: "m", Port: 1, ExtraArgs: []ExtraArg{{Name: "---port", Values: []string{"2"}}}},
		{Model: "m", Port: 1, ExtraArgs: []ExtraArg{{Name: "max.num.seqs", Values: []string{"2"}}}},
		{Model: "m", Port: 1, ExtraArgs: []ExtraArg{{Name: "max-num-seqs", Values: []string{"--port", "2"}}}},
		{Model: "m", Port: 1, ExtraArgs: []ExtraArg{{Name: "chat-template", Values: []string{"bad\x00value"}}}},
	}
	for _, options := range invalid {
		if _, err := BuildArgs(options); err == nil {
			t.Fatalf("accepted %#v", options)
		}
	}
	for _, name := range []string{"model", "host", "port", "tensor-parallel-size", "pipeline-parallel-size", "gpu-memory-utilization", "max-model-len", "dtype", "quantization", "trust-remote-code", "tool-call-parser", "reasoning-parser", "enable-auto-tool-choice", "served-model-name"} {
		if _, err := BuildArgs(Options{Model: "m", Port: 1, ExtraArgs: []ExtraArg{{Name: name}}}); err == nil {
			t.Fatalf("reserved flag %q accepted", name)
		}
	}
}
