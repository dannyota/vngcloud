package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/endpoints"
	"danny.vn/vngcloud/internal/testutil"
	"danny.vn/vngcloud/internal/transport"
)

type credentialTestTokens struct {
	values []string
	calls  atomic.Int32
}

func (s *credentialTestTokens) Token(context.Context) (transport.Token, error) {
	i := min(int(s.calls.Add(1))-1, len(s.values)-1)
	return transport.Token{AccessToken: s.values[i], ExpiresAt: time.Now().Add(time.Hour)}, nil
}

func (*credentialTestTokens) Invalidate(string) {}

func TestEnvelopeOverlappingCredentials(t *testing.T) {
	const first = "synthetic-shared-token"
	const second = first + "-SECRET-TAIL"
	for _, reverse := range []bool{false, true} {
		for _, kind := range []string{"read", "price", "buckets"} {
			t.Run(fmt.Sprintf("reverse=%t/%s", reverse, kind), func(t *testing.T) {
				values := []string{first, second}
				if reverse {
					values[0], values[1] = values[1], values[0]
				}
				var attempts atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					if attempts.Add(1) == 1 {
						w.WriteHeader(http.StatusUnauthorized)
						return
					}
					_, _ = fmt.Fprintf(w, `{"success":false,"code":%q,"errorMsg":%q}`, second, second)
				}))
				defer server.Close()
				tc := transport.New(transport.Config{HTTPClient: server.Client(), TokenSource: &credentialTestTokens{values: values}})
				c := New(core.NewTestConfig("hcm-3", "", endpoints.Set{Storage: server.URL + "/"}, tc))
				var err error
				switch kind {
				case "read":
					_, err = c.ListRegions(context.Background(), nil)
				case "price":
					_, err = c.exchangeProjectPrice(context.Background(), call{op: "storage.QuoteCreateProject", method: http.MethodPost, url: server.URL, ok: []int{200}})
				case "buckets":
					_, err = c.readProjectBuckets(context.Background(), "storage.DeleteProject", "region-1", "project-1")
				}
				var api *core.APIError
				if attempts.Load() != 2 || !errors.As(err, &api) {
					t.Fatal("expected refresh and envelope failure")
				}
				if api.Code != "[redacted]" || api.Message != "[redacted]" {
					t.Error("envelope retained an overlapping credential suffix")
				}
			})
		}
	}
}

func TestStorageDecodeCauseWithholdsPeerValues(t *testing.T) {
	const secret = "123456789e999"
	for _, kind := range []string{"regions", "buckets", "bucket", "delete guard"} {
		t.Run(kind, func(t *testing.T) {
			var attempts, deletes atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodDelete {
					deletes.Add(1)
				}
				if kind != "regions" && strings.HasSuffix(r.URL.Path, "/regions") {
					_, _ = w.Write([]byte(`{"success":true,"data":[{"regionId":"region-1","regionName":"HCM04"}]}`))
					return
				}
				if attempts.Add(1) == 1 {
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
				switch kind {
				case "regions":
					_, _ = fmt.Fprintf(w, `{"success":true,"data":[{"status":%s}]}`, secret)
				case "buckets":
					_, _ = fmt.Fprintf(w, `{"success":true,"data":[{"count":%s}]}`, secret)
				default:
					_, _ = fmt.Fprintf(w, `{"success":true,"data":{"count":%s}}`, secret)
				}
			}))
			defer server.Close()
			tc := transport.New(transport.Config{HTTPClient: server.Client(), TokenSource: &credentialTestTokens{values: []string{secret, "synthetic-fresh-token"}}})
			c := New(core.NewTestConfig("hcm-3", "", endpoints.Set{Storage: server.URL + "/"}, tc))
			var err error
			switch kind {
			case "regions":
				var out *ListRegionsOutput
				out, err = c.ListRegions(context.Background(), nil)
				if out != nil {
					t.Fatal("decoder failure returned partial regions")
				}
			case "buckets":
				var out *ListBucketsOutput
				out, err = c.ListBuckets(context.Background(), &ListBucketsInput{Region: "HCM04", ProjectID: "project-1"})
				if out != nil {
					t.Fatal("decoder failure returned partial buckets")
				}
			case "bucket":
				var out *GetBucketOutput
				out, err = c.GetBucket(context.Background(), &GetBucketInput{Region: "HCM04", ProjectID: "project-1", Bucket: "bucket-1"})
				if out != nil {
					t.Fatal("decoder failure returned partial bucket")
				}
			case "delete guard":
				var out *DeleteBucketOutput
				out, err = c.DeleteBucket(context.Background(), &DeleteBucketInput{Region: "HCM04", ProjectID: "project-1", Bucket: "bucket-1"})
				if out != nil {
					t.Fatal("decoder failure reached mutation")
				}
			}
			var api *core.APIError
			var typed *json.UnmarshalTypeError
			if attempts.Load() != 2 || deletes.Load() != 0 || !errors.As(err, &api) || !errors.As(err, &typed) {
				t.Fatal("expected refresh, safe APIError, JSON type identity, and no delete")
			}
			for cause := err; cause != nil; cause = errors.Unwrap(cause) {
				if strings.Contains(cause.Error(), secret) {
					t.Error("decoder error chain retained an echoed credential")
				}
			}
			if typed.Value != "string" || typed.Struct != "" || typed.Field != "" || typed.Offset != 2 {
				t.Error("JSON type error retained original exported fields")
			}
		})
	}
}

