package cli

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestNetworkNATDetailReads(t *testing.T) {
	for _, tc := range []struct{ name, suffix, body string }{
		{"get-nat-instance", "", `{"success":true,"data":{"uuid":"nat-1","status":"ACTIVE","natName":"edge","natGatewayIp":"10.0.0.1","publicIp":"203.0.113.1","createdAt":"today","natPackage":{"uuid":"pkg-1","name":"small"},"vpc":{"uuid":"vpc-1","name":"private"}}}`},
		{"list-nat-inbound-rules", "/sec-groups/rules", `{"success":true,"data":[{"uuid":"rule-1","protocol":"icmp","etherType":"IPv4","direction":"ingress","portRangeMin":null,"portRangeMax":null,"isSystem":true}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			body := tc.body
			run := natCLI(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != http.MethodGet || r.URL.RequestURI() != "/vnetwork/v1/zone-1/proj-1/nats/nat-1"+tc.suffix {
					t.Errorf("request=%s %s", r.Method, r.URL.RequestURI())
				}
				jsonHandler(200, body)(w, r)
			}))
			args := []string{"--profile", "agent", "--read-only", "network", tc.name, "--zone-id", "zone-1"}
			code, out, stderr := run(args...)
			if code != 2 || out != "" || calls != 0 {
				t.Fatalf("missing ID: exit=%d out=%s stderr=%s calls=%d", code, out, stderr, calls)
			}
			code, out, stderr = run(append(args, "--nat-id", "nat-1")...)
			if code != 0 || calls != 1 {
				t.Fatalf("exit=%d out=%s stderr=%s calls=%d", code, out, stderr, calls)
			}
			var result map[string]json.RawMessage
			if json.Unmarshal([]byte(out), &result) != nil {
				t.Fatalf("output=%s", out)
			}
			if tc.suffix == "" {
				var nat struct{ ID string }
				if json.Unmarshal(result["NAT"], &nat) != nil || nat.ID != "nat-1" {
					t.Fatalf("output=%s", out)
				}
			} else {
				var items []map[string]any
				if len(result) != 1 || json.Unmarshal(result["Items"], &items) != nil || len(items) != 1 || items[0]["ID"] != "rule-1" || items[0]["PortRangeMin"] != nil || items[0]["PortRangeMax"] != nil {
					t.Fatalf("output=%s", out)
				}
			}
			if tc.suffix != "" {
				body = `{"success":true,"data":[]}`
				code, out, stderr = run(append(args, "--nat-id", "nat-1")...)
				var empty map[string]json.RawMessage
				if code != 0 || json.Unmarshal([]byte(out), &empty) != nil || len(empty) != 1 || string(empty["Items"]) != "[]" {
					t.Fatalf("empty: exit=%d out=%s stderr=%s", code, out, stderr)
				}
			}
			for _, invalid := range []string{`{"success":true,"data":null}`, `{"success":`} {
				body = invalid
				code, out, stderr = run(append(args, "--nat-id", "nat-1")...)
				if code != 1 || out != "" || !strings.Contains(stderr, "InvalidResponse") {
					t.Fatalf("invalid: exit=%d out=%s stderr=%s", code, out, stderr)
				}
			}
		})
	}
}
