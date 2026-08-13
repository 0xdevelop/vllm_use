package ability_gpu

import (
	"context"
	"encoding/csv"
	"errors"
	"os/exec"
	"strconv"
	"strings"

	"github.com/0xdevelop/vllm-use/api/api_supported_methods"
)

const MethodList = "gpu.list"

var currentService *NVIDIA

func Setup(service *NVIDIA) { currentService = service }

func LoadAPIMethods() {
	api_supported_methods.AddMethod(&api_supported_methods.SupportedMethod{
		Name:        MethodList,
		Description: "列出 NVIDIA GPU 状态",
		Scope:       "mcp.read",
		InputSchema: map[string]interface{}{
			"type":                 "object",
			"properties":           map[string]interface{}{},
			"additionalProperties": false,
		},
		Execute: func(ctx context.Context, _ interface{}) (interface{}, error) {
			if currentService == nil {
				return nil, errors.New("GPU ability is not initialized")
			}
			return currentService.List(ctx)
		},
	})
}

type GPU struct {
	Index          int    `json:"index"`
	Name           string `json:"name"`
	UUID           string `json:"uuid"`
	MemoryTotalMiB int    `json:"memory_total_mib"`
	MemoryUsedMiB  int    `json:"memory_used_mib"`
}
type Runner interface {
	Output(context.Context, string, ...string) ([]byte, error)
}
type runner struct{}

func (runner) Output(c context.Context, n string, a ...string) ([]byte, error) {
	return exec.CommandContext(c, n, a...).Output()
}

type NVIDIA struct{ r Runner }

func New(r Runner) *NVIDIA {
	if r == nil {
		r = runner{}
	}
	return &NVIDIA{r}
}
func (n *NVIDIA) List(ctx context.Context) ([]GPU, error) {
	b, e := n.r.Output(ctx, "nvidia-smi", "--query-gpu=index,name,uuid,memory.total,memory.used", "--format=csv,noheader,nounits")
	if e != nil {
		if errors.Is(e, exec.ErrNotFound) {
			return []GPU{}, nil
		}
		return []GPU{}, nil
	}
	rows, e := csv.NewReader(strings.NewReader(string(b))).ReadAll()
	if e != nil {
		return nil, e
	}
	out := make([]GPU, 0, len(rows))
	for _, r := range rows {
		if len(r) != 5 {
			return nil, errors.New("unexpected nvidia-smi output")
		}
		i, e := strconv.Atoi(strings.TrimSpace(r[0]))
		if e != nil {
			return nil, e
		}
		total, e := strconv.Atoi(strings.TrimSpace(r[3]))
		if e != nil {
			return nil, e
		}
		used, e := strconv.Atoi(strings.TrimSpace(r[4]))
		if e != nil {
			return nil, e
		}
		out = append(out, GPU{i, strings.TrimSpace(r[1]), strings.TrimSpace(r[2]), total, used})
	}
	return out, nil
}
