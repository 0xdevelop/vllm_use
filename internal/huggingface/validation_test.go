package huggingface

import (
	"strings"
	"testing"
)

func TestNormalizeRepository(t *testing.T) {
	for _, input := range []string{
		"org/model",
		"org_name/model.v2",
		"  org/model  ",
	} {
		got, err := NormalizeRepository(input)
		if err != nil {
			t.Fatalf("NormalizeRepository(%q): %v", input, err)
		}
		if got != strings.TrimSpace(input) {
			t.Fatalf("NormalizeRepository(%q) = %q", input, got)
		}
	}

	for _, input := range []string{
		"model",
		"org/",
		"/model",
		"org/model/extra",
		"-org/model",
		"org/model-",
		"org/.model",
		"org/model.git",
		"org/mo..del",
		"org/mo--del",
		"组织/model",
		"org/model name",
		strings.Repeat("a", MaxRepositoryBytes-5) + "/model",
	} {
		if _, err := NormalizeRepository(input); err == nil {
			t.Fatalf("NormalizeRepository(%q) unexpectedly succeeded", input)
		}
	}
}

func TestNormalizeRevision(t *testing.T) {
	for _, input := range []string{"", "main", "refs/pr/2", "  v1.2.3  "} {
		got, err := NormalizeRevision(input)
		if err != nil {
			t.Fatalf("NormalizeRevision(%q): %v", input, err)
		}
		if got != strings.TrimSpace(input) {
			t.Fatalf("NormalizeRevision(%q) = %q", input, got)
		}
	}

	for _, input := range []string{
		"-revision",
		"feature branch",
		"feature\tbranch",
		"line\nbreak",
		strings.Repeat("r", MaxRevisionBytes+1),
	} {
		if _, err := NormalizeRevision(input); err == nil {
			t.Fatalf("NormalizeRevision(%q) unexpectedly succeeded", input)
		}
	}
}
