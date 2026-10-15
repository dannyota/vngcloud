package jsonresponse

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"slices"
	"strings"
	"unicode"
)

// ValidateStrict rejects duplicate keys and keys that decode into the same
// struct field. It follows target's static type without calling its methods.
// Custom JSON unmarshallers receive only syntax and exact-duplicate checks.
func ValidateStrict(raw []byte, target any) error {
	if !json.Valid(raw) {
		var value json.RawMessage
		return json.Unmarshal(raw, &value)
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if err := strictValue(d, reflect.TypeOf(target)); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return errors.New("invalid JSON response")
	}
	return nil
}

var unmarshalerType = reflect.TypeFor[json.Unmarshaler]()

func strictType(t reflect.Type) reflect.Type {
	for t != nil {
		if t.Implements(unmarshalerType) || reflect.PointerTo(t).Implements(unmarshalerType) {
			return nil
		}
		if t.Kind() != reflect.Pointer {
			return t
		}
		t = t.Elem()
	}
	return nil
}

func strictValue(d *json.Decoder, t reflect.Type) error {
	token, err := d.Token()
	if err != nil {
		return errors.New("invalid JSON response")
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	t = strictType(t)
	switch delimiter {
	case '{':
		var fields []strictField
		if t != nil && t.Kind() == reflect.Struct {
			fields = strictFields(t)
		}
		keys := make(map[string]bool)
		assigned := make(map[int]bool)
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
			var child reflect.Type
			if i := matchStrictField(fields, key); i >= 0 {
				if assigned[i] {
					return errors.New("duplicate JSON field")
				}
				assigned[i] = true
				child = fields[i].typ
			}
			if err := strictValue(d, child); err != nil {
				return err
			}
		}
	case '[':
		var child reflect.Type
		if t != nil && (t.Kind() == reflect.Slice || t.Kind() == reflect.Array) {
			child = t.Elem()
		}
		for d.More() {
			if err := strictValue(d, child); err != nil {
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

type strictField struct {
	name   string
	typ    reflect.Type
	index  []int
	tagged bool
}

func matchStrictField(fields []strictField, key string) int {
	for i, f := range fields {
		if f.name == key {
			return i
		}
	}
	// encoding/json prefers exact names, then the first field in index order
	// whose name matches by Unicode simple folding, as strings.EqualFold does.
	for i, f := range fields {
		if strings.EqualFold(f.name, key) {
			return i
		}
	}
	return -1
}

func strictFields(t reflect.Type) []strictField {
	var candidates []strictField
	collectStrictFields(t, nil, make(map[reflect.Type]bool), &candidates)
	slices.SortFunc(candidates, func(a, b strictField) int {
		if n := strings.Compare(a.name, b.name); n != 0 {
			return n
		}
		if n := len(a.index) - len(b.index); n != 0 {
			return n
		}
		if a.tagged != b.tagged {
			if a.tagged {
				return -1
			}
			return 1
		}
		return slices.Compare(a.index, b.index)
	})
	var fields []strictField
	for i := 0; i < len(candidates); {
		end := i + 1
		for end < len(candidates) && candidates[end].name == candidates[i].name {
			end++
		}
		first := candidates[i]
		// At the shallowest depth a tagged field dominates an untagged field.
		// Two equally dominant fields are ambiguous and encoding/json ignores both.
		if end == i+1 || len(first.index) != len(candidates[i+1].index) || first.tagged != candidates[i+1].tagged {
			fields = append(fields, first)
		}
		i = end
	}
	slices.SortFunc(fields, func(a, b strictField) int { return slices.Compare(a.index, b.index) })
	return fields
}

func collectStrictFields(t reflect.Type, index []int, ancestors map[reflect.Type]bool, fields *[]strictField) {
	if ancestors[t] {
		return
	}
	ancestors[t] = true
	defer delete(ancestors, t)
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		ft := f.Type
		if ft.Kind() == reflect.Pointer {
			ft = ft.Elem()
		}
		if !f.IsExported() && (!f.Anonymous || ft.Kind() != reflect.Struct) {
			continue
		}
		tag := f.Tag.Get("json")
		if tag == "-" {
			continue
		}
		name, _, _ := strings.Cut(tag, ",")
		if !validStrictTag(name) {
			name = ""
		}
		path := append(slices.Clone(index), i)
		if name == "" && f.Anonymous && ft.Kind() == reflect.Struct {
			collectStrictFields(ft, path, ancestors, fields)
			continue
		}
		tagged := name != ""
		if name == "" {
			name = f.Name
		}
		*fields = append(*fields, strictField{name: name, typ: f.Type, index: path, tagged: tagged})
	}
}

func validStrictTag(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		if strings.ContainsRune("!#$%&()*+-./:;<=>?@[]^_{|}~ ", r) {
			continue
		}
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}
