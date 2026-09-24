package core

import (
	"errors"
	"strings"
	"testing"
)

type reqInput struct {
	ZoneID string `vngcloud:"required"`
	Name   string
}

func TestCheckRequired(t *testing.T) {
	cases := []struct {
		name string
		in   any
		want string
	}{
		{"nil pointer", (*reqInput)(nil), "ZoneID"},
		{"empty field", &reqInput{Name: "x"}, "ZoneID"},
		{"set", &reqInput{ZoneID: "z"}, ""},
		{"no required fields", &struct{ Name string }{}, ""},
	}
	for _, tc := range cases {
		err := CheckRequired("dns.GetHostedZone", tc.in)
		if tc.want == "" {
			if err != nil {
				t.Fatalf("%s: err = %v", tc.name, err)
			}
			continue
		}
		if !errors.Is(err, ErrInvalidInput) || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "dns.GetHostedZone") {
			t.Fatalf("%s: err = %v", tc.name, err)
		}
	}
}
