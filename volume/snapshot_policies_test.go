package volume

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/testutil"
)

type snapshotCredentials struct{ calls int }

func (p *snapshotCredentials) Token(context.Context) (vngcloud.Token, error) {
	p.calls++
	return vngcloud.Token{AccessToken: "test-token"}, nil
}
func (*snapshotCredentials) Invalidate(string) {}

func snapshotClient(t *testing.T, region string, override bool, project string, handler http.Handler) (*Client, *snapshotCredentials, *int) {
	t.Helper()
	requests := new(int)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { *requests++; handler.ServeHTTP(w, r) }))
	t.Cleanup(server.Close)
	p := &snapshotCredentials{}
	endpoints := vngcloud.EndpointOverrides{VServer: server.URL + "/wrong/", Dashboard: server.URL + "/wrong/"}
	if override {
		endpoints.VServerBackup = server.URL + "/vserver/vbackup-gateway/"
	}
	cfg, err := vngcloud.NewConfig(vngcloud.WithRegion(region), vngcloud.WithProjectID(project), vngcloud.WithCredentialsProvider(p), vngcloud.WithEndpointOverrides(endpoints), vngcloud.WithRetry(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	return New(cfg), p, requests
}

func TestSnapshotPoliciesRequestAndFixture(t *testing.T) {
	for _, tc := range []struct {
		page, size int
		fixture    string
	}{{0, 0, "snapshot_policies"}, {1, 1, "snapshot_policies_page_1"}, {2, 1, "snapshot_policies_page_2"}} {
		t.Run(tc.fixture, func(t *testing.T) {
			c, _, requests := snapshotClient(t, "hcm-3", true, "project-1", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				page, size := tc.page, tc.size
				if page == 0 {
					page = 1
				}
				if size == 0 {
					size = 10
				}
				want := url.Values{"backendId": {"backend-1"}, "projectId": {"project-1"}, "page": {strconv.Itoa(page)}, "size": {strconv.Itoa(size)}}
				if r.Method != "GET" || r.URL.Path != "/vserver/vbackup-gateway/v1/snapshot-policies" || r.URL.RawQuery != want.Encode() || r.Header.Get("Authorization") != "Bearer test-token" {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL)
				}
				testutil.WriteFixture(t, w, "../testdata/volume/"+tc.fixture+".json")
			}))
			out, err := c.ListSnapshotPolicies(context.Background(), &ListSnapshotPoliciesInput{BackendID: "backend-1", Page: tc.page, Size: tc.size})
			if err != nil {
				t.Fatal(err)
			}
			raw, err := os.ReadFile("../testdata/volume/" + tc.fixture + ".json")
			if err != nil {
				t.Fatal(err)
			}
			var expected struct {
				Items                                  []SnapshotPolicy
				Page, PageSize, TotalPages, TotalItems int
			}
			if err := json.Unmarshal(raw, &expected); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(out.Items, expected.Items) || out.Page != expected.Page || out.PageSize != expected.PageSize || out.TotalPage != expected.TotalPages || out.TotalItem != expected.TotalItems || *requests != 1 {
				t.Fatalf("unexpected output: %+v", out)
			}
			if tc.page == 0 {
				a, b := out.Items[0], out.Items[1]
				if a.Config.DailyConfig == nil || a.Config.DailyConfig.Retention == nil || *a.Config.DailyConfig.Retention != 1 || a.Config.HourlyConfig == nil || a.Config.HourlyConfig.Interval != nil || a.Config.HourlyConfig.Retention != nil {
					t.Fatal("daily or empty hourly config lost")
				}
				if b.Config.HourlyConfig == nil || b.Config.HourlyConfig.Interval == nil || b.Config.HourlyConfig.Retention == nil || *b.Config.HourlyConfig.Interval != 1 || *b.Config.HourlyConfig.Retention != 1 || b.Config.DailyConfig == nil || b.Config.DailyConfig.Retention != nil {
					t.Fatal("hourly or empty daily config lost")
				}
				encoded, _ := json.Marshal(out)
				for _, key := range []string{"userId", "backendId", "projectId", "weeklyConfig", "monthlyConfig", "isDefault", "deletedAt"} {
					if strings.Contains(string(encoded), `"`+key+`"`) {
						t.Errorf("omitted field %s exposed", key)
					}
				}
			}
		})
	}
}

