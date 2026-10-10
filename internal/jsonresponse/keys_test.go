package jsonresponse

import "testing"

func TestValidate(t *testing.T) {
	for _, body := range []string{
		`{"left":{"a":1},"right":{"a":2}}`,
		`[{"a":1},{"a":2}]`,
		`{"a":[{"a":1},{"a":2}]}`,
		`{"number":1e1000}`,
	} {
		if err := Validate([]byte(body)); err != nil {
			t.Fatalf("valid response rejected: %v", err)
		}
	}
	for _, body := range []string{
		`{"a":1,"a":2}`,
		`{"a":1,"\u0061":2}`,
		`{"nested":[{"a":1,"a":2}]}`,
		`{"a":1} {"b":2}`,
		`{"a":`,
		``,
	} {
		if err := Validate([]byte(body)); err == nil {
			t.Fatal("invalid response accepted")
		}
	}
}
