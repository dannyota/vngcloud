package network

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"danny.vn/vngcloud"
)

func epActiveList(n int) string {
	if n < 4 {
		return epList(epRow(epID, "demo", "PROVISIONING", epSubnet))
	}
	return epList(epRow(epID, "demo", "ACTIVE", epSubnet))
}

func epCreateFake() *epFake {
	f := newEPFake()
	created := false
	f.onCreate = func() { created = true }
	f.list = func(n int) string {
		if !created {
			return epList()
		}
		return epActiveList(n)
	}
	return f
}

func decodeMap(t *testing.T, s string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestEndpointQuoteBodyAndOutput(t *testing.T) {
	f := newEPFake()
	h := newEPHarness(t, f)
	out, err := h.c.QuoteCreateEndpoint(context.Background(), epCreateInput())
	if err != nil || out.MonthlyPrice != 712400 || out.Currency != "VND" {
		t.Fatalf("out=%#v err=%v", out, err)
	}
	req := f.find(http.MethodPost, "/price")
	if req == nil || req.Path != "/vnetwork-gateway/vnetwork/billing/v1/"+epRegion+"/"+epProject+"/price" {
		t.Fatalf("price request %#v", req)
	}
	want := `{"resourceType":"endpoint","action":"create","resourceInfo":{"isBuyMorePoc":false,"isPoc":false,"isEnableAutoRenew":false,"endpointName":"demo","categoryUuid":"` + epCategory + `","serviceUuid":"` + epService + `","packageUuid":"` + epPackage + `","vpcUuid":"` + epVPC + `","subnetUuid":"` + epSubnet + `","regionUuid":"` + epRegion + `","projectUuid":"` + epProject + `","enableAZ":true,"enableDnsName":false}}`
	if !reflect.DeepEqual(decodeMap(t, req.Body), decodeMap(t, want)) {
		t.Fatalf("quote body %s", req.Body)
	}
	if f.count(http.MethodPost, "/endpoints") != 0 || f.count(http.MethodGet, "/endpoints") != 0 {
		t.Fatal("quote created or took a baseline")
	}
}

func TestEndpointQuoteIgnoresCap(t *testing.T) {
	h := newEPHarness(t, newEPFake())
	in := epCreateInput()
	in.MaxPrice = 1
	if _, err := h.c.QuoteCreateEndpoint(context.Background(), in); err != nil {
		t.Fatal(err)
	}
}

func TestEndpointCreateHappyPath(t *testing.T) {
	f := epCreateFake()
	h := newEPHarness(t, f)
	out, err := h.c.CreateEndpoint(context.Background(), epCreateInput())
	if err != nil || out == nil || out.EndpointID != epID || out.Status != "ACTIVE" || out.MonthlyPrice != 712400 || out.Currency != "VND" {
		t.Fatalf("out=%#v err=%v", out, err)
	}
	if f.count(http.MethodPost, epBase) != 1 {
		t.Fatalf("posts %d", f.count(http.MethodPost, epBase))
	}
	create := f.find(http.MethodPost, epBase)
	quote := f.find(http.MethodPost, "/price")
	cm, qm := decodeMap(t, create.Body), decodeMap(t, quote.Body)
	tags, ok := cm["tagDetails"].([]any)
	if !ok || len(tags) != 0 {
		t.Fatalf("tagDetails %v", cm["tagDetails"])
	}
	delete(cm, "tagDetails")
	if !reflect.DeepEqual(cm, qm) {
		t.Fatalf("create body differs from quote\n%s\n%s", create.Body, quote.Body)
	}
	info := cm["resourceInfo"].(map[string]any)
	if info["isEnableAutoRenew"] != false || info["enableDnsName"] != false || info["enableAZ"] != true {
		t.Fatalf("info %v", info)
	}
	for _, key := range []string{"period", "paymentType"} {
		if _, ok := cm[key]; ok {
			t.Fatalf("sent %s", key)
		}
	}
	if len(h.slept) < 2 {
		t.Fatalf("expected polling, slept %v", h.slept)
	}
	for _, d := range h.slept {
		if d != 5*time.Second {
			t.Fatalf("poll interval %v", d)
		}
	}
	for _, r := range f.reqs {
		if strings.Contains(r.Path, "billing/v1/"+epRegion) && !strings.HasSuffix(r.Path, "/price") {
			t.Fatalf("billing read %s", r.Path)
		}
	}
}

func TestEndpointCreateOutputCarriesNoBillingClaim(t *testing.T) {
	typ := reflect.TypeFor[CreateEndpointOutput]()
	for i := range typ.NumField() {
		name := strings.ToLower(typ.Field(i).Name)
		if strings.Contains(name, "renew") || strings.Contains(name, "billing") {
			t.Fatalf("field %s", typ.Field(i).Name)
		}
	}
}

func TestEndpointCreatePriceGuards(t *testing.T) {
	for _, tc := range []struct {
		name    string
		price   string
		max     float64
		want    error
		posts   int
		invalid bool
	}{
		{name: "equal passes", price: epPrice(712400), max: 712400, posts: 1},
		{name: "over cap", price: epPrice(712401), max: 712400, want: vngcloud.ErrPriceAboveMax},
		{name: "zero cap blocks", price: epPrice(712400), max: 0, want: vngcloud.ErrPriceAboveMax},
		{name: "zero quote unpriced", price: epPrice(0), max: 10, want: vngcloud.ErrUnpriced},
		{name: "negative quote", price: strings.ReplaceAll(epPrice(5), `"optimumPrice":5`, `"optimumPrice":-5`), max: 10, invalid: true},
		{name: "property mismatch", price: strings.Replace(epPrice(5), `"monthlyPrice":5`, `"monthlyPrice":6`, 1), max: 10, invalid: true},
		{name: "two properties", price: strings.Replace(epPrice(5), `"propertiesPrice":[`, `"propertiesPrice":[{"optimumPrice":5,"monthlyPrice":5,"currentPrice":null,"discountPercent":0,"name":"Standard","description":"x"},`, 1), max: 10, invalid: true},
		{name: "wrong property name", price: strings.Replace(epPrice(5), `"name":"Standard"`, `"name":"Premium"`, 1), max: 10, invalid: true},
		{name: "missing top level", price: strings.Replace(epPrice(5), `"optimumPrice":5,"discountPercent"`, `"discountPercent"`, 1), max: 10, invalid: true},
		{name: "code not zero", price: strings.Replace(epPrice(5), `"code":0`, `"code":1`, 1), max: 10, invalid: true},
		{name: "success false", price: strings.Replace(epPrice(5), `"success":true`, `"success":false`, 1), max: 10, invalid: true},
		{name: "overflow", price: strings.ReplaceAll(epPrice(5), `"optimumPrice":5`, `"optimumPrice":1e999`), max: 10, invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := epCreateFake()
			f.price = tc.price
			h := newEPHarness(t, f)
			in := epCreateInput()
			in.MaxPrice = tc.max
			out, err := h.c.CreateEndpoint(context.Background(), in)
			if f.count(http.MethodPost, epBase) != tc.posts {
				t.Fatalf("posts %d err=%v", f.count(http.MethodPost, epBase), err)
			}
			switch {
			case tc.want != nil:
				if !errors.Is(err, tc.want) || out != nil {
					t.Fatalf("out=%v err=%v", out, err)
				}
			case tc.invalid:
				if err == nil || out != nil {
					t.Fatalf("out=%v err=%v", out, err)
				}
			default:
				if err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestEndpointCreateRejectsInputBeforeRequests(t *testing.T) {
	for name, mutate := range map[string]func(*CreateEndpointInput){
		"blank name":        func(in *CreateEndpointInput) { in.Name = "  " },
		"empty name":        func(in *CreateEndpointInput) { in.Name = "" },
		"control character": func(in *CreateEndpointInput) { in.Name = "a\nb" },
		"vpc form":          func(in *CreateEndpointInput) { in.VPCID = "vpc-1" },
		"vpc traversal":     func(in *CreateEndpointInput) { in.VPCID = "../x" },
		"subnet form":       func(in *CreateEndpointInput) { in.SubnetID = "net-00000000-0000-0000-0000-000000000003" },
		"negative cap":      func(in *CreateEndpointInput) { in.MaxPrice = -1 },
	} {
		t.Run(name, func(t *testing.T) {
			f := newEPFake()
			h := newEPHarness(t, f)
			in := epCreateInput()
			mutate(in)
			for label, call := range map[string]func() (any, error){
				"create": func() (any, error) { return h.c.CreateEndpoint(context.Background(), in) },
				"quote":  func() (any, error) { return h.c.QuoteCreateEndpoint(context.Background(), in) },
			} {
				if _, err := call(); !errors.Is(err, vngcloud.ErrInvalidInput) {
					t.Fatalf("%s err=%v", label, err)
				}
			}
			if f.total() != 0 {
				t.Fatalf("requests %d", f.total())
			}
		})
	}
	for _, cap := range []float64{nan(), inf()} {
		f := newEPFake()
		h := newEPHarness(t, f)
		in := epCreateInput()
		in.MaxPrice = cap
		if _, err := h.c.CreateEndpoint(context.Background(), in); !errors.Is(err, vngcloud.ErrInvalidInput) || f.total() != 0 {
			t.Fatalf("cap %v err=%v requests=%d", cap, err, f.total())
		}
	}
}

func TestEndpointWritesRejectUntrustedConfigBeforeRequests(t *testing.T) {
	for name, opt := range map[string]vngcloud.LoadOption{
		"region":    vngcloud.WithRegion("han-1"),
		"vnetwork":  vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{VNetwork: "https://invalid.example/"}),
		"vserver":   vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{VServer: "https://invalid.example/"}),
		"signin":    vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{Signin: "https://invalid.example"}),
		"token":     vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{Token: "https://invalid.example"}),
		"dashboard": vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{Dashboard: "https://invalid.example"}),
		"static":    vngcloud.WithStaticToken("static-token"),
	} {
		t.Run(name, func(t *testing.T) {
			f := newEPFake()
			h := newEPHarness(t, f, opt)
			_, e1 := h.c.CreateEndpoint(context.Background(), epCreateInput())
			_, e2 := h.c.QuoteCreateEndpoint(context.Background(), epCreateInput())
			_, e3 := h.c.DeleteEndpoint(context.Background(), &DeleteEndpointInput{EndpointID: epID, VPCID: epVPC})
			for _, err := range []error{e1, e2, e3} {
				if !errors.Is(err, vngcloud.ErrInvalidConfig) {
					t.Fatalf("err=%v", err)
				}
			}
			if f.total() != 0 {
				t.Fatalf("requests %d", f.total())
			}
		})
	}
}