func TestSnapshotReadsRejectBeforeAccess(t *testing.T) {
	for _, region := range []string{"han-1", "unknown"} {
		for _, override := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/%t", region, override), func(t *testing.T) {
				c, p, requests := snapshotClient(t, region, override, "", http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("unexpected request") }))
				_, err := c.ListSnapshotPolicies(context.Background(), &ListSnapshotPoliciesInput{BackendID: "backend-1"})
				if !errors.Is(err, vngcloud.ErrInvalidConfig) {
					t.Errorf("error = %v", err)
				}
				_, err = c.ListSnapshotBackends(context.Background(), &ListSnapshotBackendsInput{Name: "HCM-03"})
				if !errors.Is(err, vngcloud.ErrInvalidConfig) {
					t.Errorf("error = %v", err)
				}
				if p.calls != 0 || *requests != 0 {
					t.Fatal("region rejection made access")
				}
			})
		}
	}
	c, p, requests := snapshotClient(t, "hcm-3", true, "", http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("unexpected request") }))
	for _, in := range []*ListSnapshotPoliciesInput{nil, {}, {BackendID: "../bad"}, {BackendID: "a?b"}, {BackendID: "backend-1", Page: -1}, {BackendID: "backend-1", Size: -1}} {
		_, err := c.ListSnapshotPolicies(context.Background(), in)
		if !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Errorf("error = %v", err)
		}
	}
	for _, in := range []*ListSnapshotBackendsInput{nil, {}, {Name: " "}} {
		_, err := c.ListSnapshotBackends(context.Background(), in)
		if !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Errorf("error = %v", err)
		}
	}
	if p.calls != 0 || *requests != 0 {
		t.Fatal("invalid input made access")
	}
}

func TestSnapshotPoliciesSyntheticShapes(t *testing.T) {
	for _, config := range []string{`{}`, `{"hourlyConfig":null,"dailyConfig":null}`, `{"hourlyConfig":{},"dailyConfig":{}}`, `{"hourlyConfig":{"interval":0,"retention":0},"dailyConfig":{"retention":0}}`} {
		c, _, _ := snapshotClient(t, "hcm-3", true, "project-1", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = fmt.Fprintf(w, `{"items":[{"policyType":"FUTURE","config":%s}],"page":1,"pageSize":10,"totalPages":1,"totalItems":1}`, config)
		}))
		out, err := c.ListSnapshotPolicies(context.Background(), &ListSnapshotPoliciesInput{BackendID: "backend-1"})
		if err != nil {
			t.Fatal(err)
		}
		if out.Items[0].PolicyType != "FUTURE" {
			t.Fatal("unknown type lost")
		}
		got, _ := json.Marshal(out.Items[0].Config)
		var original, encoded map[string]json.RawMessage
		_ = json.Unmarshal([]byte(config), &original)
		_ = json.Unmarshal(got, &encoded)
		for _, key := range []string{"hourlyConfig", "dailyConfig"} {
			want := original[key]
			if string(want) == "null" {
				want = nil
			}
			if !reflect.DeepEqual(want, encoded[key]) {
				t.Errorf("%s = %s, want %s", key, encoded[key], want)
			}
		}
	}
}

func TestSnapshotReadsResponseErrors(t *testing.T) {
	for _, policies := range []bool{false, true} {
		for _, body := range []string{``, `null`, `[]`, `{}`, `{"items":null}`, `{"items":"secret-marker"}`, `{"items":[]}`, `{"items":[],"page":null,"pageSize":10,"totalPages":0,"totalItems":0}`, `{"items":[],"page":1,"pageSize":"secret-marker","totalPages":0,"totalItems":0}`, `{"items":[],"page":1,"pageSize":10,"totalPages":0}`, `{"items":[{"config":{"hour":"secret-marker"}}],"page":1,"pageSize":10,"totalPages":1,"totalItems":1}`, `{"items":[],"page":1,"pageSize":10,"totalPages":0,"totalItems":0}`} {
			if !policies && strings.Contains(body, `"page"`) {
				continue
			}
			if !policies && strings.Contains(body, `"config"`) {
				continue
			}
			c, _, _ := snapshotClient(t, "hcm-3", true, "project-1", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = fmt.Fprint(w, body) }))
			var err error
			var count int
			if policies {
				var out *ListSnapshotPoliciesOutput
				out, err = c.ListSnapshotPolicies(context.Background(), &ListSnapshotPoliciesInput{BackendID: "backend-1"})
				if out != nil {
					count = len(out.Items)
				}
			} else {
				var out *ListSnapshotBackendsOutput
				out, err = c.ListSnapshotBackends(context.Background(), &ListSnapshotBackendsInput{Name: "HCM-03"})
				if out != nil {
					count = len(out.Items)
				}
			}
			valid := body == `{"items":[]}` && !policies || body == `{"items":[],"page":1,"pageSize":10,"totalPages":0,"totalItems":0}`
			if valid {
				if err != nil || count != 0 {
					t.Fatalf("empty result: %v", err)
				}
			} else if err == nil || strings.Contains(err.Error(), "secret-marker") {
				t.Errorf("invalid response accepted or echoed: %v", err)
			}
		}
		for _, status := range []int{401, 403, 404, 429, 500} {
			c, _, _ := snapshotClient(t, "hcm-3", true, "project-1", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(status)
				_, _ = fmt.Fprint(w, `{"message":"failure"}`)
			}))
			var err error
			if policies {
				_, err = c.ListSnapshotPolicies(context.Background(), &ListSnapshotPoliciesInput{BackendID: "backend-1"})
			} else {
				_, err = c.ListSnapshotBackends(context.Background(), &ListSnapshotBackendsInput{Name: "HCM-03"})
			}
			var api *vngcloud.APIError
			if !errors.As(err, &api) || api.StatusCode != status {
				t.Errorf("status %d: %v", status, err)
			}
			sentinel := map[int]error{401: vngcloud.ErrAuth, 403: vngcloud.ErrPermission, 404: vngcloud.ErrNotFound, 429: vngcloud.ErrRateLimited}[status]
			if sentinel != nil && !errors.Is(err, sentinel) {
				t.Errorf("status mapping: %v", err)
			}
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		c, _, requests := snapshotClient(t, "hcm-3", true, "project-1", http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("unexpected request") }))
		var err error
		if policies {
			_, err = c.ListSnapshotPolicies(ctx, &ListSnapshotPoliciesInput{BackendID: "backend-1"})
		} else {
			_, err = c.ListSnapshotBackends(ctx, &ListSnapshotBackendsInput{Name: "HCM-03"})
		}
		if !errors.Is(err, context.Canceled) || *requests != 0 {
			t.Errorf("cancellation: %v", err)
		}
	}
}

