// Package huggingface defines the input boundary shared by model registration
// and host-side Hugging Face downloads.
package huggingface

import (
	"errors"
	"strings"
	"unicode"
)

const (
	// MaxRepositoryBytes follows the Hugging Face Hub repository ID limit.
	MaxRepositoryBytes = 96
	// MaxRevisionBytes bounds branch, tag, or commit references before they are
	// persisted and passed to the host CLI.
	MaxRevisionBytes = 255
)

// NormalizeRepository trims cosmetic outer whitespace and validates the
// owner/name form accepted by the Hugging Face Hub. Keeping this check shared
// prevents the registry from accepting a model that the downloader must later
// reject.
func NormalizeRepository(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > MaxRepositoryBytes {
		return "", errors.New("invalid repository; expected a Hugging Face owner/name up to 96 bytes")
	}
	parts := strings.Split(value, "/")
	if len(parts) != 2 || !validRepositoryPart(parts[0]) || !validRepositoryPart(parts[1]) || strings.HasSuffix(value, ".git") {
		return "", errors.New("invalid repository; expected a Hugging Face owner/name")
	}
	return value, nil
}

func validRepositoryPart(value string) bool {
	if value == "" || value[0] == '-' || value[0] == '.' || value[len(value)-1] == '-' || value[len(value)-1] == '.' || strings.Contains(value, "--") || strings.Contains(value, "..") {
		return false
	}
	for i := 0; i < len(value); i++ {
		c := value[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_' || c == '.' {
			continue
		}
		return false
	}
	return true
}

// NormalizeRevision validates an optional branch, tag, or commit reference.
// Arguments are passed without a shell, but leading dashes and whitespace are
// still rejected so they cannot be interpreted inconsistently by CLI versions.
func NormalizeRevision(value string) (string, error) {
	value = strings.TrimSpace(value)
	if len(value) > MaxRevisionBytes || strings.HasPrefix(value, "-") {
		return "", errors.New("invalid Hugging Face revision")
	}
	for _, r := range value {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return "", errors.New("invalid Hugging Face revision")
		}
	}
	return value, nil
}
