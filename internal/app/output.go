package app

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/tello-ai/tello-go/tello"
)

// WriteLine writes v as one JSON line to Stdout. Safe for concurrent use;
// UTF-8 and <, >, & are written unescaped.
func (a *App) WriteLine(v any) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return err
	}
	a.outMu.Lock()
	defer a.outMu.Unlock()
	_, err := a.Stdout.Write(buf.Bytes())
	return err
}

// WriteEvent writes a gateway frame verbatim as an NDJSON event line.
func (a *App) WriteEvent(event tello.Event) error {
	return a.WriteLine(event.Raw)
}

// WriteResult writes the success cli.result line in JSON mode. In human
// mode it writes nothing; commands print their own human output.
func (a *App) WriteResult(data any) error {
	if !a.JSON {
		return nil
	}
	if data == nil {
		data = map[string]any{}
	}
	return a.WriteLine(map[string]any{"type": "cli.result", "ok": true, "data": data})
}

// Printf writes human output to Stdout.
func (a *App) Printf(format string, args ...any) {
	a.outMu.Lock()
	defer a.outMu.Unlock()
	fmt.Fprintf(a.Stdout, format, args...)
}

// Warnf writes a warning to Stderr in both modes.
func (a *App) Warnf(format string, args ...any) {
	a.outMu.Lock()
	defer a.outMu.Unlock()
	fmt.Fprintf(a.Stderr, "tello: warning: "+format+"\n", args...)
}

// ErrorObject is the "error" member of a failed cli.result.
func ErrorObject(e *Error) map[string]any {
	obj := map[string]any{
		"kind":      string(e.Kind),
		"code":      e.Code,
		"message":   e.Message,
		"retryable": e.Retryable(),
		"exitCode":  e.ExitCode(),
	}
	if e.Question != "" {
		obj["question"] = e.Question
	}
	return obj
}

// ReportError reports err once and returns the process exit code. JSON
// mode writes the failed cli.result line to Stdout; human mode writes the
// message, code and a hint to Stderr.
func (a *App) ReportError(err error) int {
	e := Classify(err)
	if a.JSON {
		line := map[string]any{"type": "cli.result", "ok": false, "error": ErrorObject(e)}
		if e.Data != nil {
			line["data"] = e.Data
		}
		if werr := a.WriteLine(line); werr != nil {
			fmt.Fprintf(a.Stderr, "tello: %s\n", e.Message)
		}
		return e.ExitCode()
	}
	a.outMu.Lock()
	defer a.outMu.Unlock()
	fmt.Fprintf(a.Stderr, "tello: %s [%s]\n", e.Message, e.Code)
	if e.Question != "" {
		fmt.Fprintf(a.Stderr, "  question: %s\n", e.Question)
	}
	if hint, ok := hints[e.Code]; ok {
		fmt.Fprintf(a.Stderr, "  hint: %s\n", hint)
	}
	return e.ExitCode()
}