func TestSnapshotReadsRedactEchoedToken(t *testing.T) {
	for _, operation := range []struct {
		name string
		call func(*Client) error
	}{
		{"backends", func(c *Client) error {
			_, err := c.ListSnapshotBackends(context.Background(), &ListSnapshotBackendsInput{Name: "HCM-03"})
			return err
		}},
		{"policies", func(c *Client) error {
			_, err := c.ListSnapshotPolicies(context.Background(), &ListSnapshotPoliciesInput{BackendID: "backend-1"})
			return err
		}},
	} {
		for _, status := range []int{http.StatusForbidden, http.StatusInternalServerError} {
			t.Run(fmt.Sprintf("%s/%d", operation.name, status), func(t *testing.T) {
				const token = "test-token"
				c, _, requests := snapshotClient(t, "hcm-3", true, "project-1", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					received := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
					if received != token {
						t.Error("unexpected bearer token")
					}
					w.WriteHeader(status)
					_, _ = fmt.Fprintf(w, `{"code":%q,"message":%q}`, received, "denied "+received)
				}))
				err := operation.call(c)
				var api *vngcloud.APIError
				if !errors.As(err, &api) || api.StatusCode != status {
					t.Fatalf("error = %v, want APIError with status %d", err, status)
				}
				for name, text := range map[string]string{"error": err.Error(), "code": api.Code, "message": api.Message} {
					if strings.Contains(text, token) {
						t.Errorf("%s exposed bearer token", name)
					}
				}
				if api.Code != "[redacted]" || api.Message != "denied [redacted]" {
					t.Fatal("error fields lost redacted server text")
				}
				if *requests != 1 {
					t.Fatalf("requests=%d, want 1", *requests)
				}
			})
		}
	}
}

func TestSnapshotPolicyRetainedFields(t *testing.T) {
	c, _, _ := snapshotClient(t, "hcm-3", true, "project-1", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		testutil.WriteFixture(t, w, "../testdata/volume/snapshot_policies.json")
	}))
	out, err := c.ListSnapshotPolicies(context.Background(), &ListSnapshotPoliciesInput{BackendID: "backend-1"})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("../testdata/volume/snapshot_policies.json")
	if err != nil {
		t.Fatal(err)
	}
	var envelope struct{ Items []map[string]any }
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(out.Items)
	if err != nil {
		t.Fatal(err)
	}
	var items []map[string]any
	if err := json.Unmarshal(encoded, &items); err != nil {
		t.Fatal(err)
	}
	for i, row := range envelope.Items {
		for _, key := range []string{"userId", "backendId", "projectId", "isDefault", "deletedAt"} {
			delete(row, key)
		}
		config := row["config"].(map[string]any)
		delete(config, "weeklyConfig")
		delete(config, "monthlyConfig")
		if !reflect.DeepEqual(row, items[i]) {
			t.Fatalf("retained fields differ in row %d", i)
		}
	}
}

