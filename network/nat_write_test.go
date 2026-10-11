package network

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/testutil"
)

const natTestZones = `{"code":200,"success":true,"data":[{"uuid":"HAN01-1B","name":"Example","zoneType":"AVAILABILITY","isEnabled":true,"isDefault":false,"description":"Example"}]}`
const natTestPackages = `{"code":200,"success":true,"data":[{"uuid":"package-1","name":"Standard","packageId":"standard","resourceServiceId":"service-1","billingSku":"nat.s-standard","serviceName":"nat","description":null,"currencyUnit":"VND","createdAt":"2026-01-01","isDefault":true,"monthlyPrice":100,"price":{"optimumPrice":100,"originalPrice":100,"discountPrice":0,"discountPercent":0}}]}`
const natTestVPCs = `{"success":true,"page":1,"size":1000,"totalPage":1,"total":1,"data":[{"uuid":"vpc-1","projectId":"66b500000000000000000002","projectUuid":"pro-00000000-0000-0000-0000-000000000001","regionId":"66b500000000000000000001","regionUuid":"66b500000000000000000001","zones":[{"uuid":"HAN01-1B"}]}]}`
const natTestPrice = `{"code":0,"success":true,"data":{"optimumPrice":100,"originalPrice":100,"discountPrice":0,"discountPercent":null,"propertiesPrice":[{"optimumPrice":100,"monthlyPrice":100,"currentPrice":100,"discountPercent":0,"name":"Standard","description":"Example"}]}}`
const natTestRow = `{"uuid":"nat-1","natName":"example","status":"ACTIVE","projectUuid":"pro-00000000-0000-0000-0000-000000000001","zoneUuid":"HAN01-1B","natPackage":{"uuid":"package-1"},"vpc":{"uuid":"vpc-1","projectId":"66b500000000000000000002","projectUuid":null,"regionUuid":null,"regionId":"66b500000000000000000001"}}`

