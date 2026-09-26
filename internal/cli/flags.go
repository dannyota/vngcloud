package cli

import (
	"fmt"
	"reflect"

	"github.com/spf13/cobra"
)

// globalFlagNames names every flag every operation command already carries
// from the root's persistent flags, plus --cli-input-json, which op.go adds
// to every operation command itself. An Input field must never derive one of
// these as its own flag name, or the two would collide.
var globalFlagNames = map[string]bool{
	"profile":        true,
	"region":         true,
	"project-id":     true,
	"output":         true,
	"query":          true,
	"yes":            true,
	"debug":          true,
	"read-only":      true,
	"cli-input-json": true,
	"help":           true,
}

// flagSpec describes one Input field that becomes a flag: its index in the
// struct, its flag name, and the primitive kind flags.go knows how to bind,
// which is the field's own kind for a plain field or the pointed-to kind for
// a pointer field.
type flagSpec struct {
	fieldIndex int
	fieldName  string
	flagName   string
	kind       reflect.Kind
	isPointer  bool
}

// flagSpecsFor reflects over the struct inputPtr points to and returns one
// flagSpec per exported field of a supported type: string, int, int64,
// float64, bool, or a pointer to one of those. Every other field type (a
// map, for example) is skipped; it is only ever set through
// --cli-input-json.
func flagSpecsFor(inputPtr any) ([]flagSpec, error) {
	v := reflect.ValueOf(inputPtr)
	if v.Kind() != reflect.Pointer || v.IsNil() || v.Elem().Kind() != reflect.Struct {
		return nil, fmt.Errorf("cli: Input must be a non-nil pointer to a struct, got %T", inputPtr)
	}
	t := v.Elem().Type()
	specs := make([]flagSpec, 0, t.NumField())
	for i := range t.NumField() {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		kind, isPointer, ok := supportedFieldKind(f.Type)
		if !ok {
			continue
		}
		specs = append(specs, flagSpec{
			fieldIndex: i,
			fieldName:  f.Name,
			flagName:   flagNameFor(f.Name),
			kind:       kind,
			isPointer:  isPointer,
		})
	}
	return specs, nil
}

// inputFieldNames returns the set of inputPtr's exported struct field names,
// by their Go name. validateOps (op.go) checks every NoFlag name against
// this set, so a typo in NoFlag("Regoin") is rejected at registration
// instead of silently marking nothing.
func inputFieldNames(inputPtr any) (map[string]bool, error) {
	v := reflect.ValueOf(inputPtr)
	if v.Kind() != reflect.Pointer || v.IsNil() || v.Elem().Kind() != reflect.Struct {
		return nil, fmt.Errorf("cli: Input must be a non-nil pointer to a struct, got %T", inputPtr)
	}
	t := v.Elem().Type()
	names := make(map[string]bool, t.NumField())
	for i := range t.NumField() {
		if f := t.Field(i); f.IsExported() {
			names[f.Name] = true
		}
	}
	return names, nil
}

// withoutNoFlag returns specs with every entry named in noFlag removed, so
// the caller never registers a flag, or checks it for a global-flag
// collision, for an Input field NoFlag (op.go) marks: it stays settable only
// through --cli-input-json. It returns specs unchanged, not a copy, when
// noFlag is empty, which most Read ops (every one but project's
// list-projects, in this design) take.
func withoutNoFlag(specs []flagSpec, noFlag map[string]bool) []flagSpec {
	if len(noFlag) == 0 {
		return specs
	}
	kept := make([]flagSpec, 0, len(specs))
	for _, s := range specs {
		if !noFlag[s.fieldName] {
			kept = append(kept, s)
		}
	}
	return kept
}

// supportedFieldKind reports the primitive kind flags.go binds for t: t's own
// kind for string, int, int64, float64, or bool, or the pointed-to kind for a
// pointer to one of those. ok is false for any other type.
func supportedFieldKind(t reflect.Type) (kind reflect.Kind, isPointer bool, ok bool) {
	switch t.Kind() {
	case reflect.String, reflect.Int, reflect.Int64, reflect.Float64, reflect.Bool:
		return t.Kind(), false, true
	case reflect.Pointer:
		switch t.Elem().Kind() {
		case reflect.String, reflect.Int, reflect.Int64, reflect.Float64, reflect.Bool:
			return t.Elem().Kind(), true, true
		}
	}
	return 0, false, false
}

