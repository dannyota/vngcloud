package compute

import (
	"encoding/json"
	"strings"
	"testing"
)

func publicIPValue(t *testing.T, server Server) string {
	t.Helper()
	return server.PublicIP
}

// Positive addresses are schema-built; invalid inputs are synthetic.
func TestServerPublicIPDecode(t *testing.T) {
	for _, tc := range []struct {
		name string
		ips  []string
		want string
	}{
		{"internal", []string{"192.0.2.1"}, "192.0.2.1"},
		{"ipv6 canonical", []string{"2001:0DB8:0:0:0:0:0:1"}, "2001:db8::1"},
		{"empty", nil, ""},
		{"empty then address", []string{"", "192.0.2.1"}, "192.0.2.1"},
		{"equal", []string{"192.0.2.1", "192.0.2.1"}, "192.0.2.1"},
		{"mapped equal", []string{"::ffff:192.0.2.1", "192.0.2.1"}, "192.0.2.1"},
		{"distinct", []string{"192.0.2.1", "198.51.100.1"}, ""},
		{"invalid", []string{"192.0.2.1", "not-an-address"}, ""},
		{"whitespace", []string{" 192.0.2.1"}, ""},
		{"trailing whitespace", []string{"192.0.2.1 "}, ""},
		{"control", []string{"192.0.2.1\n"}, ""},
		{"nul", []string{"192.0.2.1\x00"}, ""},
		{"scope", []string{"2001:db8::1%eth0"}, ""},
		{"private", []string{"10.0.0.1"}, ""},
		{"private mapped", []string{"::ffff:10.0.0.1"}, ""},
		{"private ipv6", []string{"fd00::1"}, ""},
		{"loopback", []string{"127.0.0.1"}, ""},
		{"loopback ipv6", []string{"::1"}, ""},
		{"link local", []string{"169.254.1.1"}, ""},
		{"link local ipv6", []string{"fe80::1"}, ""},
		{"multicast", []string{"224.0.0.1"}, ""},
		{"multicast ipv6", []string{"ff02::1"}, ""},
		{"unspecified", []string{"0.0.0.0"}, ""},
		{"unspecified ipv6", []string{"::"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			interfaces := make([]map[string]string, len(tc.ips))
			for i, ip := range tc.ips {
				interfaces[i] = map[string]string{"floatingIp": ip, "floatingIpId": "wan-1", "uuid": "nic-1", "fixedIp": "192.0.2.2"}
			}
			raw, err := json.Marshal(map[string]any{"name": "web-1", "internalInterfaces": interfaces, "publicIp": "203.0.113.1"})
			if err != nil {
				t.Fatal(err)
			}
			var server Server
			if err := json.Unmarshal(raw, &server); err != nil {
				t.Fatal(err)
			}
			if got := publicIPValue(t, server); got != tc.want {
				t.Fatalf("PublicIP = %q, want %q", got, tc.want)
			}
			if len(server.InternalInterfaces) != len(tc.ips) {
				t.Fatal("interfaces changed")
			}
			for i, nic := range server.InternalInterfaces {
				if nic.FloatingIP != tc.ips[i] || nic.FloatingIPID != "wan-1" || nic.UUID != "nic-1" || nic.FixedIP != "192.0.2.2" {
					t.Fatal("interface fields changed")
				}
			}
			encoded, err := json.Marshal(server)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(encoded), "publicIp") || strings.Contains(string(encoded), "PublicIP") || !strings.Contains(string(encoded), `"name":"web-1"`) {
				t.Fatal("ordinary JSON tags changed")
			}
		})
	}
}

func TestServerPublicIPExternalAndRepeatedDecode(t *testing.T) {
	var server Server
	for _, tc := range []struct{ raw, want string }{
		{`{"internalInterfaces":[{"floatingIp":"192.0.2.1"}]}`, "192.0.2.1"},
		{`{"publicIp":"203.0.113.1","externalInterfaces":[{"floatingIp":"198.51.100.1"}]}`, ""},
		{`{"internalInterfaces":[{"floatingIp":"::ffff:192.0.2.2"}]}`, "192.0.2.2"},
		{`null`, ""},
	} {
		if err := json.Unmarshal([]byte(tc.raw), &server); err != nil {
			t.Fatal(err)
		}
		if got := publicIPValue(t, server); got != tc.want {
			t.Fatalf("PublicIP = %q, want %q", got, tc.want)
		}
	}
}
