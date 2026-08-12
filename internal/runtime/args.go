package runtime

import (
	"errors"
	"strconv"
	"strings"
)

type ExtraArg struct {
	Name   string
	Values []string
}
type Options struct {
	Model, Host     string
	Port            int
	TensorParallel  int
	ServedModelName string
	ExtraArgs       []ExtraArg
}

func BuildArgs(o Options) ([]string, error) {
	if o.Model == "" {
		return nil, errors.New("model is required")
	}
	if o.Port < 1 || o.Port > 65535 {
		return nil, errors.New("invalid port")
	}
	if o.Host == "" {
		o.Host = "127.0.0.1"
	}
	a := []string{"serve", o.Model, "--host", o.Host, "--port", strconv.Itoa(o.Port)}
	if o.TensorParallel > 0 {
		a = append(a, "--tensor-parallel-size", strconv.Itoa(o.TensorParallel))
	}
	if o.ServedModelName != "" {
		a = append(a, "--served-model-name", o.ServedModelName)
	}
	reserved := map[string]bool{"model": true, "host": true, "port": true, "tensor-parallel-size": true, "served-model-name": true}
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