func TestEndpointCreateRefusesExistingNameOrBaselineFailure(t *testing.T) {
	f := newEPFake()
	f.list = func(int) string { return epList(epRow(epOtherID, "demo", "ACTIVE", epSubnet)) }
	h := newEPHarness(t, f)
	if out, err := h.c.CreateEndpoint(context.Background(), epCreateInput()); err == nil || out != nil || f.count(http.MethodPost, epBase) != 0 {
		t.Fatalf("out=%v err=%v", out, err)
	}
	f = newEPFake()
	f.list = func(int) string { return `{"success":true}` }
	h = newEPHarness(t, f)
	if out, err := h.c.CreateEndpoint(context.Background(), epCreateInput()); err == nil || out != nil || f.count(http.MethodPost, epBase) != 0 {
		t.Fatalf("out=%v err=%v", out, err)
	}
	f = newEPFake()
	f.list = func(int) string { return epList(epRow(epOtherID, "demo-other", "ACTIVE", epSubnet)) }
	h = newEPHarness(t, f)
	f.onCreate = func() {
		f.list = func(int) string {
			return epList(epRow(epOtherID, "demo-other", "ACTIVE", epSubnet), epRow(epID, "demo", "ACTIVE", epSubnet))
		}
	}
	if out, err := h.c.CreateEndpoint(context.Background(), epCreateInput()); err != nil || out.EndpointID != epID {
		t.Fatalf("out=%v err=%v", out, err)
	}
}

