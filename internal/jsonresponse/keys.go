// Package jsonresponse validates JSON before response fields are decoded.
package jsonresponse

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
)

// Validate rejects malformed JSON and duplicate object keys at any depth.
func Validate(raw []byte) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if err := uniqueKeys(d); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return errors.New("invalid JSON response")
	}
	return nil
}

func uniqueKeys(d *json.Decoder) error {
	token, err := d.Token()
	if err != nil {
		return errors.New("invalid JSON response")
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		keys := make(map[string]bool)
		for d.More() {
			token, err := d.Token()
			if err != nil {
				return errors.New("invalid JSON response")
			}
			key, ok := token.(string)
			if !ok || keys[key] {
				return errors.New("duplicate or invalid JSON key")
			}
			keys[key] = true
			if err := uniqueKeys(d); err != nil {
				return err
			}
		}
	case '[':
		for d.More() {
			if err := uniqueKeys(d); err != nil {
				return err
			}
		}
	default:
		return errors.New("invalid JSON response")
	}
	if _, err := d.Token(); err != nil {
		return errors.New("invalid JSON response")
	}
	return nil
}
