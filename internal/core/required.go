package core

import (
	"fmt"
	"reflect"
	"regexp"
	"time"
)

// CheckRequired returns an error wrapping ErrInvalidInput for the first field
// tagged vngcloud:"required" that holds its zero value. A nil pointer counts
// as all fields empty. in must be a pointer to a struct.
func CheckRequired(op string, in any) error {
	v := reflect.ValueOf(in)
	t := v.Type().Elem()
	for i := range t.NumField() {
		f := t.Field(i)
		if f.Tag.Get("vngcloud") != "required" {
			continue
		}
		if v.IsNil() || v.Elem().Field(i).IsZero() {
			return fmt.Errorf("%w: %s requires %s", ErrInvalidInput, op, f.Name)
		}
	}
	return nil
}

var pathIDPattern = regexp.MustCompile("^[A-Za-z0-9-]+$")

// CheckPathID returns an error wrapping ErrInvalidInput when value does not
// match ^[A-Za-z0-9-]+$. routes.URL escapes "/" in a path segment but not
// "." or "..", so this check keeps a write from reaching a different path
// than the caller named.
func CheckPathID(op, field, value string) error {
	if pathIDPattern.MatchString(value) {
		return nil
	}
	return fmt.Errorf("%w: %s requires %s to match %s, got %q",
		ErrInvalidInput, op, field, pathIDPattern.String(), truncateForError(value))
}

// CheckDate returns an error wrapping ErrInvalidInput when value is not a
// valid YYYY-MM-DD date, the only format the API's date fields take.
func CheckDate(op, field, value string) error {
	if _, err := time.Parse("2006-01-02", value); err != nil {
		return fmt.Errorf("%w: %s requires %s to be a YYYY-MM-DD date, got %q",
			ErrInvalidInput, op, field, truncateForError(value))
	}
	return nil
}

// truncateForError caps an echoed input value at 64 bytes, so an error
// message never repeats an oversized or abusive value back to the caller.
func truncateForError(value string) string {
	const maxEchoLen = 64
	if len(value) <= maxEchoLen {
		return value
	}
	return value[:maxEchoLen]
}
