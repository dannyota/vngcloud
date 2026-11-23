package network

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"danny.vn/vngcloud"
)

func TestEndpointSelectionRefusals(t *testing.T) {
	r := strings.NewReplacer
	for _, tc := range []struct {
		name  string
		apply func(*epFake)
	}{
		{"zone missing", func(f *epFake) { f.zones = `{"success":true,"data":[{"uuid":"HCM03-1B","isEnabled":true}]}` }},
		{"zone disabled", func(f *epFake) { f.zones = r(`"isEnabled":true`, `"isEnabled":false`).Replace(f.zones) }},
		{"zone duplicate", func(f *epFake) {
			f.zones = `{"success":true,"data":[{"uuid":"HCM03-1C","isEnabled":true},{"uuid":"HCM03-1C","isEnabled":true}]}`
		}},
		{"zones no data", func(f *epFake) { f.zones = `{"success":true}` }},
		{"zones success false", func(f *epFake) { f.zones = r(`"success":true`, `"success":false`).Replace(f.zones) }},
		{"two categories", func(f *epFake) {
			f.categories = `{"success":true,"data":[{"uuid":"` + epCategory + `","name":"a","isDefault":true},{"uuid":"66b50000000000000000000a","name":"b","isDefault":true}]}`
		}},
		{"category not default", func(f *epFake) { f.categories = r(`"isDefault":true`, `"isDefault":false`).Replace(f.categories) }},
		{"category bad id", func(f *epFake) { f.categories = r(epCategory, "abc").Replace(f.categories) }},
		{"service missing", func(f *epFake) { f.services = r("vstorage.hcm03", "other").Replace(f.services) }},
		{"service duplicate gateway", func(f *epFake) { f.services = r("vstorage.han01", "vstorage.hcm03").Replace(f.services) }},
		{"service not gateway", func(f *epFake) {
			f.services = `{"success":true,"data":[{"uuid":"` + epService + `","name":"vstorage.hcm03","endpointType":"Interface"}]}`
		}},
		{"package missing", func(f *epFake) { f.packages = r("Standard", "Premium").Replace(f.packages) }},
		{"package wrong service name", func(f *epFake) {
			f.packages = r(`"serviceName":"vstorage.hcm03"`, `"serviceName":"other"`).Replace(f.packages)
		}},
		{"package duplicate", func(f *epFake) {
			row := `{"uuid":"` + epPackage + `","name":"Standard","serviceName":"vstorage.hcm03"}`
			f.packages = `{"success":true,"data":[` + row + `,` + row + `]}`
		}},
		{"vpc absent", func(f *epFake) { f.vpcs = epList() }},
		{"vpc inactive", func(f *epFake) { f.vpcs = r(`"status":"ACTIVE"`, `"status":"ERROR"`).Replace(f.vpcs) }},
		{"vpc other project", func(f *epFake) {
			f.vpcs = r(`"projectUuid":"`+epProject, `"projectUuid":"pro-00000000-0000-0000-0000-0000000000ff`).Replace(f.vpcs)
		}},
		{"vpc other region", func(f *epFake) {
			f.vpcs = r(`"regionUuid":"`+epRegion, `"regionUuid":"66b5000000000000000000ff`).Replace(f.vpcs)
		}},
		{"vpc other id", func(f *epFake) {
			f.vpcs = r(`"uuid":"`+epVPC, `"uuid":"net-00000000-0000-0000-0000-0000000000ff`).Replace(f.vpcs)
		}},
		{"subnet no data", func(f *epFake) { f.subnets = `{"message":"Success","code":0,"success":true}` }},
		{"subnet other zone", func(f *epFake) {
			f.subnets = r(`"zone":{"uuid":"HCM03-1C"}`, `"zone":{"uuid":"HCM03-1B"}`).Replace(f.subnets)
		}},
		{"subnet other vpc", func(f *epFake) {
			f.subnets = r(`"vpcUuid":"`+epVPC, `"vpcUuid":"net-00000000-0000-0000-0000-0000000000ff`).Replace(f.subnets)
		}},
		{"subnet inactive", func(f *epFake) { f.subnets = r(`"status":"ACTIVE"`, `"status":"ERROR"`).Replace(f.subnets) }},
		{"subnet absent", func(f *epFake) {
			f.subnets = r(`"uuid":"`+epSubnet, `"uuid":"sub-00000000-0000-0000-0000-0000000000ff`).Replace(f.subnets)
		}},
		{"project discovery empty", func(f *epFake) { f.projects = `{"projects":[]}` }},
		{"project ambiguous", func(f *epFake) {
			f.projects = `{"projects":[{"projectId":"` + epProject + `","region":"hcm-3"},{"projectId":"pro-00000000-0000-0000-0000-0000000000ff","region":"hcm-3"}]}`
		}},
		{"project bad form", func(f *epFake) { f.projects = `{"projects":[{"projectId":"abc","region":"hcm-3"}]}` }},
		{"region unmatched", func(f *epFake) { f.regions = `{"success":true,"data":[]}` }},
		{"region bad id", func(f *epFake) { f.regions = r(epRegion, "xyz").Replace(f.regions) }},
		{"duplicate keys", func(f *epFake) {
			f.zones = `{"success":true,"success":true,"data":[{"uuid":"HCM03-1C","isEnabled":true}]}`
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := epCreateFake()
			tc.apply(f)
			h := newEPHarness(t, f)
			out, err := h.c.CreateEndpoint(context.Background(), epCreateInput())
			if err == nil || out != nil || errors.Is(err, ErrNotSettled) {
				t.Fatalf("out=%v err=%v", out, err)
			}
			if f.count(http.MethodPost, epBase) != 0 || f.count(http.MethodPost, "/price") != 0 {
				t.Fatal("wrote or quoted after refusal")
			}
			if _, qerr := h.c.QuoteCreateEndpoint(context.Background(), epCreateInput()); qerr == nil {
				t.Fatal("quote accepted")
			}
		})
	}
}

