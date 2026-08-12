package runtime

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
	Model           string     `json:"model"`
	Host            string     `json:"host"`
	Port            int        `json:"port"`
	TensorParallel  int        `json:"tensor_parallel"`
	ServedModelName string     `json:"served_model_name"`
	ExtraArgs       []ExtraArg `json:"extra_args"`
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
