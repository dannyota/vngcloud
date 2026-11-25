package network

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/endpoints"
	"danny.vn/vngcloud/internal/transport"
)

func TestNATDetailReads(t *testing.T) {
	c := natTestClient(t, "hcm-3", "project-1", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/vnetwork-gateway/vnetwork/v1/zone-1/project-1/nats/nat-1":
			_, _ = w.Write([]byte(`{"success":true,"data":{"uuid":"nat-1","status":"ACTIVE","vpc":{"uuid":"vpc-1"},"natPackage":{"uuid":"pkg-1"}}}`))
		case "/vnetwork-gateway/vnetwork/v1/zone-1/project-1/nats/nat-1/sec-groups/rules":
			_, _ = w.Write([]byte(`{"success":true,"data":[{"uuid":"rule-1","protocol":"tcp","etherType":"IPv4","direction":"ingress","isSystem":true,"portRangeMin":null,"portRangeMax":80}]}`))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	detail, err := c.GetNATInstance(context.Background(), &GetNATInstanceInput{NATID: "nat-1", ZoneID: "zone-1"})
	if err != nil || detail == nil || detail.NAT.ID != "nat-1" {
		t.Fatalf("detail=%+v err=%v", detail, err)
	}
	rules, err := c.ListNATInboundRules(context.Background(), &ListNATInboundRulesInput{NATID: "nat-1", ZoneID: "zone-1"})
	if err != nil || rules == nil || len(rules.Items) != 1 || rules.Items[0].PortRangeMin != nil {
		t.Fatalf("rules=%+v err=%v", rules, err)
	}
}

func TestNATReadFixtures(t *testing.T) {
	for _, region := range []string{"hcm-3", "han-1"} {
		for _, rules := range []bool{false, true} {
			name := "get_nat_instance.json"
			if rules {
				name = "list_nat_inbound_rules.json"
			}
			body, err := os.ReadFile("../testdata/network/" + name)
			if err != nil {
				t.Fatal(err)
			}
			c := natTestClient(t, region, "project-1", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.RawQuery != "" {
					t.Errorf("unexpected request %s %s", r.Method, r.URL.String())
				}
				_, _ = w.Write(body)
			}))
			if rules {
				out, err := c.ListNATInboundRules(context.Background(), &ListNATInboundRulesInput{NATID: "nat-1", ZoneID: "zone-1"})
				if err != nil || out == nil || len(out.Items) != 2 || !out.Items[1].System || *out.Items[1].PortRangeMin != 443 {
					t.Fatalf("%+v %v", out, err)
				}
			} else {
				out, err := c.GetNATInstance(context.Background(), &GetNATInstanceInput{NATID: "nat-1", ZoneID: "zone-1"})
				if err != nil || out == nil || out.NAT.Name != "example" || out.NAT.VPCID != "vpc-1" {
					t.Fatalf("%+v %v", out, err)
				}
			}
		}
	}
}

func TestNATReadInvalidBodies(t *testing.T) {
	detail := `{"success":true,"data":{"uuid":"nat-1","status":"ACTIVE","vpc":{"uuid":"vpc-1"},"natPackage":{"uuid":"pkg-1"}}}`
	rules := `{"success":true,"data":[{"uuid":"rule-1","protocol":"tcp","etherType":"IPv4","direction":"ingress","isSystem":true,"portRangeMin":null,"portRangeMax":80}]}`
	for _, tc := range []struct {
		body  string
		rules bool
	}{
		{strings.Replace(detail, `"success":true`, `"success":false`, 1), false},
		{strings.Replace(detail, `"uuid":"nat-1"`, `"uuid":"other"`, 1), false},
		{strings.Replace(detail, `"status":"ACTIVE"`, `"Status":"ACTIVE"`, 1), false},
		{strings.Replace(detail, `"uuid":"vpc-1"`, `"uuid":"../bad"`, 1), false},
		{strings.Replace(detail, `"status":"ACTIVE"`, `"status":null`, 1), false},
		{strings.Replace(detail, `"status":"ACTIVE"`, `"status":"ACTIVE","status":"ACTIVE"`, 1), false},
		{strings.Replace(rules, `"portRangeMin":null,`, ``, 1), true},
		{strings.Replace(rules, `"portRangeMax":80`, `"portRangeMax":"80"`, 1), true},
		{strings.Replace(rules, `"isSystem":true`, `"isSystem":null`, 1), true},
		{`{"success":true,"data":null}`, true},
		{`{"success":true}`, true},
	} {
		c := natTestClient(t, "hcm-3", "project-1", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(tc.body)) }))
		var err error
		if tc.rules {
			var out *ListNATInboundRulesOutput
			out, err = c.ListNATInboundRules(context.Background(), &ListNATInboundRulesInput{NATID: "nat-1", ZoneID: "zone-1"})
			if out != nil {
				t.Error("non-nil output")
			}
		} else {
			var out *GetNATInstanceOutput
			out, err = c.GetNATInstance(context.Background(), &GetNATInstanceInput{NATID: "nat-1", ZoneID: "zone-1"})
			if out != nil {
				t.Error("non-nil output")
			}
		}
		if vngcloud.ErrorCode(err) != "InvalidResponse" {
			t.Errorf("body %s: %v", tc.body, err)
		}
	}
}

