package api_executer

import (
	"context"
	"errors"
	"fmt"

	"github.com/0xdevelop/vllm-use/api/api_supported_methods"
	"github.com/george012/gtbox/gtbox_log"
)

var (
	ErrMethodNotFound   = errors.New("ability method not found")
	ErrInvalidArguments = errors.New("invalid ability arguments")
	ErrInvalidCall      = errors.New("invalid tools/call request")
)

const ToolsCallMethod = "tools/call"

// ExecuteAbility is the protocol-neutral business execution entry used by all
// authenticated adapters. Authentication and transport concerns stay outside.
func ExecuteAbility(ctx context.Context, methodName string, arguments interface{}) (interface{}, error) {
	supportedMethod, ok := api_supported_methods.Method(methodName)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrMethodNotFound, methodName)
	}
	if arguments == nil {
		arguments = map[string]interface{}{}
	}
	gtbox_log.LogInfof("Ability method=[%s]", methodName)
	return supportedMethod.Execute(ctx, arguments)
}

func APIExecuter(ctx context.Context, method string, params interface{}, encryptionKey string) (*CallToolResult, error) {
	methodName, arguments, err := extractCall(method, params)
	if err != nil {
		return nil, err
	}
	value, executeErr := ExecuteAbility(ctx, methodName, arguments)
	return finish(value, executeErr, encryptionKey)
}

func extractCall(method string, protocolParams interface{}) (string, interface{}, error) {
	if method != ToolsCallMethod {
		return "", nil, fmt.Errorf("%w: only tools/call is supported", ErrInvalidCall)
	}
	callParams, ok := protocolParams.(map[string]interface{})
	if !ok {
		return "", nil, fmt.Errorf("%w: tools/call params must be an object", ErrInvalidCall)
	}
	methodName, ok := callParams["name"].(string)
	if !ok || methodName == "" {
		return "", nil, fmt.Errorf("%w: tools/call params.name is required", ErrInvalidCall)
	}
	arguments, ok := callParams["arguments"]
	if !ok {
		return "", nil, fmt.Errorf("%w: tools/call params.arguments is required", ErrInvalidCall)
	}
	if _, ok = arguments.(map[string]interface{}); !ok {
		return "", nil, fmt.Errorf("%w: arguments must be an object", ErrInvalidArguments)
	}
	return methodName, arguments, nil
}
