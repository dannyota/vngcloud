package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"danny.vn/vngcloud/compute"
	"danny.vn/vngcloud/pricing"
)

func TestFloatingIPCommandFlagsAndErrorMapping(t *testing.T) {
	cmd := newComputeCmd(&env{})
	for _, name := range []string{"create-server", "quote-create-server"} {
		sub, _, err := cmd.Find([]string{name})
		if err != nil {
			t.Fatal(err)
		}
		if sub.Flags().Lookup("attach-floating-ip") == nil {
			t.Fatalf("%s missing opt-in", name)
		}
		if (sub.Flags().Lookup("allow-open-login") != nil) != (name == "create-server") {
			t.Fatalf("%s override flag", name)
		}
	}
	if got := classify(compute.ErrOpenLogin); got.Code != "OpenLogin" || exitCode(compute.ErrOpenLogin) != 1 {
		t.Fatalf("mapping=%+v", got)
	}
}

func TestFloatingIPCLIQuoteAndGuard(t *testing.T) {
	for _, tc := range []struct {
		name                            string
		quote, open, override, readonly bool
		cap                             string
		code                            string
		orders                          int
	}{
		{name: "quote", quote: true},
		{name: "low cap", cap: "347799", code: "PriceAboveMax"},
		{name: "exact cap", cap: "347800", orders: 1},
		{name: "open login", open: true, cap: "347800", code: "OpenLogin"},
		{name: "override", open: true, override: true, cap: "347800", orders: 1},
		{name: "readonly", readonly: true, cap: "347800", code: "ReadOnly"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			orders, quotes, groups := 0, 0, 0
			prefix := "192.0.2.0/24"
			if tc.open {
				prefix = "0.0.0.0/0"
			}
			routes := map[string]func(http.ResponseWriter, *http.Request){
				"/v2/proj-1/servers": func(w http.ResponseWriter, r *http.Request) {
					if r.Method == "GET" {
						_, _ = w.Write([]byte(computeEmptyServerListJSON))
						return
					}
					orders++
					var body map[string]any
					_ = json.NewDecoder(r.Body).Decode(&body)
					if body["attachFloating"] != true {
						t.Error("opt-in missing")
					}
					if _, ok := body["allowOpenLogin"]; ok {
						t.Error("override on wire")
					}
					w.WriteHeader(202)
					_, _ = w.Write([]byte(`{"data":{"uuid":"server-1"}}`))
				},
				"/v1/price": func(w http.ResponseWriter, r *http.Request) {
					quotes++
					var body struct {
						ResourceType string `json:"resourceType"`
					}
					_ = json.NewDecoder(r.Body).Decode(&body)
					if body.ResourceType != "server" {
						t.Errorf("unexpected quote resource %q", body.ResourceType)
					}
					_, _ = w.Write([]byte(`{"optimumPrice":347800,"originalPrice":347800,"discountPrice":0,"discountPercent":0,"propertiesPrice":[]}`))
				},
			}
			for _, id := range []string{"sg-1", "sg-2"} {
				routes["/v2/proj-1/secgroups/"+id] = func(w http.ResponseWriter, _ *http.Request) {
					groups++
					_, _ = fmt.Fprintf(w, `{"data":{"id":%q}}`, id)
				}
				routes["/v2/proj-1/secgroups/"+id+"/secGroupRules"] = func(w http.ResponseWriter, _ *http.Request) {
					groups++
					_, _ = fmt.Fprintf(w, `{"data":[{"id":"rule-1","direction":"ingress","etherType":"IPv4","protocol":"TCP","portRangeMin":234,"portRangeMax":234,"remoteIpPrefix":%q,"remoteGroupId":"sg-1"}]}`, prefix)
				}
			}
			fixture := newSvcFixture(routes)
			root, stdout, stderr := newSvcRoot(t, fixture)
			args := append([]string{"--region", "hcm-3", "--project-id", "proj-1"}, validCreateServerArgs...)
			if tc.quote {
				args = append([]string{"--region", "hcm-3", "--project-id", "proj-1"}, validQuoteCreateServerArgs...)
			} else {
				args = append(args, "--max-price", tc.cap, "--no-wait")
			}
			args = append(args, "--attach-floating-ip")
			if tc.override {
				args = append(args, "--allow-open-login")
			}
			if tc.readonly {
				args = append(args, "--read-only")
			}
			root.SetArgs(args)
			err := root.ExecuteContext(context.Background())
			if tc.code != "" {
				if err == nil || classify(err).Code != tc.code {
					t.Fatalf("err=%v stderr=%s", err, stderr)
				}
			} else if err != nil {
				t.Fatalf("err=%v stderr=%s", err, stderr)
			}
			if orders != tc.orders {
				t.Fatalf("orders=%d want=%d", orders, tc.orders)
			}
			if tc.quote {
				if groups != 0 || quotes != 1 || !strings.Contains(stdout.String(), "347800") {
					t.Fatalf("quote groups=%d quotes=%d stdout=%s", groups, quotes, stdout)
				}
				var out pricing.GetQuoteOutput
				if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
					t.Fatal(err)
				}
				if len(out.Properties) != 1 || out.Properties[0] != (pricing.PriceProperty{Name: "FLOATING IP", Description: "Included in server order for the verified configuration"}) {
					t.Fatalf("properties=%+v", out.Properties)
				}
			}
			if tc.orders != 0 {
				var out compute.CreateServerOutput
				if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
					t.Fatal(err)
				}
				if quotes != 1 || out.MonthlyPrice != 347800 {
					t.Fatalf("quotes=%d output=%+v", quotes, out)
				}
			}
			if tc.readonly && fixture.requestCount() != 0 {
				t.Fatal("read-only sent request")
			}
		})
	}
}

func TestGoldenComputeFloatingIP(t *testing.T) {
	quote := &pricing.GetQuoteOutput{OptimumPrice: 347800, OriginalPrice: 347800, Properties: []pricing.PriceProperty{
		{Name: "INSTANCE TYPE", OptimumPrice: 283800, MonthlyPrice: 283800},
		{Name: "ROOT DISK", OptimumPrice: 64000, MonthlyPrice: 64000},
		{Name: "FLOATING IP", Description: "Included in server order for the verified configuration"},
	}}
	create := &compute.CreateServerOutput{Server: compute.Server{UUID: "server-1", Name: "web-1", Status: "ACTIVE", PublicIP: "192.0.2.1"}, MonthlyPrice: 347800}
	for _, format := range []string{"json", "text", "table"} {
		checkGolden(t, "compute-quote-create-server-floating-ip."+format+".golden", format, "", quote)
		checkGolden(t, "compute-create-server-floating-ip."+format+".golden", format, "", create)
	}
	checkGolden(t, "compute-create-server-floating-ip-query.text.golden", "text", "Server.PublicIP", create)
}

func TestFloatingIPErrorKeepsSentinel(t *testing.T) {
	err := fmt.Errorf("refused: %w", compute.ErrOpenLogin)
	if !errors.Is(err, compute.ErrOpenLogin) || classify(err).Code != "OpenLogin" {
		t.Fatal(err)
	}
}