// boundFlag pairs a flagSpec with the plain local variable cobra parses the
// flag's value into. A local variable is used even for a non-pointer field,
// rather than binding the struct field directly, because the merge in
// input.go must know whether the user actually gave the flag (Changed)
// before overwriting whatever --cli-input-json already set.
type boundFlag struct {
	spec  flagSpec
	strv  *string
	intv  *int
	i64v  *int64
	f64v  *float64
	boolv *bool
}

// registerFlags adds one flag per spec to cmd's flag set, bound to a fresh
// local variable, and returns the bindings for applyChangedFlags to read
// after parsing.
func registerFlags(cmd *cobra.Command, specs []flagSpec) []boundFlag {
	bound := make([]boundFlag, len(specs))
	for i, spec := range specs {
		b := boundFlag{spec: spec}
		switch spec.kind {
		case reflect.String:
			b.strv = new(string)
			cmd.Flags().StringVar(b.strv, spec.flagName, "", "")
		case reflect.Int:
			b.intv = new(int)
			cmd.Flags().IntVar(b.intv, spec.flagName, 0, "")
		case reflect.Int64:
			b.i64v = new(int64)
			cmd.Flags().Int64Var(b.i64v, spec.flagName, 0, "")
		case reflect.Float64:
			b.f64v = new(float64)
			cmd.Flags().Float64Var(b.f64v, spec.flagName, 0, "")
		case reflect.Bool:
			b.boolv = new(bool)
			cmd.Flags().BoolVar(b.boolv, spec.flagName, false, "")
		}
		bound[i] = b
	}
	return bound
}

// applyChangedFlags writes every flag the user actually set (cobra reports
// it Changed) into target, which must be the same Input pointer the bound
// flags' specs were computed from. A flag left at its default is never
// applied, so it never overwrites a value --cli-input-json already set.
func applyChangedFlags(cmd *cobra.Command, target any, bound []boundFlag) {
	v := reflect.ValueOf(target).Elem()
	for _, b := range bound {
		if !cmd.Flags().Changed(b.spec.flagName) {
			continue
		}
		field := v.Field(b.spec.fieldIndex)
		switch b.spec.kind {
		case reflect.String:
			setFieldValue(field, b.spec.isPointer, reflect.ValueOf(*b.strv))
		case reflect.Int:
			setFieldValue(field, b.spec.isPointer, reflect.ValueOf(*b.intv))
		case reflect.Int64:
			setFieldValue(field, b.spec.isPointer, reflect.ValueOf(*b.i64v))
		case reflect.Float64:
			setFieldValue(field, b.spec.isPointer, reflect.ValueOf(*b.f64v))
		case reflect.Bool:
			setFieldValue(field, b.spec.isPointer, reflect.ValueOf(*b.boolv))
		}
	}
}

// setFieldValue sets field to value directly, or, when the Input field is a
// pointer, to a fresh pointer holding value, exactly the shape an update
// Input's optional fields need.
func setFieldValue(field reflect.Value, isPointer bool, value reflect.Value) {
	if !isPointer {
		field.Set(value)
		return
	}
	ptr := reflect.New(value.Type())
	ptr.Elem().Set(value)
	field.Set(ptr)
}

// checkRequiredFlags returns a usageError for the first vngcloud:"required"
// field of the struct inputPtr points to that is still zero after the
// --cli-input-json and flag merge: naming the flag for an ordinary field, or
// the Go field name for one noFlag (op.go's NoFlag) marks, since that field
// has no flag to name. noFlag may be nil, which no field is ever in.
func checkRequiredFlags(inputPtr any, noFlag map[string]bool) error {
	v := reflect.ValueOf(inputPtr).Elem()
	t := v.Type()
	for i := range t.NumField() {
		f := t.Field(i)
		if f.Tag.Get("vngcloud") != "required" {
			continue
		}
		if !v.Field(i).IsZero() {
			continue
		}
		if noFlag[f.Name] {
			return newUsageError("%s is required; set it with --cli-input-json", f.Name)
		}
		return newUsageError("--%s is required", flagNameFor(f.Name))
	}
	return nil
}
