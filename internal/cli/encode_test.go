package cli

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

type encodeBase struct {
	ID   string
	Name string
}

type encodeEmbedded struct {
	encodeBase
	Extra string
}

type encodeNested struct {
	Widget encodeBase
	Count  int64
}

type encodeWithPointersAndSlices struct {
	Ptr    *string
	Nested *encodeBase
	Items  []string
	Nils   []string
}

type encodeWithAny struct {
	Value any
}

type encodeWithMap struct {
	Descriptions map[string]string
}

type encodeWithTime struct {
	CreatedAt time.Time
}

func mustEncode(t *testing.T, v any) string {
	t.Helper()
	data, err := encodeJSON(v)
	if err != nil {
		t.Fatalf("encodeJSON: %v", err)
	}
	return string(data)
}

// assertJSONEqual decodes both sides generically so key order (which
// encodeJSON deliberately preserves as declaration order, unlike a plain
// map comparison) is checked separately by the caller when it matters.
func assertJSONEqual(t *testing.T, got, want string) {
	t.Helper()
	var gv, wv any
	if err := json.Unmarshal([]byte(got), &gv); err != nil {
		t.Fatalf("got is not valid JSON: %v (%s)", err, got)
	}
	if err := json.Unmarshal([]byte(want), &wv); err != nil {
		t.Fatalf("want is not valid JSON: %v (%s)", err, want)
	}
	gb, _ := json.Marshal(gv)
	wb, _ := json.Marshal(wv)
	if string(gb) != string(wb) {
		t.Fatalf("got %s, want %s", got, want)
	}
}

func TestEncodeJSONUsesGoFieldNamesNotJSONTags(t *testing.T) {
	type withTags struct {
		UUID string `json:"uuid"`
	}
	got := mustEncode(t, &withTags{UUID: "u-1"})
	assertJSONEqual(t, got, `{"UUID":"u-1"}`)
}

func TestEncodeJSONDeclarationOrder(t *testing.T) {
	type ordered struct {
		Zebra string
		Apple string
	}
	got := mustEncode(t, &ordered{Zebra: "z", Apple: "a"})
	want := "{\n  \"Zebra\": \"z\",\n  \"Apple\": \"a\"\n}"
	if got != want {
		t.Fatalf("got %q, want %q (declaration order, not alphabetical)", got, want)
	}
}

func TestEncodeJSONFlattensEmbeddedStruct(t *testing.T) {
	got := mustEncode(t, &encodeEmbedded{encodeBase: encodeBase{ID: "1", Name: "n"}, Extra: "e"})
	assertJSONEqual(t, got, `{"ID":"1","Name":"n","Extra":"e"}`)
}

func TestEncodeJSONNestedStruct(t *testing.T) {
	got := mustEncode(t, &encodeNested{Widget: encodeBase{ID: "1", Name: "n"}, Count: 3})
	assertJSONEqual(t, got, `{"Widget":{"ID":"1","Name":"n"},"Count":3}`)
}

func TestEncodeJSONNilPointerAndSlice(t *testing.T) {
	got := mustEncode(t, &encodeWithPointersAndSlices{})
	assertJSONEqual(t, got, `{"Ptr":null,"Nested":null,"Items":[],"Nils":[]}`)
}

func TestEncodeJSONNonNilPointer(t *testing.T) {
	s := "hi"
	got := mustEncode(t, &encodeWithPointersAndSlices{Ptr: &s, Items: []string{"a", "b"}})
	assertJSONEqual(t, got, `{"Ptr":"hi","Nested":null,"Items":["a","b"],"Nils":[]}`)
}

func TestEncodeJSONAnyField(t *testing.T) {
	got := mustEncode(t, &encodeWithAny{Value: "a string"})
	assertJSONEqual(t, got, `{"Value":"a string"}`)

	got = mustEncode(t, &encodeWithAny{Value: 42})
	assertJSONEqual(t, got, `{"Value":42}`)

	got = mustEncode(t, &encodeWithAny{Value: nil})
	assertJSONEqual(t, got, `{"Value":null}`)
}

func TestEncodeJSONMapKeysSorted(t *testing.T) {
	got := mustEncode(t, &encodeWithMap{Descriptions: map[string]string{"vi": "Vietnamese", "en": "English"}})
	want := "{\n  \"Descriptions\": {\n    \"en\": \"English\",\n    \"vi\": \"Vietnamese\"\n  }\n}"
	if got != want {
		t.Fatalf("got %q, want %q (map keys sorted)", got, want)
	}
}

func TestEncodeJSONTimeIsRFC3339(t *testing.T) {
	when := time.Date(2026, 1, 2, 15, 4, 5, 0, time.UTC)
	got := mustEncode(t, &encodeWithTime{CreatedAt: when})
	assertJSONEqual(t, got, `{"CreatedAt":"2026-01-02T15:04:05Z"}`)
}

func TestEncodeJSONUnexportedFieldsSkipped(t *testing.T) {
	type withUnexported struct {
		Name   string
		hidden string //nolint:unused // proves encodeJSON skips it
	}
	got := mustEncode(t, &withUnexported{Name: "n", hidden: "shhh"})
	assertJSONEqual(t, got, `{"Name":"n"}`)
	if strings.Contains(got, "shhh") {
		t.Fatalf("unexported field value leaked: %s", got)
	}
}
