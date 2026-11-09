package floatingip

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/netip"
	"sort"
	"strings"
	"time"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/routes"
	"danny.vn/vngcloud/internal/transport"
)

// CheckGroups validates the bounded, explicitly selected membership before I/O.
func CheckGroups(op string, ids []string) error {
	unique := make(map[string]bool)
	for _, id := range ids {
		if err := core.CheckPathID(op, "SecurityGroupIDs", id); err != nil {
			return err
		}
		unique[id] = true
	}
	if len(unique) == 0 || len(unique) > 64 {
		return fmt.Errorf("%w: %s requires 1 to 64 security groups", core.ErrInvalidInput, op)
	}
	return nil
}

// Exposure reads complete named groups and rules and fingerprints the snapshot.
// The callback reuses the caller's whole-operation response protection.
func Exposure(ctx context.Context, c *core.Client, ids []string, allow bool, unsafe func([]byte) bool) ([32]byte, error) {
	const op = "floatingip.Exposure"
	var zero [32]byte
	if err := CheckGroups(op, ids); err != nil {
		return zero, err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	project, err := c.RequireProjectID(ctx)
	if err != nil {
		return zero, err
	}
	if err := core.CheckPathID(op, "ProjectID", project); err != nil {
		return zero, err
	}
	unique := make(map[string]bool)
	var rules []json.RawMessage
	snapshot := sha256.New()
	for _, id := range ids {
		if unique[id] {
			continue
		}
		unique[id] = true
		if err := ctx.Err(); err != nil {
			return zero, err
		}
		url := func(parts ...string) string {
			return c.RouteURL(routes.Route{Product: routes.ProductVServer, Version: "v2", Parts: append([]string{project, "secgroups", id}, parts...)})
		}
		var group struct {
			Data json.RawMessage `json:"data"`
		}
		if err := ReadJSON(ctx, c, transport.Request{Operation: op, Method: http.MethodGet, URL: url(), OK: []int{200}}, &group, unsafe); err != nil {
			return zero, err
		}
		var identity struct {
			ID string `json:"id"`
		}
		if len(group.Data) == 0 || json.Unmarshal(group.Data, &identity) != nil || identity.ID != id || core.CheckPathID(op, "SecurityGroupID", identity.ID) != nil {
			return zero, InvalidResponse(op, nil)
		}
		var response struct {
			Data json.RawMessage `json:"data"`
		}
		if err := ReadJSON(ctx, c, transport.Request{Operation: op, Method: http.MethodGet, URL: url("secGroupRules"), OK: []int{200}}, &response, unsafe); err != nil {
			return zero, err
		}
		var rows []json.RawMessage
		if len(response.Data) == 0 || bytes.Equal(response.Data, []byte("null")) || json.Unmarshal(response.Data, &rows) != nil {
			return zero, InvalidResponse(op, nil)
		}
		if len(rows) > 4096-len(rules) {
			return zero, InvalidResponse(op, nil)
		}
		for _, row := range rows {
			var fields map[string]json.RawMessage
			if json.Unmarshal(row, &fields) != nil || fields == nil {
				return zero, InvalidResponse(op, nil)
			}
		}
		rules = append(rules, rows...)
		// Canonical JSON removes insignificant formatting differences. Row order
		// remains part of the snapshot so any unproven change refuses the order.
		var data any
		decoder := json.NewDecoder(bytes.NewReader(group.Data))
		decoder.UseNumber()
		if decoder.Decode(&data) != nil {
			return zero, InvalidResponse(op, nil)
		}
		canonical, err := json.Marshal(data)
		if err != nil {
			return zero, InvalidResponse(op, err)
		}
		_, _ = snapshot.Write(canonical)
		decoder = json.NewDecoder(bytes.NewReader(response.Data))
		decoder.UseNumber()
		if decoder.Decode(&data) != nil {
			return zero, InvalidResponse(op, nil)
		}
		canonical, err = json.Marshal(data)
		if err != nil {
			return zero, InvalidResponse(op, err)
		}
		_, _ = snapshot.Write(canonical)
	}
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	if !allow && openLogin(rules) {
		return zero, ErrOpenLogin
	}
	var digest [32]byte
	copy(digest[:], snapshot.Sum(nil))
	return digest, nil
}

type ingressRule struct {
	Direction   *string `json:"direction"`
	EtherType   *string `json:"etherType"`
	Protocol    *string `json:"protocol"`
	Min         *int    `json:"portRangeMin"`
	Max         *int    `json:"portRangeMax"`
	Prefix      *string `json:"remoteIpPrefix"`
	RemoteGroup *string `json:"remoteGroupId"`
}

type interval struct{ first, last netip.Addr }

func openLogin(rawRules []json.RawMessage) bool {
	coverage := make(map[string][]interval)
	for _, raw := range rawRules {
		var r ingressRule
		if json.Unmarshal(raw, &r) != nil || r.Direction == nil || r.EtherType == nil || r.Protocol == nil || r.Min == nil || r.Max == nil || r.Prefix == nil || r.RemoteGroup == nil || *r.RemoteGroup == "" {
			return true
		}
		if (*r.Direction != "ingress" && *r.Direction != "egress") || (*r.EtherType != "IPv4" && *r.EtherType != "IPv6") {
			return true
		}
		protocol := strings.ToLower(*r.Protocol)
		if protocol == "any" && (*r.Min != 0 || *r.Max != 65535) {
			return true
		}
		// Only the observed any/TCP encodings establish the meaning of ports.
		if protocol != "tcp" && protocol != "any" {
			return true
		}
		if *r.Min < 0 || *r.Max > 65535 || *r.Min > *r.Max || (*r.Min == 0 && *r.Max != 65535) {
			return true
		}
		prefix, err := netip.ParsePrefix(*r.Prefix)
		if err != nil || prefix.Addr().Is4In6() || ((*r.EtherType == "IPv4") != prefix.Addr().Is4()) {
			return true
		}
		if *r.Direction == "egress" {
			continue
		}
		span := prefixInterval(prefix)
		for _, port := range []int{22, 234, 3389, 3490} {
			if *r.Min <= port && port <= *r.Max {
				key := fmt.Sprintf("%s/%d", *r.EtherType, port)
				coverage[key] = append(coverage[key], span)
			}
		}
	}
	for _, spans := range coverage {
		if coversAll(spans) {
			return true
		}
	}
	return false
}

func prefixInterval(prefix netip.Prefix) interval {
	prefix = prefix.Masked()
	first := prefix.Addr()
	last := first.As16()
	start := prefix.Bits()
	if first.Is4() {
		start += 96
	}
	for bit := start; bit < 128; bit++ {
		last[bit/8] |= 1 << uint(7-bit%8)
	}
	return interval{first, netip.AddrFrom16(last).Unmap()}
}

func coversAll(spans []interval) bool {
	sort.Slice(spans, func(i, j int) bool { return spans[i].first.Compare(spans[j].first) < 0 })
	if !spans[0].first.IsUnspecified() {
		return false
	}
	high := spans[0].last
	for _, span := range spans[1:] {
		if span.first.Compare(high) > 0 {
			next := high.Next()
			if next.IsValid() && span.first.Compare(next) > 0 {
				return false
			}
		}
		if span.last.Compare(high) > 0 {
			high = span.last
		}
	}
	return !high.Next().IsValid()
}