func TestEnvelopeRedactsEarlierAttempt(t *testing.T) {
	for _, kind := range []string{"read", "price", "buckets"} {
		t.Run(kind, func(t *testing.T) {
			var sent []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				sent = append(sent, strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
				if len(sent) == 1 {
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
				_, _ = fmt.Fprintf(w, `{"success":false,"code":%q,"errorMsg":%q}`, sent[0], "bad "+strings.Join(sent, " "))
			}))
			defer server.Close()
			tc := transport.New(transport.Config{HTTPClient: server.Client(), TokenSource: &rotatingEnvelopeTokens{}})
			c := New(core.NewTestConfig("hcm-3", "", endpoints.Set{Region: "hcm-3", Storage: server.URL + "/"}, tc))
			var err error
			switch kind {
			case "read":
				_, err = c.ListRegions(context.Background(), nil)
			case "price":
				_, err = c.exchangeProjectPrice(context.Background(), call{op: "storage.QuoteCreateProject", method: http.MethodPost, url: server.URL, ok: []int{200}})
			case "buckets":
				_, err = c.readProjectBuckets(context.Background(), "storage.DeleteProject", "region-1", "project-1")
			}
			var api *core.APIError
			if len(sent) != 2 || sent[0] == sent[1] || !errors.As(err, &api) {
				t.Fatalf("expected refresh and envelope error, got %v", err)
			}
			for _, credential := range sent {
				if strings.Contains(api.Message, credential) || strings.Contains(api.Code, credential) || strings.Contains(api.Error(), credential) {
					t.Error("envelope leaked an earlier credential")
				}
			}
		})
	}
}

const fixtures = "../testdata/storage/"

func newTestClient(t *testing.T, h http.Handler) *Client {
	t.Helper()
	return New(testutil.NewConfig(t, h))
}

// serve answers GET internal/v1/regions with the regions fixture and every
// other path with fn.
func serve(t *testing.T, fn func(w http.ResponseWriter, r *http.Request)) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/internal/v1/regions" {
			testutil.WriteFixture(t, w, fixtures+"list_regions.json")
			return
		}
		fn(w, r)
	})
}

func TestListRegionsDecodesFixture(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/v1/regions" {
			t.Errorf("path = %s", r.URL.Path)
		}
		for _, h := range []string{"region", "region_id"} {
			if got := r.Header.Get(h); got != "" {
				t.Errorf("%s = %q, want none", h, got)
			}
		}
		testutil.WriteFixture(t, w, fixtures+"list_regions.json")
	}))
	out, err := c.ListRegions(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Items) != 2 {
		t.Fatalf("items = %d, want 2", len(out.Items))
	}
	r := out.Items[1]
	if r.ID != "<region-id-2>" || r.Name != "HCM04" || r.BackendType != "ceph" ||
		r.S3Host != "https://hcm04.vstorage.vngcloud.vn" || r.Status != 1 || r.AccountURL == "" || r.AuthHost == "" || r.VOSAPIHost == "" {
		t.Fatalf("unexpected region: %+v", r)
	}
}

func TestListProjectsSendsBothRegionHeaders(t *testing.T) {
	c := newTestClient(t, serve(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/v1/projects" {
			t.Errorf("path = %s", r.URL.Path)
		}
		checkRegionHeaders(t, r, "<region-id-2>")
		testutil.WriteFixture(t, w, fixtures+"list_projects.json")
	}))
	out, err := c.ListProjects(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Items) != 1 {
		t.Fatalf("items = %d, want 1", len(out.Items))
	}
	p := out.Items[0]
	if p.ID != "00000000000000000000000000000000" || p.Name != "<project-name>" || p.RegionID != "<region-id-2>" ||
		p.RegionName != "HCM04" || p.Status != 1 || p.TotalQuota != 30 || p.Period != 0 ||
		p.StartTime != "2026-10-09T14:23:05.933+00:00" || p.EndTime != p.StartTime {
		t.Fatalf("unexpected project: %+v", p)
	}
}

