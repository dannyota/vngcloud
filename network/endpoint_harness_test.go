package network

import (
	"context"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"danny.vn/vngcloud"
)

const (
	epRegion      = "66b500000000000000000001"
	epCategory    = "66b500000000000000000009"
	epProject     = "pro-00000000-0000-0000-0000-000000000001"
	epVPC         = "net-00000000-0000-0000-0000-000000000002"
	epSubnet      = "sub-00000000-0000-0000-0000-000000000003"
	epService     = "00000000-0000-0000-0000-0000000000a1"
	epPackage     = "00000000-0000-0000-0000-0000000000b1"
	epServiceID24 = "66b500000000000000000008"
	epID          = "enp-00000000-0000-0000-0000-0000000000c1"
	epOtherID     = "enp-00000000-0000-0000-0000-0000000000c2"
	epBearer      = "bearer-synthetic"
	epBase        = "/vnetwork-gateway/vnetwork/v1/" + epRegion + "/" + epProject + "/endpoints"
)

type epRoundTrip func(*http.Request) (*http.Response, error)

func (f epRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type epRecorded struct{ Method, Path, Query, Body string }

type epFake struct {
	mu                                                   sync.Mutex
	reqs                                                 []epRecorded
	regions, projects                                    string
	zones, categories, services, packages, vpcs, subnets string
	price                                                string
	priceStatus                                          int
	list                                                 func(n int) string
	createStatus, deleteStatus                           int
	createBody, deleteBody                               string
	listCalls                                            int
	onCreate                                             func()
	onDelete                                             func()
	rewrap                                               func(r *http.Request, resp *http.Response)
	listStatus                                           func(n int) int
	listDelay                                            func(n int)
}

func epList(rows ...string) string {
	if len(rows) == 0 {
		return `{"success":true,"page":1,"size":1000,"totalPage":0,"total":0}`
	}
	return `{"success":true,"page":1,"size":1000,"totalPage":1,"total":` + strconv.Itoa(len(rows)) + `,"data":[` + strings.Join(rows, ",") + `]}`
}

func epRow(id, name, status, subnet string) string {
	return `{"uuid":"` + id + `","endpointName":"` + name + `","endpointServiceId":"` + epServiceID24 + `","vpcId":"` + epVPC + `","status":"` + status + `","billingStatus":"provisioning","endpointType":"Gateway","zoneUuid":"HCM03-1C","enableDnsName":false,"endpointResource":null,"endpointDomains":[],"category":{},"service":{"uuid":"` + epService + `","name":"vstorage.hcm03","endpointType":"Gateway"},"vpc":{"uuid":"` + epVPC + `","projectUuid":null,"project":null,"projectId":"backend-1"},"subnet":{"uuid":"` + subnet + `"},"project":{"id":"1","backendProjectId":"2","portalUserId":"3","VServerProjectId":"` + epProject + `"},"packageId":{"uuid":"` + epPackage + `","monthlyPrice":712400,"currencyUnit":"VND"}}`
}

func newEPFake() *epFake {
	return &epFake{
		regions:      `{"success":true,"data":[{"uuid":"` + epRegion + `","vnetworkDashboard":"https://hcm-3-vnetwork.console.greennode.ai","gatewayUrl":"https://hcm-3-vnetwork.console.greennode.ai/vnetwork-gateway"}]}`,
		projects:     `{"projects":[{"projectId":"` + epProject + `","region":"hcm-3"}]}`,
		zones:        `{"message":"Success","code":200,"success":true,"data":[{"uuid":"HCM03-1B","name":"b","zoneType":"AVAILABILITY","isEnabled":false,"isDefault":false},{"uuid":"HCM03-1C","name":"c","zoneType":"AVAILABILITY","isEnabled":true,"isDefault":true}]}`,
		categories:   `{"success":true,"data":[{"uuid":"` + epCategory + `","name":"VNGEndpoint","isDefault":true}]}`,
		services:     `{"success":true,"data":[{"uuid":"00000000-0000-0000-0000-0000000000f1","name":"vstorage.han01","endpointType":"Gateway"},{"uuid":"` + epService + `","name":"vstorage.hcm03","endpointType":"Gateway"}]}`,
		packages:     `{"success":true,"data":[{"uuid":"` + epPackage + `","name":"Standard","serviceName":"vstorage.hcm03","monthlyPrice":712400,"currencyUnit":"VND"}]}`,
		vpcs:         `{"success":true,"page":1,"size":1000,"totalPage":1,"total":1,"data":[{"uuid":"` + epVPC + `","status":"ACTIVE","projectUuid":"` + epProject + `","regionUuid":"` + epRegion + `"}]}`,
		subnets:      `{"message":"Success","code":0,"success":true,"data":[{"uuid":"` + epSubnet + `","name":"s","status":"ACTIVE","cidr":"10.0.0.0/24","subnetType":"USER","vpcUuid":"` + epVPC + `","zoneId":"66b500000000000000000007","zone":{"uuid":"HCM03-1C"}}]}`,
		price:        epPrice(712400),
		createStatus: 201, createBody: `{"message":"Success","code":0,"success":true,"data":{"uuid":"` + epID + `","name":"demo"}}`,
		deleteStatus: 200, deleteBody: `{"message":"Success","code":0,"success":true}`,
		list: func(int) string { return epList() },
	}
}

func epPrice(amount int) string {
	a := strconv.Itoa(amount)
	return `{"message":"Success","code":0,"success":true,"data":{"optimumPrice":` + a + `,"discountPercent":0.0,"originalPrice":` + a + `,"discountPrice":0,"propertiesPrice":[{"optimumPrice":` + a + `,"monthlyPrice":` + a + `,"currentPrice":null,"discountPercent":0.0,"name":"Standard","description":"STANDARD"}]}}`
}

func (f *epFake) count(method, suffix string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, r := range f.reqs {
		if r.Method == method && strings.HasSuffix(r.Path, suffix) {
			n++
		}
	}
	return n
}

func (f *epFake) total() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.reqs)
}

