// Package api_supported_methods stores protocol-neutral Ability definitions.
// It is the single method/schema registry shared by HTTP, MCP, and future adapters.
package api_supported_methods

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
)

type SupportedMethod struct {
	Name        string
	Description string
	InputSchema map[string]interface{}
	Async       bool
	Scope       string
	// Public is metadata for adapters that choose to expose unauthenticated
	// methods. Product management methods remain protected by their adapter.
	Public  bool
	Execute func(context.Context, interface{}) (interface{}, error)
}

var currentSupportedMethods []*SupportedMethod

func SupportedMethodsSetup() { currentSupportedMethods = nil }

func AddMethod(method *SupportedMethod) {
	if method == nil || method.Name == "" || method.Execute == nil {
		panic("supported API method requires name and execute function")
	}
	for _, currentMethod := range currentSupportedMethods {
		if currentMethod.Name == method.Name {
			panic("duplicate supported API method: " + method.Name)
		}
	}
	if method.InputSchema == nil {
		method.InputSchema = ObjectSchema(nil, nil)
	}
	currentSupportedMethods = append(currentSupportedMethods, method)
}

func Methods() []SupportedMethod {
	methods := make([]SupportedMethod, 0, len(currentSupportedMethods))
	for _, method := range currentSupportedMethods {
		methods = append(methods, *method)
	}
	return methods
}

func Method(name string) (*SupportedMethod, bool) {
	for _, method := range currentSupportedMethods {
		if method.Name == name {
			return method, true
		}
	}
	return nil, false
}

func ObjectSchema(properties map[string]interface{}, required []string) map[string]interface{} {
	if properties == nil {
		properties = map[string]interface{}{}
	}
	schema := map[string]interface{}{
		"type":                 "object",
		"properties":           properties,
		"additionalProperties": false,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

// DecodeArguments converts protocol-neutral map arguments into a typed domain
// request while rejecting unknown fields, preserving the HTTP JSON contract.
func DecodeArguments(input interface{}, output interface{}) error {
	if input == nil {
		input = map[string]interface{}{}
	}
	payload, err := json.Marshal(input)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(output); err != nil {
		return err
	}
	if decoder.Decode(&struct{}{}) == nil {
		return errors.New("multiple argument values")
	}
	return nil
}