func TestEndpointConfiguredProjectNarrowsButNeedsMembership(t *testing.T) {
	f := newEPFake()
	f.projects = `{"projects":[{"projectId":"` + epProject + `","region":"hcm-3"},{"projectId":"pro-00000000-0000-0000-0000-0000000000ff","region":"hcm-3"},{"projectId":"pro-00000000-0000-0000-0000-0000000000ee","region":"han-1"}]}`
	h := newEPHarness(t, f, vngcloud.WithProjectID(epProject))
	if _, err := h.c.QuoteCreateEndpoint(context.Background(), epCreateInput()); err != nil {
		t.Fatal(err)
	}
	f = newEPFake()
	h = newEPHarness(t, f, vngcloud.WithProjectID("pro-00000000-0000-0000-0000-0000000000dd"))
	if _, err := h.c.QuoteCreateEndpoint(context.Background(), epCreateInput()); !errors.Is(err, vngcloud.ErrProjectNotFound) {
		t.Fatalf("err=%v", err)
	}
	if f.count(http.MethodGet, "/projects") != 1 {
		t.Fatal("configured project skipped discovery")
	}
}

func TestEndpointCatalogRequests(t *testing.T) {
	f := epCreateFake()
	h := newEPHarness(t, f)
	if _, err := h.c.CreateEndpoint(context.Background(), epCreateInput()); err != nil {
		t.Fatal(err)
	}
	_, _ = h.c.DeleteEndpoint(context.Background(), epDeleteInput())
	empty := `{"search":[],"sort":{},"page":1,"size":1000}`
	for _, tc := range []struct{ suffix, params, extra string }{
		{"/zones", empty, ""},
		{"/categories", empty, ""},
		{"/services", empty, "zoneUuid=HCM03-1C"},
		{"/packages", empty, ""},
		{"/vpcs", `{"search":[{"field":"status","value":"ACTIVE"},{"field":"endpointServiceId","value":"` + epService + `"},{"field":"zoneType","value":"AVAILABILITY"}],"sort":{},"page":1,"size":1000}`, ""},
		{"/subnets", `{"search":[{"field":"isEndpointUsable","value":"true"},{"field":"zoneUuid","value":"HCM03-1C"}],"sort":{},"page":1,"size":1000}`, ""},
		{epBase, empty, ""},
	} {
		found := false
		for _, r := range f.reqs {
			if r.Method != http.MethodGet || !strings.HasSuffix(r.Path, tc.suffix) {
				continue
			}
			q, err := url.ParseQuery(r.Query)
			if err != nil {
				t.Fatal(err)
			}
			found = true
			if q.Get("params") != tc.params {
				t.Errorf("%s params %s", tc.suffix, q.Get("params"))
			}
			want := 1
			if tc.extra != "" {
				want = 2
			}
			if len(q) != want || (tc.extra != "" && q.Encode() != url.Values{"params": {tc.params}, "zoneUuid": {"HCM03-1C"}}.Encode()) {
				t.Errorf("%s query %s", tc.suffix, r.Query)
			}
		}
		if !found {
			t.Errorf("no %s read", tc.suffix)
		}
	}
}

