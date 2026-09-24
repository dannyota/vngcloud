package core

import (
	"fmt"
	"reflect"
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
