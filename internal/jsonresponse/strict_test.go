package jsonresponse

import (
	"encoding/json"
	"errors"
	"testing"
)

type strictDocument struct {
	ID        string            `json:"id"`
	Actions   []string          `json:"actions,omitempty"`
	Data      []*strictDocument `json:"data"`
	Condition map[string]any    `json:"condition,omitempty"`
}

type strictCustom struct {
	ID string `json:"id"`
}

func (*strictCustom) UnmarshalJSON([]byte) error { panic("validator must not call target methods") }

type strictEmbedded struct {
	ID string `json:"id,omitempty"`
}
type strictTagged struct {
	strictEmbedded
	Actions []string `json:"permissions,omitempty"`
	Ignored string   `json:"-"`
}

type strictLeft struct{ ID string }
type strictRight struct{ ID string }
type strictAmbiguous struct {
	strictLeft
	strictRight
}
type strictDominant struct {
	strictLeft
	ID string `json:"ID"`
}
type strictTaggedWinner struct {
	strictLeft
	strictEmbedded
}
type strictRecursive struct {
	*strictRecursive
	ID string
}

func TestValidateStrict(t *testing.T) {
	cases := []struct {
		name, body string
		target     any
		valid      bool
	}{
		{"nested duplicate", `{"unknown":[{"key":1,"key":2}]}`, new(strictDocument), false},
		{"escaped duplicate", `{"id":"a","\u0069d":"b"}`, new(strictDocument), false},
		{"id alias", `{"Id":"a","id":"b"}`, new(strictDocument), false},
		{"actions alias", `{"actions":[],"Actions":[]}`, new(strictDocument), false},
		{"data alias", `{"DATA":[],"data":[]}`, new(strictDocument), false},
		{"nested alias", `{"data":[{"id":"a","ID":"b"}]}`, new(strictDocument), false},
		{"siblings", `{"data":[{"id":"a"},{"id":"b"}]}`, new(strictDocument), true},
		{"condition map", `{"condition":{"Env":"one","env":"two"}}`, new(strictDocument), true},
		{"condition duplicate", `{"condition":{"Env":1,"Env":2}}`, new(strictDocument), false},
		{"unknown aliases", `{"future":{},"FUTURE":{}}`, new(strictDocument), true},
		{"map aliases", `{"Id":"a","id":"b"}`, new(map[string]string), true},
		{"interface aliases", `{"Id":"a","id":"b"}`, new(any), true},
		{"raw aliases", `{"Id":"a","id":"b"}`, new(json.RawMessage), true},
		{"custom aliases", `{"Id":"a","id":"b"}`, new(strictCustom), true},
		{"custom nested aliases", `{"nested":{"Id":"a","id":"b"}}`, new(strictCustom), true},
		{"custom nested duplicates", `{"nested":{"id":"a","id":"b"}}`, new(strictCustom), false},
		{"embedded aliases", `{"Id":"a","id":"b"}`, new(strictTagged), false},
		{"tagged aliases", `{"permissions":[],"Permissions":[]}`, new(strictTagged), false},
		{"ignored aliases", `{"Ignored":"a","ignored":"b"}`, new(strictTagged), true},
		{"ignored duplicates", `{"Ignored":"a","Ignored":"b"}`, new(strictTagged), false},
		{"ambiguous fields", `{"ID":"a","id":"b"}`, new(strictAmbiguous), true},
		{"shallow dominant", `{"ID":"a","id":"b"}`, new(strictDominant), false},
		{"tagged winner", `{"ID":"a","id":"b"}`, new(strictTaggedWinner), true},
		{"recursive embedding", `{"ID":"a","id":"b"}`, &strictRecursive{strictRecursive: &strictRecursive{}}, false},
		{"unicode fold", `{"K":"a","K":"b"}`, new(struct{ K string }), false},
		{"unicode long s", `{"S":"a","ſ":"b"}`, new(struct{ S string }), false},
		{"distinct exact fields", `{"id":"a","ID":"b"}`, new(struct {
			Lower string `json:"id"`
			Upper string `json:"ID"`
		}), true},
		{"fallback first field", `{"ID":"a","Id":"b"}`, new(struct {
			Upper string `json:"ID"`
			Lower string `json:"id"`
		}), false},
		{"malformed", `{"id":`, new(strictDocument), false},
		{"trailing", `{} {}`, new(strictDocument), false},
		{"empty", ``, new(strictDocument), false},
		{"wrong kind aliases", `{"id":1,"ID":2}`, new(int), true},
		{"wrong kind duplicates", `{"id":1,"id":2}`, new(int), false},
		{"large number", `{"unknown":1e1000}`, new(strictDocument), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateStrict([]byte(tc.body), tc.target)
			if (err == nil) != tc.valid {
				t.Fatalf("ValidateStrict() = %v, valid = %v", err, tc.valid)
			}
		})
	}
}

func TestValidateStrictConditionDecode(t *testing.T) {
	body := []byte(`{"condition":{"Env":"one","env":"two"}}`)
	var out strictDocument
	if err := ValidateStrict(body, &out); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Condition) != 2 || out.Condition["Env"] != "one" || out.Condition["env"] != "two" {
		t.Fatalf("condition = %v", out.Condition)
	}
}

func TestValidateStrictSyntaxIdentity(t *testing.T) {
	body := []byte(`{"id":`)
	err := ValidateStrict(body, new(strictDocument))
	var syntax *json.SyntaxError
	if !errors.As(err, &syntax) {
		t.Fatalf("error = %T, want SyntaxError", err)
	}
}
