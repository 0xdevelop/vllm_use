// Package modelid defines the canonical identity shared by model-backed domains.
package modelid

import (
	"encoding/hex"
	"errors"
)

const (
	Length  = 32
	Pattern = "^[0-9a-f]{32}$"
)

var ErrInvalid = errors.New("model ID must be 32 lowercase hexadecimal characters")

func Validate(id string) error {
	if len(id) != Length {
		return ErrInvalid
	}
	decoded, err := hex.DecodeString(id)
	if err != nil || len(decoded) != Length/2 || hex.EncodeToString(decoded) != id {
		return ErrInvalid
	}
	return nil
}

func Valid(id string) bool { return Validate(id) == nil }
