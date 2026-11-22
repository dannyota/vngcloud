package globalloadbalancer

import (
	"errors"
	"fmt"
	"testing"

	"danny.vn/vngcloud"
)

func TestCanonicalCIDRsAccepts(t *testing.T) {
	got, err := canonicalCIDRs([]string{"192.0.2.5/32", "198.51.100.0/24", "10.0.0.0/8"})
	if err != nil || got != "192.0.2.5/32,198.51.100.0/24,10.0.0.0/8" {
		t.Fatalf("got=%q err=%v", got, err)
	}
}

func TestCanonicalCIDRsRejects(t *testing.T) {
	all := make([]string, 0, 256)
	for i := range 256 {
		all = append(all, fmt.Sprintf("%d.0.0.0/8", i))
	}
	for name, items := range map[string][]string{
		"empty set":        nil,
		"empty item":       {""},
		"space inside":     {"192.0.2.0 /24"},
		"leading space":    {" 192.0.2.0/24"},
		"trailing space":   {"192.0.2.0/24 "},
		"no prefix":        {"192.0.2.5"},
		"host bits":        {"192.0.2.5/24"},
		"slash zero":       {"0.0.0.0/0"},
		"slash seven":      {"10.0.0.0/7"},
		"duplicate prefix": {"10.0.0.0/8", "10.0.0.0/8"},
		"leading zeros":    {"010.0.0.0/8"},
		"ipv6":             {"2001:db8::/32"},
		"mapped ipv4":      {"::ffff:192.0.2.0/120"},
		"zone":             {"fe80::1%eth0/64"},
		"wildcard":         {"*"},
		"comma inside":     {"192.0.2.0/24,198.51.100.0/24"},
		"noncanonical len": {"192.0.2.0/024"},
		"covers all ipv4":  all,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := canonicalCIDRs(items); !errors.Is(err, vngcloud.ErrInvalidInput) {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestCIDRSetEqual(t *testing.T) {
	for _, tc := range []struct {
		got  string
		want bool
	}{
		{"192.0.2.5/32,198.51.100.0/24", true},
		{"198.51.100.0/24,192.0.2.5/32", true},
		{"192.0.2.5/32", false},
		{"192.0.2.5/32,198.51.100.0/24,203.0.113.0/24", false},
		{"192.0.2.5/32,198.51.100.0/24,198.51.100.0/24", false},
		{"192.0.2.5/32, 198.51.100.0/24", false},
		{"0.0.0.0/0", false},
		{"192.0.2.0/24,198.51.100.0/24", false},
		{"", false},
	} {
		if got := cidrSetEqual(tc.got, []string{"192.0.2.5/32", "198.51.100.0/24"}); got != tc.want {
			t.Errorf("%q = %v", tc.got, got)
		}
	}
}
