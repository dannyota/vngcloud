package globalloadbalancer

import (
	"fmt"
	"net/netip"
	"slices"
	"strings"
	"unicode"

	"danny.vn/vngcloud/internal/core"
)

func canonicalCIDRs(items []string) (string, error) {
	const op = "globalloadbalancer.CreateListener"
	if len(items) == 0 {
		return "", fmt.Errorf("%w: %s requires at least one allowed CIDR", core.ErrInvalidInput, op)
	}
	prefixes := make([]netip.Prefix, 0, len(items))
	for _, item := range items {
		p, ok := parseCIDR(item)
		if !ok {
			return "", fmt.Errorf("%w: %s allowed CIDRs must be canonical IPv4 networks from /8 through /32", core.ErrInvalidInput, op)
		}
		if slices.Contains(prefixes, p) {
			return "", fmt.Errorf("%w: %s allowed CIDRs repeat a prefix", core.ErrInvalidInput, op)
		}
		prefixes = append(prefixes, p)
	}
	if coversAllIPv4(prefixes) {
		return "", fmt.Errorf("%w: %s allowed CIDRs may not cover all IPv4 addresses", core.ErrInvalidInput, op)
	}
	return strings.Join(items, ","), nil
}

func parseCIDR(item string) (netip.Prefix, bool) {
	if item == "" || strings.IndexFunc(item, unicode.IsSpace) >= 0 {
		return netip.Prefix{}, false
	}
	p, err := netip.ParsePrefix(item)
	if err != nil || !p.Addr().Is4() || p.Bits() < 8 || p.Masked() != p || p.String() != item {
		return netip.Prefix{}, false
	}
	return p, true
}

func coversAllIPv4(prefixes []netip.Prefix) bool {
	var total uint64
	for i, p := range prefixes {
		nested := false
		for j, q := range prefixes {
			if i != j && q.Bits() <= p.Bits() && q.Contains(p.Addr()) && (q.Bits() < p.Bits() || j < i) {
				nested = true
				break
			}
		}
		if !nested {
			total += 1 << (32 - p.Bits())
		}
	}
	return total >= 1<<32
}

func cidrSetEqual(got string, want []string) bool {
	parts := strings.Split(got, ",")
	if len(parts) != len(want) {
		return false
	}
	seen := make([]string, 0, len(parts))
	for _, part := range parts {
		if _, ok := parseCIDR(part); !ok || slices.Contains(seen, part) || !slices.Contains(want, part) {
			return false
		}
		seen = append(seen, part)
	}
	return true
}
