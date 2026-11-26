package network

import (
	"context"
	"net/http"
	"os"
	"strings"
	"testing"
)

func TestNATInboundRulesHCMIDs(t *testing.T) {
	body, err := os.ReadFile("../testdata/network/list_nat_inbound_rules_hcm_shape.json")
	if err != nil {
		t.Fatal(err)
	}
	c := natTestClient(t, "hcm-3", "project-1", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(body)
	}))
	out, err := c.ListNATInboundRules(context.Background(), &ListNATInboundRulesInput{NATID: "nat-1", ZoneID: "zone-1"})
	if err != nil || out == nil || len(out.Items) != 7 {
		t.Fatalf("out=%+v err=%v", out, err)
	}
	if out.Items[0].ID != "rule_00000000-0000-4000-8000-000000000001" {
		t.Fatalf("unexpected ID: %q", out.Items[0].ID)
	}
}

func TestNATInboundRulesRejectUnsafeIDs(t *testing.T) {
	for _, id := range []string{"", "rule/1", "rule.1"} {
		t.Run(id, func(t *testing.T) {
			body := strings.Replace(`{"success":true,"data":[{"uuid":"ID","protocol":"tcp","etherType":"IPv4","direction":"ingress","isSystem":true,"portRangeMin":1,"portRangeMax":2}]}`, "ID", id, 1)
			c := natTestClient(t, "hcm-3", "project-1", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(body))
			}))
			out, err := c.ListNATInboundRules(context.Background(), &ListNATInboundRulesInput{NATID: "nat-1", ZoneID: "zone-1"})
			if err == nil || out != nil {
				t.Fatalf("out=%+v err=%v", out, err)
			}
		})
	}
}