func TestNATReadInputAndStatus(t *testing.T) {
	for _, rules := range []bool{false, true} {
		call := func(c *Client, id string) error {
			if rules {
				_, err := c.ListNATInboundRules(context.Background(), &ListNATInboundRulesInput{NATID: id, ZoneID: "zone-1"})
				return err
			}
			_, err := c.GetNATInstance(context.Background(), &GetNATInstanceInput{NATID: id, ZoneID: "zone-1"})
			return err
		}
		for _, region := range []string{"hcm-3", "unsupported"} {
			calls := 0
			c := natTestClient(t, region, "project-1", http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls++ }))
			id := "nat-1"
			want := vngcloud.ErrInvalidConfig
			if region == "hcm-3" {
				id = "../secret"
				want = vngcloud.ErrInvalidInput
			}
			err := call(c, id)
			if !errors.Is(err, want) || calls != 0 || strings.Contains(err.Error(), "../secret") {
				t.Fatalf("%v calls %d", err, calls)
			}
		}
		for _, status := range []int{401, 403, 404, 429, 500, 503} {
			c := natTestClient(t, "hcm-3", "project-1", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(status)
				_, _ = w.Write([]byte(`{"code":"peer-code","message":"secret"}`))
			}))
			err := call(c, "nat-1")
			var api *vngcloud.APIError
			if !errors.As(err, &api) || api.StatusCode != status || api.Code == "peer-code" || strings.Contains(err.Error(), "secret") {
				t.Fatalf("status %d: %v", status, err)
			}
		}
	}
}

func TestNATReadCredentialAfterRefresh(t *testing.T) {
	for _, rules := range []bool{false, true} {
		var calls atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if calls.Add(1) == 1 {
				w.WriteHeader(401)
				return
			}
			token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			if rules {
				_, _ = fmt.Fprintf(w, `{"success":true,"data":[{"uuid":"rule-1","protocol":"tcp","etherType":"IPv4","direction":"ingress","isSystem":true,"portRangeMin":null,"portRangeMax":80,"description":%q}]}`, token)
			} else {
				_, _ = fmt.Fprintf(w, `{"success":true,"data":{"uuid":"nat-1","natName":%q,"status":"ACTIVE","vpc":{"uuid":"vpc-1"},"natPackage":{"uuid":"pkg-1"}}}`, token)
			}
		}))
		tc := transport.New(transport.Config{HTTPClient: server.Client(), TokenSource: &rotatingNATTokens{}})
		c := New(core.NewTestConfig("hcm-3", "project-1", endpoints.Set{VServer: server.URL + "/", VNetwork: server.URL + "/"}, tc))
		var err error
		if rules {
			_, err = c.ListNATInboundRules(context.Background(), &ListNATInboundRulesInput{NATID: "nat-1", ZoneID: "zone-1"})
		} else {
			_, err = c.GetNATInstance(context.Background(), &GetNATInstanceInput{NATID: "nat-1", ZoneID: "zone-1"})
		}
		server.Close()
		if calls.Load() != 2 || vngcloud.ErrorCode(err) != "InvalidResponse" {
			t.Fatalf("calls %d error %v", calls.Load(), err)
		}
	}
}

func TestNATReadOversizeAndRedirect(t *testing.T) {
	for _, rules := range []bool{false, true} {
		for _, redirect := range []bool{false, true} {
			calls := 0
			destination := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls++ }))
			c := natTestClient(t, "hcm-3", "project-1", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if redirect {
					http.Redirect(w, r, destination.URL, http.StatusFound)
					return
				}
				_, _ = w.Write([]byte(strings.Repeat("x", natMaxBody+1)))
			}))
			var err error
			if rules {
				_, err = c.ListNATInboundRules(context.Background(), &ListNATInboundRulesInput{NATID: "nat-1", ZoneID: "zone-1"})
			} else {
				_, err = c.GetNATInstance(context.Background(), &GetNATInstanceInput{NATID: "nat-1", ZoneID: "zone-1"})
			}
			destination.Close()
			if err == nil || calls != 0 || (!redirect && !errors.Is(err, transport.ErrBodyTooLarge)) {
				t.Fatalf("redirect=%v calls=%d err=%v", redirect, calls, err)
			}
		}
	}
}