func TestEndpointCreateDefinitiveRefusalReturnsNilAndFixedText(t *testing.T) {
	for _, status := range []int{400, 401, 403, 404, 409, 422, 429} {
		f := epCreateFake()
		f.createStatus = status
		f.createBody = `{"code":1,"success":false,"message":"server-canary","data":{"uuid":"` + epID + `"}}`
		h := newEPHarness(t, f)
		out, err := h.c.CreateEndpoint(context.Background(), epCreateInput())
		if out != nil || err == nil || errors.Is(err, ErrNotSettled) || strings.Contains(err.Error(), "server-canary") {
			t.Fatalf("status %d out=%v err=%v", status, out, err)
		}
		if f.count(http.MethodPost, epBase) != 1 {
			t.Fatalf("status %d posts %d", status, f.count(http.MethodPost, epBase))
		}
	}
}

func TestEndpointCreateAmbiguousRepliesAreNotSettled(t *testing.T) {
	ok := `{"message":"Success","code":0,"success":true,"data":{"uuid":"` + epID + `","name":"demo"}}`
	for _, tc := range []struct {
		name   string
		status int
		body   string
		id     string
	}{
		{"408", 408, `{}`, ""},
		{"500", 500, `{"message":"server-canary"}`, ""},
		{"502", 502, ``, ""},
		{"200 instead of 201", 200, ok, ""},
		{"202", 202, ok, ""},
		{"redirect", 307, ok, ""},
		{"other name", 201, strings.Replace(ok, `"name":"demo"`, `"name":"other"`, 1), ""},
		{"bad id form", 201, strings.Replace(ok, epID, "enp-xyz", 1), ""},
		{"no id", 201, `{"code":0,"success":true,"data":{"name":"demo"}}`, ""},
		{"code nonzero", 201, strings.Replace(ok, `"code":0`, `"code":5`, 1), ""},
		{"success false", 201, strings.Replace(ok, `"success":true`, `"success":false`, 1), ""},
		{"string code", 201, strings.Replace(ok, `"code":0`, `"code":"0"`, 1), ""},
		{"duplicate keys", 201, `{"code":0,"code":0,"success":true,"data":{"uuid":"` + epID + `","name":"demo"}}`, ""},
		{"not json", 201, `created`, ""},
		{"reflects bearer", 201, strings.Replace(ok, `"name":"demo"`, `"name":"demo","note":"`+epBearer+`"`, 1), ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := epCreateFake()
			f.createStatus, f.createBody = tc.status, tc.body
			h := newEPHarness(t, f)
			out, err := h.c.CreateEndpoint(context.Background(), epCreateInput())
			if !errors.Is(err, ErrNotSettled) || out == nil || out.EndpointID != tc.id {
				t.Fatalf("out=%#v err=%v", out, err)
			}
			if strings.Contains(err.Error(), "server-canary") || strings.Contains(err.Error(), epBearer) {
				t.Fatalf("err leaks: %v", err)
			}
			if f.count(http.MethodPost, epBase) != 1 {
				t.Fatalf("posts %d", f.count(http.MethodPost, epBase))
			}
		})
	}
}

