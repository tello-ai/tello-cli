package app

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"unicode"

	"github.com/spf13/cobra"
)

// RandomID is 128 random bits in hex, e.g. for idempotency keys.
func RandomID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate id: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// CheckIdempotencyKey refuses a given --idempotency-key the API rejects
// (empty, over 255 bytes, a comma) or that cannot be sent as a header
// (control characters). Without the flag a random key is used.
func CheckIdempotencyKey(cmd *cobra.Command, key string) error {
	if !cmd.Flags().Changed("idempotency-key") {
		return nil
	}
	if key == "" || len(key) > 255 || strings.ContainsRune(key, ',') || strings.ContainsFunc(key, unicode.IsControl) {
		return Usagef("--idempotency-key must be 1 to 255 bytes without commas or control characters")
	}
	return nil
}

// WithIdempotencyKey classifies err and adds the key the request was sent
// with to its data, so the caller can resend the same request.
func WithIdempotencyKey(err error, key string) *Error {
	e := Classify(err)
	data, _ := e.Data.(map[string]any)
	if data == nil {
		data = map[string]any{}
	}
	data["idempotencyKey"] = key
	e.Data = data
	return e
}