func TestEndpointRefusesBearerReflectedByEarlierStep(t *testing.T) {
	for name, apply := range map[string]func(*epFake){
		"zones": func(f *epFake) { f.zones = strings.Replace(f.zones, `"name":"c"`, `"name":"`+epBearer+`"`, 1) },
		"packages": func(f *epFake) {
			f.packages = strings.Replace(f.packages, `"name":"Standard"`, `"name":"Standard","note":"`+epBearer+`"`, 1)
		},
		"escaped": func(f *epFake) {
			f.categories = strings.Replace(f.categories, `"name":"VNGEndpoint"`, `"name":"bearer-synthetic"`, 1)
		},
		"key": func(f *epFake) { f.vpcs = strings.Replace(f.vpcs, `"status"`, `"`+epBearer+`":1,"status"`, 1) },
		"list": func(f *epFake) {
			f.list = func(int) string { return epList(epRow(epOtherID, epBearer, "ACTIVE", epSubnet)) }
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := epCreateFake()
			apply(f)
			h := newEPHarness(t, f)
			out, err := h.c.CreateEndpoint(context.Background(), epCreateInput())
			if err == nil || out != nil || f.count(http.MethodPost, epBase) != 0 {
				t.Fatalf("out=%v err=%v", out, err)
			}
			if strings.Contains(err.Error(), epBearer) {
				t.Fatalf("leak %v", err)
			}
		})
	}
}

func TestEndpointInventoryPagingRules(t *testing.T) {
	page := func(p, pages, total, size int, ids ...string) string {
		rows := make([]string, 0, len(ids))
		for _, id := range ids {
			rows = append(rows, epRow(id, "n-"+id, "ACTIVE", epSubnet))
		}
		return `{"success":true,"page":` + itoaT(p) + `,"size":` + itoaT(size) + `,"totalPage":` + itoaT(pages) + `,"total":` + itoaT(total) + `,"data":[` + strings.Join(rows, ",") + `]}`
	}
	for _, tc := range []struct {
		name string
		list func(n int) string
		ok   bool
	}{
		{"two pages", func(n int) string {
			if n == 1 {
				return page(1, 2, 2, 1, epOtherID)
			}
			return page(2, 2, 2, 1, "enp-00000000-0000-0000-0000-0000000000c3")
		}, true},
		{"total changes", func(n int) string {
			if n == 1 {
				return page(1, 2, 2, 1, epOtherID)
			}
			return page(2, 2, 3, 1, "enp-00000000-0000-0000-0000-0000000000c3")
		}, false},
		{"duplicate across pages", func(n int) string {
			if n == 1 {
				return page(1, 2, 2, 1, epOtherID)
			}
			return page(2, 2, 2, 1, epOtherID)
		}, false},
		{"page number wrong", func(int) string { return page(2, 1, 1, 1, epOtherID) }, false},
		{"missing metadata", func(int) string { return `{"success":true,"data":[]}` }, false},
		{"total mismatch", func(int) string { return page(1, 1, 5, 1, epOtherID) }, false},
		{"empty with total", func(int) string { return `{"success":true,"page":1,"size":1000,"totalPage":0,"total":1}` }, false},
		{"endless pages", func(n int) string {
			return page(n, 60, 60, 1, "enp-00000000-0000-0000-0000-"+pad12(n))
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newEPFake()
			f.list = tc.list
			h := newEPHarness(t, f)
			_, err := h.c.CreateEndpoint(context.Background(), epCreateInput())
			if tc.ok {
				if !errors.Is(err, ErrNotSettled) {
					t.Fatalf("err=%v", err)
				}
				return
			}
			if err == nil || errors.Is(err, ErrNotSettled) || f.count(http.MethodPost, epBase) != 0 {
				t.Fatalf("err=%v posts=%d", err, f.count(http.MethodPost, epBase))
			}
		})
	}
}

