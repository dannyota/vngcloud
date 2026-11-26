package network

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/transport"
)

type bandwidthEmptyCredentials struct{}

func (bandwidthEmptyCredentials) Token(context.Context) (vngcloud.Token, error) {
	return vngcloud.Token{}, nil
}

func (bandwidthEmptyCredentials) Invalidate(string) {}

func TestBandwidthEmptyCredentials(t *testing.T) {
	calls := 0
	rt := natRoundTripper(func(*http.Request) (*http.Response, error) {
		calls++
		return nil, errors.New("unexpected request")
	})
	cfg, err := vngcloud.NewConfig(vngcloud.WithRegion("han-1"), vngcloud.WithProjectID("project-1"), vngcloud.WithCredentialsProvider(bandwidthEmptyCredentials{}), vngcloud.WithTransport(rt))
	if err != nil {
		t.Fatal(err)
	}
	_, err = New(cfg).ListBandwidthPackages(context.Background(), nil)
	var api *vngcloud.APIError
	if !errors.As(err, &api) || api.StatusCode != http.StatusUnauthorized || api.Code != "Unauthorized" || !errors.Is(err, vngcloud.ErrAuth) || calls != 0 {
		t.Fatalf("error %#v calls %d", err, calls)
	}
}

func TestBandwidthIncompleteProjectInventory(t *testing.T) {
	calls := 0
	rt := natRoundTripper(func(r *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"projects":[{"projectId":"project-1"}],"totalItem":2}`)), Request: r}, nil
	})
	cfg, err := vngcloud.NewConfig(vngcloud.WithRegion("han-1"), vngcloud.WithStaticToken("test-token"), vngcloud.WithTransport(rt))
	if err != nil {
		t.Fatal(err)
	}
	_, err = New(cfg).ListBandwidthPackages(context.Background(), nil)
	if vngcloud.ErrorCode(err) != "InvalidResponse" || calls != 1 {
		t.Fatalf("error %v calls %d", err, calls)
	}
}

func TestBandwidthCatalog(t *testing.T) {
	calls := 0
	rt := natRoundTripper(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.String() != "https://han-1.console.greennode.ai/vserver/bandwidth-api/v1/projects/project-1/bandwidth/package-config" || r.Method != http.MethodGet {
			t.Errorf("unexpected request %s %s", r.Method, r.URL)
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"data":[{"uuid":"pkg-1","name":"Basic","type":"FUTURE","description":"Test","bandwidth":100}]}`)), Request: r}, nil
	})
	cfg, err := vngcloud.NewConfig(vngcloud.WithRegion("han-1"), vngcloud.WithProjectID("project-1"), vngcloud.WithStaticToken("test-token"), vngcloud.WithTransport(rt))
	if err != nil {
		t.Fatal(err)
	}
	out, err := New(cfg).ListBandwidthPackages(context.Background(), nil)
	if err != nil || calls != 1 {
		t.Fatalf("calls %d err %v", calls, err)
	}
	if len(out.Items) != 1 || out.Items[0].ID != "pkg-1" || out.Items[0].Type != "FUTURE" || out.Items[0].Bandwidth != 100 {
		t.Fatalf("unexpected result %+v", out)
	}
}

