package testutil

import (
	"encoding/json"
	"reflect"
	"testing"
)

// RequireAllFieldsSet fails t unless every exported field of the struct
// pointed to by in is non-zero. A drift test builds a quote and a create
// from one such Input, so a field added later must be set there too.
func RequireAllFieldsSet(t testing.TB, in any) {
	t.Helper()
	v := reflect.ValueOf(in).Elem()
	for i := 0; i < v.NumField(); i++ {
		if f := v.Type().Field(i); f.IsExported() && v.Field(i).IsZero() {
			t.Fatalf("%s.%s is zero: a drift test needs every field set", v.Type().Name(), f.Name)
		}
	}
}

// RequireQuoteKeysInCreate fails t unless every key of quoteInfo, except
// those in skip, is present in createBody with an equal JSON value.
// quoteInfo is a quote's resourceInfo and createBody is the create's
// request body. A priced field that only the create sends makes the quote
// understate the bill, so this catches it.
func RequireQuoteKeysInCreate(t testing.TB, quoteInfo map[string]any, createBody any, skip ...string) {
	t.Helper()
	raw, err := json.Marshal(createBody)
	if err != nil {
		t.Fatalf("marshal create body: %v", err)
	}
	var create map[string]any
	if err := json.Unmarshal(raw, &create); err != nil {
		t.Fatalf("decode create body: %v, raw = %s", err, raw)
	}
	skipped := make(map[string]bool, len(skip))
	for _, k := range skip {
		skipped[k] = true
	}
	for k, want := range quoteInfo {
		if skipped[k] {
			continue
		}
		got, ok := create[k]
		if !ok {
			t.Errorf("quote key %q is missing from the create body %s", k, raw)
			continue
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("quote key %q = %v, create body has %v", k, want, got)
		}
	}
}