// checkRegionHeaders asserts that r carries region and region_id, both want.
func checkRegionHeaders(t *testing.T, r *http.Request, want string) {
	t.Helper()
	for _, h := range []string{"region", "region_id"} {
		if got := r.Header.Get(h); got != want {
			t.Errorf("%s %s header = %q, want %q", r.URL.Path, h, got, want)
		}
	}
}

func TestStorageReadsSendBothRegionHeaders(t *testing.T) {
	seen := map[string]bool{}
	c := newTestClient(t, serve(t, func(w http.ResponseWriter, r *http.Request) {
		checkRegionHeaders(t, r, "<region-id-2>")
		seen[r.URL.Path] = true
		file := "list_projects_empty.json"
		switch r.URL.Path {
		case "/internal/v1/ceph/projects/proj-1":
			file = "list_buckets.json"
		case "/internal/v1/ceph/projects/proj-1/b1/details":
			file = "get_bucket.json"
		}
		testutil.WriteFixture(t, w, fixtures+file)
	}))
	ctx := context.Background()
	if _, err := c.ListProjects(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ListBuckets(ctx, &ListBucketsInput{ProjectID: "proj-1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.GetBucket(ctx, &GetBucketInput{ProjectID: "proj-1", Bucket: "b1"}); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 3 {
		t.Fatalf("requests seen: %v, want projects, buckets, and bucket details", seen)
	}
}

func TestListProjectsEmpty(t *testing.T) {
	c := newTestClient(t, serve(t, func(w http.ResponseWriter, _ *http.Request) {
		testutil.WriteFixture(t, w, fixtures+"list_projects_empty.json")
	}))
	out, err := c.ListProjects(context.Background(), &ListProjectsInput{Region: "han02"})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Items) != 0 {
		t.Fatalf("items = %d, want 0", len(out.Items))
	}
}

func TestRegionLookupIsCachedPerClient(t *testing.T) {
	var regionCalls atomic.Int32
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/internal/v1/regions" {
			regionCalls.Add(1)
			testutil.WriteFixture(t, w, fixtures+"list_regions.json")
			return
		}
		testutil.WriteFixture(t, w, fixtures+"list_projects_empty.json")
	}))
	for range 3 {
		if _, err := c.ListProjects(context.Background(), nil); err != nil {
			t.Fatal(err)
		}
	}
	if got := regionCalls.Load(); got != 1 {
		t.Fatalf("regions fetched %d times, want 1", got)
	}
}

func TestRegionMapping(t *testing.T) {
	tests := []struct {
		name, region, want string
	}{
		{"explicit han", "HAN02", "<region-id-1>"},
		{"explicit lower case", "hcm04", "<region-id-2>"},
		{"default hcm-3", "", "<region-id-2>"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newTestClient(t, serve(t, func(w http.ResponseWriter, r *http.Request) {
				checkRegionHeaders(t, r, tt.want)
				testutil.WriteFixture(t, w, fixtures+"list_projects_empty.json")
			}))
			if _, err := c.ListProjects(context.Background(), &ListProjectsInput{Region: tt.region}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestUnmappedRegionSendsNothing(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	t.Cleanup(server.Close)
	cfg, err := vngcloud.NewConfig(vngcloud.WithRegion("sg-1"), vngcloud.WithStaticToken("x"),
		vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{Storage: server.URL}))
	if err != nil {
		t.Fatal(err)
	}
	c := New(cfg)
	if _, err := c.ListProjects(context.Background(), nil); !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
	if calls.Load() != 0 {
		t.Fatal("request was sent")
	}
}

func TestUnknownRegionName(t *testing.T) {
	c := newTestClient(t, serve(t, func(http.ResponseWriter, *http.Request) { t.Error("unexpected request") }))
	_, err := c.ListProjects(context.Background(), &ListProjectsInput{Region: "NOPE"})
	if !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
}

func TestEnvelopeFailure(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		testutil.WriteFixture(t, w, fixtures+"error_envelope.json")
	}))
	_, err := c.ListRegions(context.Background(), nil)
	var apiErr *vngcloud.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v, want *APIError", err)
	}
	if apiErr.StatusCode != 200 || apiErr.Code != "114" || apiErr.Message != "<message>" {
		t.Fatalf("unexpected error: %+v", apiErr)
	}
	if vngcloud.IsNotFound(err) || vngcloud.IsPermissionDenied(err) {
		t.Fatalf("code 114 must match no sentinel")
	}
}

