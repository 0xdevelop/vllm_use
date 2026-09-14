package modelid

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateCanonicalModelID(t *testing.T) {
	valid := strings.Repeat("0a", Length/2)
	if err := Validate(valid); err != nil {
		t.Fatalf("canonical model ID rejected: %v", err)
	}
	for _, invalid := range []string{
		"",
		strings.Repeat("a", Length-1),
		strings.Repeat("A", Length),
		strings.Repeat("g", Length),
		"../../outside-quarantine",
	} {
		if err := Validate(invalid); !errors.Is(err, ErrInvalid) {
			t.Fatalf("Validate(%q) = %v, want ErrInvalid", invalid, err)
		}
	}
}
