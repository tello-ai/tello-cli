package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
)

// ObjectArg reads the JSON object given to flag (e.g. "--body"): the object
// itself, @path of a file holding one, or - for stdin. Numbers keep their
// exact text. Anything else is a usage error.
func (a *App) ObjectArg(flag, arg string) (map[string]any, error) {
	var raw []byte
	switch path, isFile := strings.CutPrefix(arg, "@"); {
	case arg == "-":
		data, err := io.ReadAll(a.Stdin)
		if err != nil {
			return nil, Usagef("%s: read stdin: %v", flag, err)
		}
		raw = data
	case isFile:
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, Usagef("%s: %v", flag, err)
		}
		raw = data
	default:
		raw = []byte(arg)
	}
	var obj map[string]any
	if err := DecodeJSON(raw, &obj); err != nil || obj == nil {
		return nil, Usagef("%s must be a JSON object, @file holding one, or - for stdin", flag)
	}
	return obj, nil
}

// DecodeJSON decodes exactly one JSON value, keeping numbers exact.
func DecodeJSON(raw []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return errors.New("unexpected data after the JSON value")
	}
	return nil
}