func TestEnvelopeCodeMatchesSentinel(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		testutil.WriteFixture(t, w, fixtures+"error_envelope_not_found.json")
	}))
	_, err := c.ListRegions(context.Background(), nil)
	if !errors.Is(err, vngcloud.ErrNotFound) || !vngcloud.IsNotFound(err) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	if got := vngcloud.ErrorCode(err); got != "404" {
		t.Fatalf("code = %q", got)
	}
}

func TestEnvelopeMessageCut(t *testing.T) {
	long := strings.Repeat("é", 300)
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"success":false,"code":1,"errorMsg":"` + long + `"}`))
	}))
	_, err := c.ListRegions(context.Background(), nil)
	var apiErr *vngcloud.APIError
	if !errors.As(err, &apiErr) || len(apiErr.Message) > 256 || !strings.HasPrefix(long, apiErr.Message) {
		t.Fatalf("message not cut: %v", err)
	}
}

func TestEmptyAndNonJSONBody(t *testing.T) {
	for name, body := range map[string]string{"empty": "", "html": "<html>hi</html>", "null": "null", "object": "{}"} {
		t.Run(name, func(t *testing.T) {
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(body))
			}))
			_, err := c.ListRegions(context.Background(), nil)
			var apiErr *vngcloud.APIError
			if !errors.As(err, &apiErr) || apiErr.Code != "EmptyResponse" || apiErr.StatusCode != 200 {
				t.Fatalf("err = %v, want EmptyResponse", err)
			}
		})
	}
}

func TestPermissionArray(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		testutil.WriteFixture(t, w, fixtures+"error_permission.json")
	}))
	_, err := c.ListRegions(context.Background(), nil)
	if !errors.Is(err, vngcloud.ErrPermission) || vngcloud.ErrorCode(err) != "IAM_PERMISSION_DENIED" {
		t.Fatalf("err = %v, code %q", err, vngcloud.ErrorCode(err))
	}
}

func TestDefaultRegionHan1(t *testing.T) {
	var got string
	h := serve(t, func(w http.ResponseWriter, r *http.Request) {
		checkRegionHeaders(t, r, "<region-id-1>")
		got = r.Header.Get("region_id")
		testutil.WriteFixture(t, w, fixtures+"list_projects_empty.json")
	})
	server := httptest.NewServer(h)
	t.Cleanup(server.Close)
	cfg, err := vngcloud.NewConfig(vngcloud.WithRegion("han-1"), vngcloud.WithStaticToken("x"),
		vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{Storage: server.URL}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := New(cfg).ListProjects(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if got != "<region-id-1>" {
		t.Fatalf("region_id = %q, want HAN02's id", got)
	}
}

func TestRegionWithoutIDIsNotCached(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/v1/regions" {
			t.Errorf("unexpected request to %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"success":true,"datas":[{"regionName":"HCM04"},{"regionId":"x","regionName":""}]}`))
	}))
	for _, region := range []string{"HCM04", ""} {
		_, err := c.ListProjects(context.Background(), &ListProjectsInput{Region: region})
		if !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Fatalf("region %q: err = %v, want ErrInvalidInput", region, err)
		}
	}
}

func TestReadHTTPStatusErrors(t *testing.T) {
	tests := []struct {
		status int
		want   error
	}{
		{http.StatusNotFound, vngcloud.ErrNotFound},
		{http.StatusInternalServerError, nil},
	}
	calls := map[string]func(*Client) error{
		"GetBucket": func(c *Client) error {
			_, err := c.GetBucket(context.Background(), &GetBucketInput{ProjectID: "proj-1", Bucket: "my-bucket"})
			return err
		},
		"ListProjects": func(c *Client) error {
			_, err := c.ListProjects(context.Background(), nil)
			return err
		},
	}
	for name, call := range calls {
		for _, tt := range tests {
			t.Run(name+"/"+http.StatusText(tt.status), func(t *testing.T) {
				c := newTestClient(t, serve(t, func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(tt.status)
					_, _ = w.Write([]byte(`{"message":"x"}`))
				}))
				err := call(c)
				var apiErr *vngcloud.APIError
				if !errors.As(err, &apiErr) || apiErr.StatusCode != tt.status {
					t.Fatalf("err = %v, want *APIError with status %d", err, tt.status)
				}
				if tt.want != nil && !errors.Is(err, tt.want) {
					t.Fatalf("err = %v, want %v", err, tt.want)
				}
			})
		}
	}
}
