// Package processenv builds least-privilege environments for host child processes.
package processenv

import "strings"

var managerCredentialKeys = map[string]struct{}{
	"VLLM_USE_ADMIN_TOKEN":      {},
	"VLLM_USE_UPSTREAM_API_KEY": {},
}

// WithoutManagerCredentials copies env while removing credentials that belong
// only to the vllm-use management process. Callers may add a purpose-specific
// child credential (for example HF_TOKEN) after this boundary.
func WithoutManagerCredentials(env []string) []string {
	out := make([]string, 0, len(env))
	for _, item := range env {
		key, _, ok := strings.Cut(item, "=")
		if ok {
			if _, sensitive := managerCredentialKeys[key]; sensitive {
				continue
			}
		}
		out = append(out, item)
	}
	return out
}
