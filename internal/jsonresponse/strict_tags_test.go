package jsonresponse

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestValidateStrictInvalidTags(t *testing.T) {
	for _, tc := range []struct {
		name, tag string
		aliases   bool
	}{
		{"backslash", `json:"bad\\tag"`, true},
		{"quote", `json:"bad\"tag"`, true},
		{"tab", `json:"bad\ttag"`, false},
		{"unicode symbol", `json:"bad☃tag"`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			typ := reflect.StructOf([]reflect.StructField{{Name: "Value", Type: reflect.TypeFor[string](), Tag: reflect.StructTag(tc.tag)}})
			for _, body := range []string{`{"bad":"first"}`, `{"BAD":"second"}`, `{"bad":"first","BAD":"second"}`, `{}`, `null`} {
				target := reflect.New(typ)
				if err := json.Unmarshal([]byte(body), target.Interface()); err != nil {
					t.Fatal(err)
				}
				if tc.aliases {
					want := ""
					if body == `{"bad":"first"}` {
						want = "first"
					}
					if body == `{"BAD":"second"}` || body == `{"bad":"first","BAD":"second"}` {
						want = "second"
					}
					if target.Elem().Field(0).String() != want {
						t.Fatal("pinned decoder's invalid-tag field assignment changed")
					}
				}
				if err := ValidateStrict([]byte(body), target.Interface()); err == nil {
					t.Errorf("accepted invalid-tag target with body %s", body)
				}
			}
			nested := reflect.StructOf([]reflect.StructField{{Name: "Child", Type: reflect.SliceOf(reflect.PointerTo(typ)), Tag: `json:"child"`}})
			if err := ValidateStrict([]byte(`{}`), reflect.New(nested).Interface()); err == nil {
				t.Error("accepted nested invalid-tag type")
			}
			if err := ValidateStrict([]byte(`{}`), reflect.New(reflect.MapOf(reflect.TypeFor[string](), typ)).Interface()); err == nil {
				t.Error("accepted map with invalid-tag element type")
			}
		})
	}
}
