package cli

import (
	"reflect"
	"strings"
	"unicode"
)

// sensitiveKeySubstrings names the normalized substrings that mark a
// map[string]any Output's key as holding a secret, per the CLI reads
// design's "Key redaction for map-backed Outputs". Each entry is already in
// normalizeMapKey's own form (lower-case letters and digits only), since
// that is the only form isSensitiveMapKey ever compares against. It is a
// guard for a key nobody has seen yet, not a substitute for a typed model:
// it hides a value only when the key name looks secret.
var sensitiveKeySubstrings = []string{
	"password", "passwd", "passphrase", "secret", "token", "credential",
	"privatekey", "apikey", "authorization",
}

// isSensitiveMapKey reports whether key, once normalized, contains one of
// sensitiveKeySubstrings. Normalizing first, rather than matching key's
// lower-cased form directly, means "private_key", "private-key", and
// "Private Key" all match the same "privatekey" entry that "PrivateKey"
// already did.
func isSensitiveMapKey(key string) bool {
	normalized := normalizeMapKey(key)
	for _, s := range sensitiveKeySubstrings {
		if strings.Contains(normalized, s) {
			return true
		}
	}
	return false
}

// normalizeMapKey lower-cases key and drops every rune that is not a letter
// or a digit, so punctuation, whitespace, and case are never what decides
// whether a key looks secret.
func normalizeMapKey(key string) string {
	var b strings.Builder
	for _, r := range key {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(unicode.ToLower(r))
		}
	}
	return b.String()
}

// redactMaps walks v in place and replaces the value of any sensitive-keyed
// entry of a map[string]any, at any depth reached through a struct field, a
// slice or array element, a pointer, or an interface value. A map[string]any
// value's own values are themselves interface-typed, so a nested object
// (another map[string]any) or array ([]any) it holds is reached the same
// way. render.go calls redactMaps on every Output before encodeJSON, so the
// guard reaches json, table, and text output and --query alike, since all
// four start from that one walked value.
//
// A map whose value type is not any, such as monitor.CheckRequest's
// map[string]string Headers and Query fields, is left alone entirely: never
// walked and never redacted. The CLI reads design scopes this guard to
// map-backed Outputs, whose fields decode as map[string]any; a typed field
// must print exactly what the API sent, per the monitor design's decision
// that check headers and bodies print unredacted.
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
		if v.Type().Key().Kind() == reflect.String && v.Type().Elem().Kind() == reflect.Interface {
			redactMapEntries(v)
		}
	}
}

// redactMapEntries redacts m's own sensitive entries and walks every other
// entry's value for a nested map or slice further down, through the
// interface-typed value reflect.Value.MapIndex returns. redactMaps only ever
// calls this for a map whose key kind is String and value kind is Interface,
// so m's value type can always hold redactedPlaceholder, a string.
func redactMapEntries(m reflect.Value) {
	if m.IsNil() {
		return
	}
	placeholder := reflect.ValueOf(redactedPlaceholder)
	for _, key := range m.MapKeys() {
		if isSensitiveMapKey(key.String()) {
			m.SetMapIndex(key, placeholder)
			continue
		}
		redactMaps(m.MapIndex(key))
	}
}
