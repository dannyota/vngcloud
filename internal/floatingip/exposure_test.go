package floatingip

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
)

func syntheticRule(direction, ether, protocol string, minPort, maxPort int, prefix string) json.RawMessage {
	return json.RawMessage(fmt.Sprintf(`{"id":"rule-1","direction":%q,"etherType":%q,"protocol":%q,"portRangeMin":%d,"portRangeMax":%d,"remoteIpPrefix":%q,"remoteGroupId":"sg-1","status":"ACTIVE","description":"","createdAt":""}`, direction, ether, protocol, minPort, maxPort, prefix))
}

func TestExposureUnionAndObservedEncodings(t *testing.T) {
	for _, tc := range []struct {
		name  string
		rules []json.RawMessage
		open  bool
	}{
		{"CIDR with remote group metadata", []json.RawMessage{syntheticRule("ingress", "IPv4", "TCP", 234, 234, "0.0.0.0/0")}, true},
		{"IPv6 any all ports", []json.RawMessage{syntheticRule("ingress", "IPv6", "any", 0, 65535, "::/0")}, true},
		{"two halves", []json.RawMessage{syntheticRule("ingress", "IPv4", "tcp", 22, 234, "0.0.0.0/1"), syntheticRule("ingress", "IPv4", "TCP", 234, 234, "128.0.0.0/1")}, true},
		{"masked prefix", []json.RawMessage{syntheticRule("ingress", "IPv4", "tcp", 3389, 3490, "192.0.2.1/0")}, true},
		{"overlap leaves hole", []json.RawMessage{syntheticRule("ingress", "IPv4", "tcp", 22, 22, "0.0.0.0/2"), syntheticRule("ingress", "IPv4", "tcp", 22, 22, "0.0.0.0/1")}, false},
		{"families independent", []json.RawMessage{syntheticRule("ingress", "IPv4", "tcp", 22, 22, "0.0.0.0/1"), syntheticRule("ingress", "IPv6", "tcp", 22, 22, "8000::/1")}, false},
		{"narrow", []json.RawMessage{syntheticRule("ingress", "IPv4", "tcp", 22, 3490, "192.0.2.0/24")}, false},
		{"HTTP", []json.RawMessage{syntheticRule("ingress", "IPv4", "tcp", 80, 443, "0.0.0.0/0")}, true},
		{"HTTP only", []json.RawMessage{syntheticRule("ingress", "IPv4", "tcp", 80, 80, "0.0.0.0/0")}, false},
		{"egress", []json.RawMessage{syntheticRule("egress", "IPv4", "any", 0, 65535, "0.0.0.0/0")}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := openLogin(tc.rules); got != tc.open {
				t.Fatalf("open=%v, want %v", got, tc.open)
			}
		})
	}
}

func TestExposureRejectsUnobservedAndMalformedShapes(t *testing.T) {
	good := string(syntheticRule("ingress", "IPv4", "tcp", 80, 80, "192.0.2.0/24"))
	for _, pair := range [][2]string{
		{`"tcp"`, `null`}, {`"tcp"`, `"gre"`}, {`"IPv4"`, `"IP"`}, {`"ingress"`, `"unknown"`},
		{`"portRangeMin":80`, `"portRangeMin":null`}, {`"portRangeMax":80`, `"portRangeMax":"80"`},
		{`"portRangeMin":80`, `"portRangeMin":81`}, {`"portRangeMax":80`, `"portRangeMax":65536`},
		{`"192.0.2.0/24"`, `null`}, {`"192.0.2.0/24"`, `""`}, {`"192.0.2.0/24"`, `"bad"`},
		{`"portRangeMin":80,`, ``}, {`"remoteGroupId":"sg-1"`, `"remoteGroupId":null`},
	} {
		if !openLogin([]json.RawMessage{json.RawMessage(strings.Replace(good, pair[0], pair[1], 1))}) {
			t.Fatalf("accepted %v", pair)
		}
	}
}

func TestSyntheticRuleFixtureRetainsObservedShapes(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/network/security_group_rules_floating_ip.json")
	if err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		Data []json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope.Data) != 7 || !openLogin(envelope.Data) {
		t.Fatalf("fixture failed: %d rules", len(envelope.Data))
	}
}

func TestExposureRefusesUnobservedAnyPortCombination(t *testing.T) {
	if !openLogin([]json.RawMessage{syntheticRule("ingress", "IPv4", "any", 80, 80, "0.0.0.0/0")}) {
		t.Fatal("unobserved any-protocol port semantics treated as restricted")
	}
}
