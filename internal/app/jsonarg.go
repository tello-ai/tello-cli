package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
)

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