func TestEndpointCreateAcceptedIDInBaselineIsNotSettledWithoutID(t *testing.T) {
	f := newEPFake()
	f.list = func(int) string { return epList(epRow(epID, "earlier", "ACTIVE", epSubnet)) }
	h := newEPHarness(t, f)
	out, err := h.c.CreateEndpoint(context.Background(), epCreateInput())
	if !errors.Is(err, ErrNotSettled) || out == nil || out.EndpointID != "" {
		t.Fatalf("out=%#v err=%v", out, err)
	}
}

func TestEndpointCreateWaitOutcomes(t *testing.T) {
	for _, tc := range []struct {
		name string
		row  func(n int) string
		ok   bool
	}{
		{"wrong name", func(int) string { return epRow(epID, "other", "ACTIVE", epSubnet) }, false},
		{"wrong subnet", func(int) string {
			return epRow(epID, "demo", "ACTIVE", "sub-00000000-0000-0000-0000-0000000000ff")
		}, false},
		{"wrong service", func(int) string {
			return strings.Replace(epRow(epID, "demo", "ACTIVE", epSubnet), `"endpointServiceId":"`+epService, `"endpointServiceId":"00000000-0000-0000-0000-0000000000a2`, 1)
		}, false},
		{"wrong package", func(int) string {
			return strings.Replace(epRow(epID, "demo", "ACTIVE", epSubnet), `"packageId":{"uuid":"`+epPackage, `"packageId":{"uuid":"00000000-0000-0000-0000-0000000000b2`, 1)
		}, false},
		{"wrong project", func(int) string {
			return strings.Replace(epRow(epID, "demo", "ACTIVE", epSubnet), `"VServerProjectId":"`+epProject, `"VServerProjectId":"pro-00000000-0000-0000-0000-0000000000ff`, 1)
		}, false},
		{"wrong zone", func(int) string {
			return strings.Replace(epRow(epID, "demo", "ACTIVE", epSubnet), `"zoneUuid":"HCM03-1C"`, `"zoneUuid":"HCM03-1B"`, 1)
		}, false},
		{"dns on", func(int) string {
			return strings.Replace(epRow(epID, "demo", "ACTIVE", epSubnet), `"enableDnsName":false`, `"enableDnsName":true`, 1)
		}, false},
		{"error state", func(int) string { return epRow(epID, "demo", "ERROR", epSubnet) }, false},
		{"unknown state", func(int) string { return epRow(epID, "demo", "DELETING", epSubnet) }, false},
		{"billing status ignored", func(int) string {
			return strings.Replace(epRow(epID, "demo", "ACTIVE", epSubnet), `"billingStatus":"provisioning"`, `"billingStatus":"weird"`, 1)
		}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newEPFake()
			created := false
			f.onCreate = func() { created = true }
			f.list = func(n int) string {
				if !created {
					return epList()
				}
				return epList(tc.row(n))
			}
			h := newEPHarness(t, f)
			out, err := h.c.CreateEndpoint(context.Background(), epCreateInput())
			if tc.ok {
				if err != nil || out.Status != "ACTIVE" {
					t.Fatalf("out=%v err=%v", out, err)
				}
				return
			}
			if !errors.Is(err, ErrNotSettled) || out == nil || out.EndpointID != epID {
				t.Fatalf("out=%#v err=%v", out, err)
			}
		})
	}
}