func natWriteTestClient(t *testing.T, h http.Handler, opts ...vngcloud.LoadOption) *Client {
	t.Helper()
	api := httptest.NewServer(h)
	t.Cleanup(api.Close)
	token := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"accessToken":"synthetic-token","expiresIn":3600}`))
	}))
	t.Cleanup(token.Close)
	signin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			http.Redirect(w, r, token.URL+"/callback?code=synthetic-code", http.StatusFound)
		} else {
			_, _ = w.Write([]byte(`<input name="_csrf" value="synthetic-csrf">`))
		}
	}))
	t.Cleanup(signin.Close)
	options := []vngcloud.LoadOption{vngcloud.WithRegion("han-1"), vngcloud.WithProjectID("pro-00000000-0000-0000-0000-000000000001"), vngcloud.WithRetry(3, time.Millisecond), vngcloud.WithIAMUser(&vngcloud.IAMUserAuth{RootEmail: "root@example.test", Username: "user", Password: "synthetic", SigninBaseURL: signin.URL, TokenURL: token.URL, DashboardURI: token.URL + "/"}), vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{VNetwork: api.URL + "/", Billing: api.URL + "/", VServer: api.URL + "/"})}
	cfg, err := vngcloud.NewConfig(append(options, opts...)...)
	if err != nil {
		t.Fatal(err)
	}
	c := New(cfg)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	c.now = func() time.Time { return now }
	c.sleep = func(ctx context.Context, d time.Duration) error { now = now.Add(d); return ctx.Err() }
	return c
}
func natTestInput() *CreateNATInstanceInput {
	return &CreateNATInstanceInput{Name: "example", ZoneID: "66b500000000000000000001", AvailabilityZoneID: "HAN01-1B", PackageID: "package-1", VPCID: "vpc-1", MaxPrice: 100}
}
func natInventory(row string) string {
	if row == "" {
		return `{"success":true,"page":1,"size":1000,"totalPage":0,"total":0,"data":[]}`
	}
	return `{"success":true,"page":1,"size":1000,"totalPage":1,"total":1,"data":[` + row + `]}`
}
func natBilling(manual bool) string {
	renewal := `"renewType":"AUTO-RENEW","renewPeriod":1`
	if manual {
		renewal = `"renewType":"MANUAL","renewPeriod":null`
	}
	return `{"code":200,"data":{"data":[{"product":"vserver","artifactType":"nat","artifactId":"nat-1","billingType":"PREPAID","channel":7,"status":"active","billingElements":[{"sku":"nat.s-standard","quantity":1}],` + renewal + `}]}}`
}

type natScenario struct {
	ordered, manual       bool
	orders, puts, deletes int
	priceBody, orderBody  []byte
}

func (s *natScenario) serve(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	body := ""
	switch {
	case strings.HasSuffix(r.URL.Path, "/v3-whitelist"):
		body = `{"code":200,"success":true,"data":{"enabledForAll":true,"whitelistedPortalUserIds":["portal-secret-canary"]}}`
	case strings.HasSuffix(r.URL.Path, "/zones"):
		body = natTestZones
	case strings.HasSuffix(r.URL.Path, "/nat-package"):
		if r.URL.Query().Get("zoneUuid") != "HAN01-1B" {
			t.Error("package not zone filtered")
		}
		body = natTestPackages
	case strings.HasSuffix(r.URL.Path, "/vpcs"):
		testutil.WriteFixture(t, w, "../testdata/network/nat_vpc_picker.json")
		return
	case strings.HasSuffix(r.URL.Path, "/price"):
		s.priceBody, _ = io.ReadAll(r.Body)
		_, _ = w.Write([]byte(natTestPrice))
		return
	case r.URL.Path == "/gateway/api/v1/home/user-info":
		body = `{"code":200,"data":{"userId":123456}}`
	case r.URL.Path == "/gateway/api/v1/resources/autoRenew":
		s.puts++
		s.manual = true
		var settings []struct {
			Channel       int
			AutoRenewInfo struct {
				IsEnable bool
				Period   int
			}
			Product, ArtifactType, ArtifactID string
		}
		if json.NewDecoder(r.Body).Decode(&settings) != nil || len(settings) != 1 || settings[0].Channel != 7 || settings[0].AutoRenewInfo.IsEnable || settings[0].AutoRenewInfo.Period != 43200 || settings[0].Product != "vserver" || settings[0].ArtifactType != "nat" || settings[0].ArtifactID != "nat-1" || r.Header.Get("portal-user-id") != "123456" {
			t.Error("renewal body or header")
		}
		body = `{"code":200,"data":{"successAll":true,"errorAutoRenewResources":[]}}`
	case r.URL.Path == "/gateway/api/v1/resources":
		body = natBilling(s.manual)
	case strings.HasSuffix(r.URL.Path, "/nats/nat-1"):
		s.deletes++
		s.ordered = false
		body = `{"code":0,"success":true}`
	case strings.HasSuffix(r.URL.Path, "/nats"):
		switch {
		case r.Method == http.MethodPost:
			s.orders++
			s.ordered = true
			s.orderBody, _ = io.ReadAll(r.Body)
			w.WriteHeader(201)
			body = `{"code":0,"success":true}`
		case s.ordered:
			testutil.WriteFixture(t, w, "../testdata/network/nat_write_inventory.json")
			return
		default:
			body = natInventory("")
		}
	default:
		t.Errorf("unexpected path %s", r.URL.Path)
	}
	_, _ = w.Write([]byte(body))
}
func TestNATCreateAndDelete(t *testing.T) {
	s := new(natScenario)
	c := natWriteTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { s.serve(t, w, r) }))
	out, err := c.CreateNATInstance(context.Background(), natTestInput())
	if err != nil {
		t.Fatal(err)
	}
	if out.NATInstance == nil || out.NATInstance.UUID != "nat-1" || out.AutoRenew == nil || *out.AutoRenew || out.TotalPrice != 100 || out.Currency != "VND" || out.OrderID != "" || s.orders != 1 || s.puts != 1 {
		t.Fatalf("create result %+v", out)
	}
	var price, order map[string]json.RawMessage
	if json.Unmarshal(s.priceBody, &price) != nil || json.Unmarshal(s.orderBody, &order) != nil {
		t.Fatal("request JSON")
	}
	if string(order["tagDetails"]) != "[]" {
		t.Fatal("missing tags")
	}
	delete(order, "tagDetails")
	p, _ := json.Marshal(price)
	o, _ := json.Marshal(order)
	if string(p) != string(o) {
		t.Fatal("quote/order mismatch")
	}
	for _, field := range []string{"period", "paymentType", "subnet"} {
		if strings.Contains(string(s.orderBody), field) {
			t.Errorf("forbidden order field %s", field)
		}
	}
	expected := `{"resourceType":"nat","action":"create","resourceInfo":{"isPoc":false,"isEnableAutoRenew":false,"isBuyMorePoc":false,"natName":"example","packageUuid":"package-1","vpcUuid":"vpc-1","regionUuid":"66b500000000000000000001","projectUuid":"pro-00000000-0000-0000-0000-000000000001"},"tagDetails":[]}`
	if string(s.orderBody) != expected {
		t.Fatalf("order body %s", s.orderBody)
	}
	if !strings.Contains(string(s.orderBody), `"isEnableAutoRenew":false`) {
		t.Error("renewal flag")
	}
	_, err = c.DeleteNATInstance(context.Background(), &DeleteNATInstanceInput{ZoneID: "66b500000000000000000001", VPCID: "vpc-1", NATID: "nat-1"})
	if err != nil {
		t.Fatal(err)
	}
	if s.deletes != 1 {
		t.Fatal("delete count")
	}
	_, err = c.DeleteNATInstance(context.Background(), &DeleteNATInstanceInput{ZoneID: "66b500000000000000000001", VPCID: "vpc-1", NATID: "nat-1"})
	if !errors.Is(err, vngcloud.ErrNotFound) || s.deletes != 1 {
		t.Fatal("absence guard")
	}
}

func TestNATQuoteDoesNotOrder(t *testing.T) {
	s := new(natScenario)
	c := natWriteTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { s.serve(t, w, r) }))
	in := natTestInput()
	in.MaxPrice = 0
	out, err := c.QuoteCreateNATInstance(context.Background(), in)
	if err != nil || s.orders != 0 || s.puts != 0 || out.TotalPrice != 100 || out.MonthlyPrice != 100 || out.Currency != "VND" || out.OriginalPrice != 100 || out.DiscountPrice != 0 || out.DiscountPercent != nil || len(out.Properties) != 1 || out.Properties[0].CurrentPrice == nil || *out.Properties[0].CurrentPrice != 100 {
		t.Fatalf("quote %v %+v", err, out)
	}
}

func TestNATProjectDiscoveryWithholdsAccountData(t *testing.T) {
	s := new(natScenario)
	captures := 0
	c := natWriteTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/projects" {
			_, _ = w.Write([]byte(`{"projects":[{"projectId":"pro-00000000-0000-0000-0000-000000000001","region":"han-1","userId":123456}]}`))
			return
		}
		s.serve(t, w, r)
	}), vngcloud.WithProjectID(""), vngcloud.WithResponseCapture(func(vngcloud.ResponseCapture) { captures++ }))
	_, err := c.CreateNATInstance(context.Background(), natTestInput())
	if err != nil || captures != 0 {
		t.Fatalf("project discovery: %v captures %d", err, captures)
	}
}

func TestNATQuoteRetainsReadAuthAndRegions(t *testing.T) {
	for _, opt := range []vngcloud.LoadOption{vngcloud.WithStaticToken("synthetic-token"), vngcloud.WithCredentialsProvider(natCustomProvider{}), vngcloud.WithRegion("hcm-3")} {
		s := new(natScenario)
		c := natWriteTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { s.serve(t, w, r) }), opt)
		out, err := c.QuoteCreateNATInstance(context.Background(), natTestInput())
		if err != nil || out == nil || out.TotalPrice != 100 || s.orders != 0 || s.puts != 0 {
			t.Fatalf("read quote %v %+v", err, out)
		}
	}
}

func TestNATQuoteDoesNotRequireCreateBaseline(t *testing.T) {
	s := new(natScenario)
	c := natWriteTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/nats") {
			t.Error("quote requested a create inventory baseline")
			w.WriteHeader(403)
			return
		}
		s.serve(t, w, r)
	}))
	_, err := c.QuoteCreateNATInstance(context.Background(), natTestInput())
	if err != nil {
		t.Fatal(err)
	}
}
