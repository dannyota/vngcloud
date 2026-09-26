package cli

import (
	"reflect"
	"strings"
)

// sensitiveKeySubstrings names the lower-case substrings that mark a
// map-backed Output's key as holding a secret, per the CLI reads design's
// "Key redaction for map-backed Outputs". It is a guard for a key nobody has
// seen yet, not a substitute for a typed model: it hides a value only when
// the key name looks secret.
var sensitiveKeySubstrings = []string{"password", "secret", "token", "credential", "privatekey"}

// redactedValue replaces the value redactMaps finds under a sensitive key.
const redactedValue = "[redacted]"

// isSensitiveMapKey reports whether key's lower-case form contains one of
// sensitiveKeySubstrings.
func isSensitiveMapKey(key string) bool {
	lower := strings.ToLower(key)
	for _, s := range sensitiveKeySubstrings {
		if strings.Contains(lower, s) {
			return true
		}
	}
	return false
}

// redactMaps walks v in place and replaces the value of any string-keyed map
// entry whose key isSensitiveMapKey, at any depth: through a struct field, a
// slice or array element, a map value, and a pointer or interface. render.go
// calls it on every Output before encodeJSON, so the guard reaches json,
// table, and text output and --query alike, since all four start from that
// one walked value.
//
// Only a map is ever mutated: a Go map is a reference, so setting one of its
// entries is visible to encodeJSON without copying the map itself. A struct
// or slice is only walked, never copied, which is safe because every Output
// the CLI renders is built for that one render and discarded right after.
func redactMaps(v reflect.Value) {
	if !v.IsValid() {
		return
	}
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface:
		if !v.IsNil() {
			redactMaps(v.Elem())
		}
	case reflect.Struct:
		for i := range v.NumField() {
			// CanInterface is false for a field reflect.Value obtained from an
			// unexported struct field; skipping it here matches encode.go's
			// own IsExported check and avoids a panic from recursing into a
			// value the reflect package refuses to expose.
			if f := v.Field(i); f.CanInterface() {
				redactMaps(f)
			}
		}
	case reflect.Slice, reflect.Array:
		for i := range v.Len() {
			redactMaps(v.Index(i))
		}
	case reflect.Map:
		redactMapEntries(v)
	}
}

// redactMapEntries redacts m's own sensitive entries and walks every other
// entry's value for a nested map further down. redactedValue is a string, so
// it is only ever written into a map whose value type can hold a string
// (map[string]any, which is what every map-backed Output in this design
// uses); a map with a narrower value type, such as map[string]int, is walked
// but never redacted, since nothing sensitive can be a number.
func redactMapEntries(m reflect.Value) {
	if m.IsNil() {
		return
	}
	elem := reflect.ValueOf(redactedValue)
	canRedact := elem.Type().AssignableTo(m.Type().Elem())
	for _, key := range m.MapKeys() {
		if canRedact && key.Kind() == reflect.String && isSensitiveMapKey(key.String()) {
			m.SetMapIndex(key, elem)
			continue
		}
		redactMaps(m.MapIndex(key))
	}
}