func TestSnapshotPolicyIntegralDecimalNumbers(t *testing.T) {
	for _, tc := range []struct {
		number string
		valid  bool
	}{{"0.0", true}, {"1.0", true}, {"1e1", true}, {"0.5", false}, {`"1"`, false}, {"9223372036854775808", false}, {"1e100", false}} {
		t.Run(tc.number, func(t *testing.T) {
			c, _, _ := snapshotClient(t, "hcm-3", true, "project-1", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = fmt.Fprintf(w, `{"items":[{"config":{"hour":%s,"minute":%s,"hourlyConfig":{"interval":%s,"retention":%s},"dailyConfig":{"retention":%s}}}],"page":1,"pageSize":10,"totalPages":1,"totalItems":1}`, tc.number, tc.number, tc.number, tc.number, tc.number)
			}))
			_, err := c.ListSnapshotPolicies(context.Background(), &ListSnapshotPoliciesInput{BackendID: "backend-1"})
			if (err == nil) != tc.valid {
				t.Errorf("decode valid=%t: %v", tc.valid, err)
			}
		})
	}
}

type snapshotTransport func(*http.Request) (*http.Response, error)

func (f snapshotTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestSnapshotReadsDefaultDestination(t *testing.T) {
	provider := &snapshotCredentials{}
	requests := 0
	cfg, err := vngcloud.NewConfig(vngcloud.WithRegion("hcm-3"), vngcloud.WithProjectID("project-1"), vngcloud.WithCredentialsProvider(provider), vngcloud.WithHTTPClient(&http.Client{Transport: snapshotTransport(func(r *http.Request) (*http.Response, error) {
		requests++
		if r.Method != "GET" || r.URL.Host != "hcm-3.console.greennode.ai" || r.URL.Scheme != "https" || r.Header.Get("Authorization") != "Bearer test-token" {
			t.Error("wrong destination or authentication")
		}
		switch r.URL.Path {
		case "/vserver/vbackup-gateway/v1/backends":
			if r.URL.RawQuery != "backend=HCM-03" {
				t.Error("wrong backend query")
			}
		case "/vserver/vbackup-gateway/v1/snapshot-policies":
			if r.URL.RawQuery != "backendId=backend-1&page=1&projectId=project-1&size=10" {
				t.Error("wrong policy query")
			}
		default:
			t.Error("unverified route")
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"items":[],"page":1,"pageSize":10,"totalPages":0,"totalItems":0}`))}, nil
	})}))
	if err != nil {
		t.Fatal(err)
	}
	c := New(cfg)
	if _, err := c.ListSnapshotBackends(context.Background(), &ListSnapshotBackendsInput{Name: "HCM-03"}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ListSnapshotPolicies(context.Background(), &ListSnapshotPoliciesInput{BackendID: "backend-1"}); err != nil {
		t.Fatal(err)
	}
	if requests != 2 || provider.calls != 2 {
		t.Fatalf("requests=%d; credential calls=%d", requests, provider.calls)
	}
}

func TestSnapshotPolicySyntheticFieldsAndSecretOmissions(t *testing.T) {
	const body = `{"items":[{"id":"policy-1","name":"synthetic","policyType":"FUTURE","createdAt":"created","updatedAt":"updated","snapshotServerCount":1,"snapshotVolumeCount":2,"token":"secret-marker","config":{"hour":8.0,"minute":15.0,"timeZone":"UTC","hourlyEnabled":true,"dailyEnabled":true,"weeklyEnabled":true,"monthlyEnabled":true,"isProtectedServer":true,"statusSendEmail":["FUTURE"],"hourlyConfig":{"interval":1.0},"dailyConfig":{},"weeklyConfig":{"token":"secret-marker"},"monthlyConfig":{}}}],"page":1,"pageSize":10,"totalPages":1,"totalItems":1}`
	c, _, _ := snapshotClient(t, "hcm-3", true, "project-1", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = fmt.Fprint(w, body) }))
	out, err := c.ListSnapshotPolicies(context.Background(), &ListSnapshotPoliciesInput{BackendID: "backend-1"})
	if err != nil {
		t.Fatal(err)
	}
	p := out.Items[0]
	cfg := p.Config
	if p.ID != "policy-1" || p.Name != "synthetic" || p.PolicyType != "FUTURE" || p.CreatedAt != "created" || p.UpdatedAt != "updated" || p.SnapshotServerCount != 1 || p.SnapshotVolumeCount != 2 || cfg.Hour != 8 || cfg.Minute != 15 || cfg.TimeZone != "UTC" || !cfg.HourlyEnabled || !cfg.DailyEnabled || !cfg.WeeklyEnabled || !cfg.MonthlyEnabled || !cfg.IsProtectedServer || !reflect.DeepEqual(cfg.StatusSendEmail, []string{"FUTURE"}) || cfg.HourlyConfig.Interval == nil || *cfg.HourlyConfig.Interval != 1 || cfg.HourlyConfig.Retention != nil {
		t.Fatal("synthetic field lost or mistyped")
	}
	encoded, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "secret-marker") {
		t.Fatal("unknown secret field exposed")
	}
}
