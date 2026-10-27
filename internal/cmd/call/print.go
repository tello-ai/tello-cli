package call

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/tello-ai/tello-cli/internal/app"
	"github.com/tello-ai/tello-cli/internal/rest"
)

// callFields are the scalar call fields printed in human mode, in order.
var callFields = []string{
	"id", "status", "direction", "source", "agentId", "crewId",
	"createdAt", "startedAt", "endedAt", "durationSeconds", "endReason", "creditCharged",
}

// printCall writes a call object in human mode; summary and transcript are
// indented blocks after the scalar fields.
func printCall(a *app.App, call rest.Object) {
	for _, k := range callFields {
		a.Printf("%-16s %s\n", k+":", show(call[k]))
	}
	a.Printf("\nsummary:\n%s\n", indent(show(call["summary"])))
	a.Printf("\ntranscript:\n%s\n", indent(show(call["transcript"])))
}

func show(v any) string {
	if v == nil {
		return "-"
	}
	return fmt.Sprint(v)
}

func indent(s string) string {
	return "  " + strings.ReplaceAll(strings.TrimRight(s, "\n"), "\n", "\n  ")
}

// randomID is 128 random bits in hex, for idempotency keys.
func randomID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate id: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}
