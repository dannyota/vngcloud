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

func TestCheckPathID(t *testing.T) {
	cases := []struct {
		name  string
		value string
		valid bool
	}{
		{"alnum with dash", "abc-123", true},
		{"uppercase", "ABC", true},
		{"empty", "", false},
		{"dot", ".", false},
		{"dotdot", "..", false},
		{"slash", "a/b", false},
		{"space", "a b", false},
		{"percent encoded slash", "a%2F", false},
	}
	for _, tc := range cases {
		err := CheckPathID("billing.GetBudget", "BudgetUUID", tc.value)
		if tc.valid {
			if err != nil {
				t.Fatalf("%s: err = %v", tc.name, err)
			}
			continue
		}
		if !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("%s: err = %v, want ErrInvalidInput", tc.name, err)
		}
		if !strings.Contains(err.Error(), "billing.GetBudget") || !strings.Contains(err.Error(), "BudgetUUID") {
			t.Fatalf("%s: err = %v, missing op or field", tc.name, err)
		}
	}
}

func TestCheckDate(t *testing.T) {
	cases := []struct {
		name  string
		value string
		valid bool
	}{
		{"valid", "2026-09-01", true},
		{"single digit month and day", "2026-9-1", false},
		{"wrong order", "01/09/2026", false},
		{"invalid day", "2026-02-30", false},
		{"empty", "", false},
	}
	for _, tc := range cases {
		err := CheckDate("billing.GetCostOverview", "StartDate", tc.value)
		if tc.valid {
			if err != nil {
				t.Fatalf("%s: err = %v", tc.name, err)
			}
			continue
		}
		if !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("%s: err = %v, want ErrInvalidInput", tc.name, err)
		}
		if !strings.Contains(err.Error(), "billing.GetCostOverview") || !strings.Contains(err.Error(), "StartDate") {
			t.Fatalf("%s: err = %v, missing op or field", tc.name, err)
		}
	}
}