func (f *epFake) find(method, suffix string) *epRecorded {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.reqs {
		if f.reqs[i].Method == method && strings.HasSuffix(f.reqs[i].Path, suffix) {
			r := f.reqs[i]
			return &r
		}
	}
	return nil
}

func (f *epFake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	f.reqs = append(f.reqs, epRecorded{r.Method, r.URL.Path, r.URL.RawQuery, string(body)})
	f.mu.Unlock()
	write := func(status int, s string) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, s)
	}
	p := r.URL.Path
	switch {
	case p == "/vserver/iam-vserver-gateway/v1/projects":
		write(200, f.projects)
	case p == "/vnetwork-gateway/vnetwork/v1/regions":
		write(200, f.regions)
	case strings.HasSuffix(p, "/price"):
		st := f.priceStatus
		if st == 0 {
			st = 200
		}
		write(st, f.price)
	case !strings.HasPrefix(p, epBase):
		write(404, `{"canary":"unexpected path `+p+`"}`)
	case p == epBase+"/zones":
		write(200, f.zones)
	case p == epBase+"/categories":
		write(200, f.categories)
	case p == epBase+"/"+epCategory+"/services":
		write(200, f.services)
	case p == epBase+"/"+epService+"/packages":
		write(200, f.packages)
	case p == epBase+"/vpcs":
		write(200, f.vpcs)
	case p == epBase+"/vpcs/"+epVPC+"/subnets":
		write(200, f.subnets)
	case p == epBase && r.Method == http.MethodPost:
		if f.onCreate != nil {
			f.onCreate()
		}
		write(f.createStatus, f.createBody)
	case p == epBase:
		f.mu.Lock()
		f.listCalls++
		n := f.listCalls
		f.mu.Unlock()
		if f.listDelay != nil {
			f.listDelay(n)
		}
		status := 200
		if f.listStatus != nil {
			status = f.listStatus(n)
		}
		write(status, f.list(n))
	case p == epBase+"/"+epID && r.Method == http.MethodDelete:
		if f.onDelete != nil {
			f.onDelete()
		}
		write(f.deleteStatus, f.deleteBody)
	default:
		write(404, `{"canary":"unexpected path `+p+`"}`)
	}
}

type epHarness struct {
	c     *Client
	fake  *epFake
	clock time.Time
	slept []time.Duration
}

func newEPHarness(t *testing.T, fake *epFake, extra ...vngcloud.LoadOption) *epHarness {
	t.Helper()
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	target, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	api := epRoundTrip(func(r *http.Request) (*http.Response, error) {
		clone := r.Clone(r.Context())
		u := *r.URL
		clone.URL = &u
		clone.URL.Scheme = target.Scheme
		clone.URL.Host = target.Host
		clone.Host = target.Host
		resp, err := server.Client().Transport.RoundTrip(clone)
		if err == nil && fake.rewrap != nil {
			fake.rewrap(r, resp)
		}
		return resp, err
	})
	auth := epRoundTrip(func(r *http.Request) (*http.Response, error) {
		w := httptest.NewRecorder()
		switch {
		case r.URL.Host == "signin.greennode.ai" && r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`<html><input name="_csrf" value="csrf"></html>`))
		case r.URL.Host == "signin.greennode.ai":
			w.Header().Set("Location", "https://dashboard.console.greennode.ai/?code=synthetic")
			w.WriteHeader(http.StatusFound)
		default:
			_, _ = w.Write([]byte(`{"accessToken":"` + epBearer + `","expiresIn":3600}`))
		}
		return w.Result(), nil
	})
	opts := []vngcloud.LoadOption{vngcloud.WithRegion("hcm-3"), vngcloud.WithIAMUser(&vngcloud.IAMUserAuth{RootEmail: "root", Username: "user", Password: "synthetic", HTTPClient: &http.Client{Transport: auth}}), vngcloud.WithTransport(api), vngcloud.WithRetry(2, 0)}
	cfg, err := vngcloud.NewConfig(append(opts, extra...)...)
	if err != nil {
		t.Fatal(err)
	}
	h := &epHarness{c: New(cfg), fake: fake, clock: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	h.c.now = func() time.Time { return h.clock }
	h.c.sleep = func(ctx context.Context, d time.Duration) error {
		h.slept = append(h.slept, d)
		h.clock = h.clock.Add(d)
		return ctx.Err()
	}
	return h
}

func epCreateInput() *CreateEndpointInput {
	return &CreateEndpointInput{Name: "demo", VPCID: epVPC, SubnetID: epSubnet, MaxPrice: 712400}
}

func nan() float64 { return math.NaN() }
func inf() float64 { return math.Inf(1) }

func itoaT(n int) string { return strconv.Itoa(n) }

func pad12(n int) string { return strings.Repeat("0", 12-len(strconv.Itoa(n))) + strconv.Itoa(n) }