func TestNATReadRegionDiscovery(t *testing.T) {
	for _, region := range []string{"hcm-3", "han-1"} {
		for _, rules := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/rules=%t", region, rules), func(t *testing.T) {
				calls := 0
				c := natTestClient(t, region, "project-1", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls++
					if r.Method != http.MethodGet || r.URL.RawQuery != "" {
						t.Errorf("unexpected request %s %s", r.Method, r.URL.String())
					}
					switch r.URL.Path {
					case "/vnetwork-gateway/vnetwork/v1/regions":
						_, _ = fmt.Fprintf(w, `{"success":true,"data":[{"uuid":"region-1","vnetworkDashboard":%q}]}`, natRegionalOrigin(region))
					case "/vnetwork-gateway/vnetwork/v1/region-1/project-1/nats/nat-1":
						_, _ = w.Write([]byte(`{"success":true,"data":{"uuid":"nat-1","status":"ACTIVE","vpc":{"uuid":"vpc-1"},"natPackage":{"uuid":"pkg-1"}}}`))
					case "/vnetwork-gateway/vnetwork/v1/region-1/project-1/nats/nat-1/sec-groups/rules":
						_, _ = w.Write([]byte(`{"success":true,"data":[]}`))
					default:
						t.Errorf("unexpected path %s", r.URL.Path)
					}
				}))
				var err error
				if rules {
					_, err = c.ListNATInboundRules(context.Background(), &ListNATInboundRulesInput{NATID: "nat-1"})
				} else {
					_, err = c.GetNATInstance(context.Background(), &GetNATInstanceInput{NATID: "nat-1"})
				}
				if err != nil || calls != 2 {
					t.Fatalf("calls=%d error=%v", calls, err)
				}
			})
		}
	}
}

func TestNATInboundDuplicateID(t *testing.T) {
	body, err := os.ReadFile("../testdata/network/list_nat_inbound_rules.json")
	if err != nil {
		t.Fatal(err)
	}
	body = []byte(strings.Replace(string(body), `"uuid":"rule-2"`, `"uuid":"rule-1"`, 1))
	c := natTestClient(t, "hcm-3", "project-1", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(body) }))
	out, err := c.ListNATInboundRules(context.Background(), &ListNATInboundRulesInput{NATID: "nat-1", ZoneID: "region-1"})
	if out != nil || vngcloud.ErrorCode(err) != "InvalidResponse" {
		t.Fatalf("output=%+v error=%v", out, err)
	}
}

func TestNATReadEveryRetainedStringReflectsCredential(t *testing.T) {
	for _, tc := range []struct {
		file   string
		rules  bool
		values []string
	}{
		{"get_nat_instance.json", false, []string{`"uuid":"nat-1"`, `"natName":"example"`, `"status":"ACTIVE"`, `"natGatewayIp":"192.0.2.1"`, `"publicIp":"198.51.100.1"`, `"createdAt":"example-time"`, `"uuid":"pkg-1"`, `"name":"small"`, `"uuid":"vpc-1"`, `"name":"example-vpc"`}},
		{"list_nat_inbound_rules.json", true, []string{`"uuid":"rule-1"`, `"protocol":"tcp"`, `"etherType":"IPv4"`, `"direction":"ingress"`, `"description":"web"`, `"createdAt":"example-time"`}},
	} {
		fixture, err := os.ReadFile("../testdata/network/" + tc.file)
		if err != nil {
			t.Fatal(err)
		}
		for _, value := range tc.values {
			t.Run(value, func(t *testing.T) {
				key, _, _ := strings.Cut(value, ":")
				body := strings.Replace(string(fixture), value, key+`:"test-token"`, 1)
				id := "nat-1"
				if value == `"uuid":"nat-1"` {
					id = "test-token"
				}
				c := natTestClient(t, "hcm-3", "project-1", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) }))
				var err error
				if tc.rules {
					_, err = c.ListNATInboundRules(context.Background(), &ListNATInboundRulesInput{NATID: id, ZoneID: "zone-1"})
				} else {
					_, err = c.GetNATInstance(context.Background(), &GetNATInstanceInput{NATID: id, ZoneID: "zone-1"})
				}
				if vngcloud.ErrorCode(err) != "InvalidResponse" {
					t.Fatalf("%v", err)
				}
			})
		}
	}
}