func TestEndpointProjectRegionEvidence(t *testing.T) {
	other := "pro-00000000-0000-0000-0000-0000000000ff"
	for _, tc := range []struct {
		name, projects string
		want           error
	}{
		{"no region field", `{"projects":[{"projectId":"` + epProject + `"}]}`, nil},
		{"empty region", `{"projects":[{"projectId":"` + epProject + `","region":""}]}`, nil},
		{"two without region", `{"projects":[{"projectId":"` + epProject + `"},{"projectId":"` + other + `"}]}`, vngcloud.ErrProjectAmbiguous},
		{"other region skipped", `{"projects":[{"projectId":"` + epProject + `"},{"projectId":"` + other + `","region":"han-1"}]}`, nil},
		{"only other region", `{"projects":[{"projectId":"` + other + `","region":"han-1"}]}`, vngcloud.ErrProjectNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newEPFake()
			f.projects = tc.projects
			h := newEPHarness(t, f)
			_, err := h.c.QuoteCreateEndpoint(context.Background(), epCreateInput())
			if !errors.Is(err, tc.want) && (tc.want != nil || err != nil) {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestEndpointRegionProjection(t *testing.T) {
	row := func(extra string) string {
		return `{"success":true,"data":[{"uuid":"` + epRegion + `","name":"HCM","code":"hcm-3","gatewayUrl":"https://hcm-3-vnetwork.console.greennode.ai/vnetwork-gateway","vnetworkDashboard":"https://hcm-3-vnetwork.console.greennode.ai","VServerEndpoint":"x","VLBEndpoint":"y"` + extra + `}]}`
	}
	for _, tc := range []struct {
		name, regions string
		ok            bool
	}{
		{"pascal extra keys ignored", row(``), true},
		{"duplicate cased used key", row(`,"GatewayUrl":"https://hcm-3-vnetwork.console.greennode.ai/vnetwork-gateway"`), false},
		{"duplicate key", row(`,"uuid":"` + epRegion + `"`), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newEPFake()
			f.regions = tc.regions
			h := newEPHarness(t, f)
			_, err := h.c.QuoteCreateEndpoint(context.Background(), epCreateInput())
			if (err == nil) != tc.ok {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestEndpointRowProjectScopeUsesVServerProjectID(t *testing.T) {
	good := epRow(epOtherID, "other", "ACTIVE", epSubnet)
	for name, tc := range map[string]struct {
		row string
		ok  bool
	}{
		"live shape, vpc.projectUuid null": {good, true},
		"project key absent":               {strings.Replace(good, `"VServerProjectId"`, `"x"`, 1), false},
		"project id null":                  {strings.Replace(good, `"VServerProjectId":"`+epProject+`"`, `"VServerProjectId":null`, 1), false},
		"other project":                    {strings.Replace(good, `"VServerProjectId":"`+epProject, `"VServerProjectId":"pro-00000000-0000-0000-0000-0000000000ff`, 1), false},
		"project object missing":           {strings.Replace(good, `"project":{`, `"projectx":{`, 1), false},
		"vpc absent is fine":               {strings.Replace(good, `"vpc":{"uuid":"`+epVPC+`","projectUuid":null,"project":null,"projectId":"backend-1"},`, ``, 1), true},
	} {
		t.Run(name, func(t *testing.T) {
			f := newEPFake()
			f.list = func(int) string { return epList(tc.row) }
			h := newEPHarness(t, f)
			_, err := h.c.CreateEndpoint(context.Background(), epCreateInput())
			if tc.ok != errors.Is(err, ErrNotSettled) {
				t.Fatalf("err=%v", err)
			}
		})
	}
}