func TestEndpointCreateWaitBoundsAndCancellation(t *testing.T) {
	f := newEPFake()
	created := false
	f.onCreate = func() { created = true }
	f.list = func(int) string {
		if !created {
			return epList()
		}
		return epList(epRow(epID, "demo", "PROVISIONING", epSubnet))
	}
	h := newEPHarness(t, f)
	out, err := h.c.CreateEndpoint(context.Background(), epCreateInput())
	if !errors.Is(err, ErrNotSettled) || out == nil || out.EndpointID != epID || out.Status != "PROVISIONING" {
		t.Fatalf("out=%#v err=%v", out, err)
	}
	var total time.Duration
	for _, d := range h.slept {
		total += d
	}
	if total != 15*time.Minute {
		t.Fatalf("waited %v", total)
	}

	f = newEPFake()
	created = false
	f.onCreate = func() { created = true }
	f.list = func(int) string {
		if !created {
			return epList()
		}
		return epList(epRow(epID, "demo", "PROVISIONING", epSubnet))
	}
	h = newEPHarness(t, f)
	ctx, cancel := context.WithCancel(context.Background())
	f.listDelay = func(n int) {
		if n == 3 {
			cancel()
		}
	}
	out, err = h.c.CreateEndpoint(ctx, epCreateInput())
	if !errors.Is(err, ErrNotSettled) || !errors.Is(err, context.Canceled) || out == nil || out.EndpointID != epID {
		t.Fatalf("out=%#v err=%v", out, err)
	}
}

func TestEndpointCreateNoLateSuccessAfterDeadline(t *testing.T) {
	f := newEPFake()
	created := false
	f.onCreate = func() { created = true }
	var h *epHarness
	f.list = func(int) string {
		if !created {
			return epList()
		}
		h.clock = h.clock.Add(16 * time.Minute)
		return epList(epRow(epID, "demo", "ACTIVE", epSubnet))
	}
	h = newEPHarness(t, f)
	out, err := h.c.CreateEndpoint(context.Background(), epCreateInput())
	if !errors.Is(err, ErrNotSettled) || out == nil || out.Status == "ACTIVE" && err == nil {
		t.Fatalf("out=%#v err=%v", out, err)
	}
}
