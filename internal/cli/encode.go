package cli

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"time"
)

// timeType lets encodeValue recognize time.Time by reflect.Type instead of a
// type assertion, since encodeValue only ever holds a reflect.Value.
var timeType = reflect.TypeOf(time.Time{})

// encodeJSON encodes v as the CLI's own JSON, per the CLI design's "Output":
// struct fields become object keys using their Go names, in declaration
// order, with JSON struct tags ignored (a resource model such as
// compute.Server keeps its own API tags for decoding responses, but the CLI
// always shows its Go field names); an embedded struct's fields flatten into
// the parent object, as encoding/json does; unexported fields are skipped;
// map keys are sorted; an interface-typed ("any") value is encoded by its
// dynamic type; a nil pointer becomes JSON null, and a nil slice becomes [].
//
// This cannot be built by handing an intermediate map[string]any to
// encoding/json.Marshal, because Go maps have no order and encoding/json
// always sorts map keys: that would lose the declaration order this format
// requires. Instead it writes the structural JSON (object and array braces,
// keys, and indentation) itself, delegating only leaf scalar values (after
// time.Time, which has its own RFC 3339 MarshalJSON) to encoding/json for
// escaping and number formatting.
func encodeJSON(v any) ([]byte, error) {
	e := &jsonEncoder{indent: "  "}
	if err := e.encode(reflect.ValueOf(v), 0); err != nil {
		return nil, err
	}
	return e.buf, nil
}

type jsonEncoder struct {
	buf    []byte
	indent string
}

func (e *jsonEncoder) encode(v reflect.Value, depth int) error {
	if !v.IsValid() {
		e.buf = append(e.buf, "null"...)
		return nil
	}
	switch v.Kind() {
	case reflect.Interface:
		if v.IsNil() {
			e.buf = append(e.buf, "null"...)
			return nil
		}
		return e.encode(v.Elem(), depth)
	case reflect.Pointer:
		if v.IsNil() {
			e.buf = append(e.buf, "null"...)
			return nil
		}
		return e.encode(v.Elem(), depth)
	case reflect.Struct:
		if v.Type() == timeType {
			return e.encodeScalar(v)
		}
		return e.encodeStruct(v, depth)
	case reflect.Map:
		return e.encodeMap(v, depth)
	case reflect.Slice:
		if v.IsNil() {
			e.buf = append(e.buf, "[]"...)
			return nil
		}
		return e.encodeSlice(v, depth)
	case reflect.Array:
		return e.encodeSlice(v, depth)
	default:
		return e.encodeScalar(v)
	}
}

func (e *jsonEncoder) encodeScalar(v reflect.Value) error {
	data, err := json.Marshal(v.Interface())
	if err != nil {
		return fmt.Errorf("cli: encoding %s: %w", v.Type(), err)
	}
	e.buf = append(e.buf, data...)
	return nil
}

// structField is one field to encode: name is its Go name (an embedded
// struct's own field names, promoted to the parent), and val is its value.
type structField struct {
	name string
	val  reflect.Value
}

// encodeStruct gathers fields in declaration order, flattening an embedded
// struct's fields into the parent like encoding/json. It does not
// reimplement encoding/json's full ambiguous-promotion rules for a name that
// collides across embedding depths; nothing in this SDK's Output or resource
// types does that today.
func (e *jsonEncoder) encodeStruct(v reflect.Value, depth int) error {
	fields := collectFields(v)
	e.buf = append(e.buf, '{')
	for i, f := range fields {
		if i > 0 {
			e.buf = append(e.buf, ',')
		}
		e.newlineIndent(depth + 1)
		if err := e.encodeScalar(reflect.ValueOf(f.name)); err != nil {
			return err
		}
		e.buf = append(e.buf, ':', ' ')
		if err := e.encode(f.val, depth+1); err != nil {
			return err
		}
	}
	if len(fields) > 0 {
		e.newlineIndent(depth)
	}
	e.buf = append(e.buf, '}')
	return nil
}

// collectFields matches encoding/json's own promotion rule: an anonymous
// struct field's fields promote into the parent even when the embedded
// type's own name is unexported (only its fields need to be exported), and a
// nil embedded pointer contributes nothing at all, not even a null entry
// under its type name.
func collectFields(v reflect.Value) []structField {
	var fields []structField
	t := v.Type()
	for i := range t.NumField() {
		f := t.Field(i)
		fv := v.Field(i)
		if f.Anonymous {
			inner := fv
			nilPointer := false
			for inner.Kind() == reflect.Pointer {
				if inner.IsNil() {
					nilPointer = true
					break
				}
				inner = inner.Elem()
			}
			switch {
			case nilPointer:
				continue
			case inner.Kind() == reflect.Struct && inner.Type() != timeType:
				fields = append(fields, collectFields(inner)...)
				continue
			}
			// An anonymous field that is neither a nil pointer nor a
			// struct (an embedded interface or named scalar type, not used
			// by any Output or resource type today): treated as a plain
			// field below, keyed by the type's own name.
		}
		if !f.IsExported() {
			continue
		}
		fields = append(fields, structField{name: f.Name, val: fv})
	}
	return fields
}

func (e *jsonEncoder) encodeMap(v reflect.Value, depth int) error {
	if v.IsNil() {
		e.buf = append(e.buf, "null"...)
		return nil
	}
	keys := v.MapKeys()
	type entry struct {
		key string
		val reflect.Value
	}
	entries := make([]entry, len(keys))
	for i, k := range keys {
		entries[i] = entry{key: fmt.Sprint(k.Interface()), val: v.MapIndex(k)}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].key < entries[j].key })

	e.buf = append(e.buf, '{')
	for i, en := range entries {
		if i > 0 {
			e.buf = append(e.buf, ',')
		}
		e.newlineIndent(depth + 1)
		if err := e.encodeScalar(reflect.ValueOf(en.key)); err != nil {
			return err
		}
		e.buf = append(e.buf, ':', ' ')
		if err := e.encode(en.val, depth+1); err != nil {
			return err
		}
	}
	if len(entries) > 0 {
		e.newlineIndent(depth)
	}
	e.buf = append(e.buf, '}')
	return nil
}

func (e *jsonEncoder) encodeSlice(v reflect.Value, depth int) error {
	n := v.Len()
	e.buf = append(e.buf, '[')
	for i := range n {
		if i > 0 {
			e.buf = append(e.buf, ',')
		}
		e.newlineIndent(depth + 1)
		if err := e.encode(v.Index(i), depth+1); err != nil {
			return err
		}
	}
	if n > 0 {
		e.newlineIndent(depth)
	}
	e.buf = append(e.buf, ']')
	return nil
}

func (e *jsonEncoder) newlineIndent(depth int) {
	e.buf = append(e.buf, '\n')
	for range depth {
		e.buf = append(e.buf, e.indent...)
	}
}