func TestBandwidthStrictResponses(t *testing.T) {
	fixture, err := os.ReadFile("../testdata/network/list_bandwidth_packages.json")
	if err != nil {
		t.Fatal(err)
	}
	good := strings.TrimSpace(string(fixture))
	for _, tc := range []struct {
		name, body string
		valid      bool
	}{
		{"fixture", good, true}, {"empty", `{"data":[]}`, true},
		{"error marker", strings.Replace(good, `"data":`, `"error":"failure","data":`, 1), false},
		{"missing data", `{}`, false}, {"null data", `{"data":null}`, false}, {"object data", `{"data":{}}`, false},
		{"false success", strings.Replace(good, `true`, `false`, 1), false},
		{"noninteger code", strings.Replace(good, `200`, `200.0`, 1), false},
		{"duplicate key", strings.Replace(good, `"success":true`, `"success":true,"success":true`, 1), false},
		{"alias", strings.Replace(good, `"uuid"`, `"UUID"`, 1), false},
		{"missing row field", strings.Replace(good, `"description":"Synthetic offer",`, ``, 1), false},
		{"null row field", strings.Replace(good, `"bandwidth":100`, `"bandwidth":null`, 1), false},
		{"fractional bandwidth", strings.Replace(good, `"bandwidth":100`, `"bandwidth":1.5`, 1), false},
		{"trailing", good + ` {}`, false}, {"paging", `{"data":[],"hasMore":true}`, false},
		{"paging alias", `{"data":[],"HasMore":true}`, false},
		{"total item exceeds rows", `{"data":[],"totalItem":1}`, false},
		{"total exceeds rows", `{"data":[],"total":1}`, false},
		{"total items exceeds rows", `{"data":[],"totalItems":1}`, false},
		{"total alias", `{"data":[],"TotalItems":0}`, false},
		{"invalid total", `{"data":[],"total":null}`, false},
		{"complete total", `{"data":[],"total":0}`, true},
		{"duplicate id", `{"data":[{"uuid":"x","name":"n","type":"t","description":"d","bandwidth":1},{"uuid":"x","name":"n","type":"t","description":"d","bandwidth":1}]}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rt := natRoundTripper(func(r *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(tc.body)), Request: r}, nil
			})
			cfg, e := vngcloud.NewConfig(vngcloud.WithRegion("hcm-3"), vngcloud.WithProjectID("project-1"), vngcloud.WithStaticToken("test-token"), vngcloud.WithTransport(rt))
			if e != nil {
				t.Fatal(e)
			}
			_, e = New(cfg).ListBandwidthPackages(context.Background(), nil)
			if tc.valid && e != nil || !tc.valid && vngcloud.ErrorCode(e) != "InvalidResponse" {
				t.Fatalf("error %v", e)
			}
		})
	}
}

func TestBandwidthProjectRoutingAndReflection(t *testing.T) {
	for _, reflected := range []bool{false, true} {
		t.Run(strconv.FormatBool(reflected), func(t *testing.T) {
			calls := 0
			rt := natRoundTripper(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.URL.Host != "hcm-3.console.greennode.ai" {
					t.Errorf("host %s", r.URL.Host)
				}
				status := 200
				body := `{"projects":[{"projectId":"project-1","region":"hcm-3"}]}`
				if reflected {
					status = 401
					body = `{"message":"test-token"}`
				} else if calls == 2 {
					if r.URL.Path != "/vserver/bandwidth-api/v1/projects/project-1/bandwidth/package-config" {
						t.Errorf("path %s", r.URL.Path)
					}
					body = `{"data":[]}`
				}
				return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
			})
			cfg, e := vngcloud.NewConfig(vngcloud.WithRegion("hcm-3"), vngcloud.WithStaticToken("test-token"), vngcloud.WithTransport(rt), vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{VServer: "https://override.example.test"}))
			if e != nil {
				t.Fatal(e)
			}
			_, e = New(cfg).ListBandwidthPackages(context.Background(), nil)
			if reflected {
				if vngcloud.ErrorCode(e) != "InvalidResponse" || calls != 1 {
					t.Fatalf("error %v calls %d", e, calls)
				}
			} else if e != nil || calls != 2 {
				t.Fatalf("error %v calls %d", e, calls)
			}
		})
	}
}

func TestBandwidthCatalogReflectedBearer(t *testing.T) {
	calls := 0
	rt := natRoundTripper(func(r *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 401, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"message":"test-token"}`)), Request: r}, nil
	})
	cfg, err := vngcloud.NewConfig(vngcloud.WithRegion("han-1"), vngcloud.WithProjectID("project-1"), vngcloud.WithStaticToken("test-token"), vngcloud.WithTransport(rt))
	if err != nil {
		t.Fatal(err)
	}
	_, err = New(cfg).ListBandwidthPackages(context.Background(), nil)
	if vngcloud.ErrorCode(err) != "InvalidResponse" || calls != 1 || strings.Contains(err.Error(), "test-token") {
		t.Fatalf("error %v calls %d", err, calls)
	}
}

func TestBandwidthUnsupportedRegion(t *testing.T) {
	calls := 0
	rt := natRoundTripper(func(r *http.Request) (*http.Response, error) { calls++; return nil, errors.New("unexpected request") })
	cfg, err := vngcloud.NewConfig(vngcloud.WithRegion("unknown"), vngcloud.WithStaticToken("test-token"), vngcloud.WithTransport(rt))
	if err != nil {
		t.Fatal(err)
	}
	_, err = New(cfg).ListBandwidthPackages(context.Background(), nil)
	if !errors.Is(err, vngcloud.ErrInvalidConfig) || calls != 0 {
		t.Fatalf("error %v calls %d", err, calls)
	}
}

func TestBandwidthBodyLimit(t *testing.T) {
	for _, project := range []bool{false, true} {
		for _, status := range []int{200, 403, 500} {
			t.Run(strconv.FormatBool(project)+"/"+strconv.Itoa(status), func(t *testing.T) {
				rt := natRoundTripper(func(r *http.Request) (*http.Response, error) {
					return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"extra":"` + strings.Repeat("a", 4<<20) + `"}`)), Request: r}, nil
				})
				id := "project-1"
				if project {
					id = ""
				}
				cfg, err := vngcloud.NewConfig(vngcloud.WithRegion("han-1"), vngcloud.WithProjectID(id), vngcloud.WithStaticToken("test-token"), vngcloud.WithTransport(rt))
				if err != nil {
					t.Fatal(err)
				}
				_, err = New(cfg).ListBandwidthPackages(context.Background(), nil)
				if !errors.Is(err, transport.ErrBodyTooLarge) {
					t.Fatalf("error %v", err)
				}
			})
		}
	}
}

func TestBandwidthCatalogErrorWithholdsPeerText(t *testing.T) {
	rt := natRoundTripper(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 403, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"code":"peer-code-canary","message":"peer-message-canary"}`)), Request: r}, nil
	})
	cfg, err := vngcloud.NewConfig(vngcloud.WithRegion("han-1"), vngcloud.WithProjectID("project-1"), vngcloud.WithStaticToken("test-token"), vngcloud.WithTransport(rt))
	if err != nil {
		t.Fatal(err)
	}
	_, err = New(cfg).ListBandwidthPackages(context.Background(), nil)
	var api *vngcloud.APIError
	if !errors.As(err, &api) || api.StatusCode != 403 || !errors.Is(err, vngcloud.ErrPermission) || strings.Contains(err.Error(), "peer-") || strings.Contains(api.Code, "peer-") {
		t.Fatalf("error %v", err)
	}
}
