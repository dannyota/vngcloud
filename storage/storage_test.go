package storage

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/testutil"
)

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
		if got := r.Header.Get("region_id"); got != "" {
			t.Errorf("region_id = %q, want none", got)
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

func TestListProjectsSendsRegionIDHeaderOnly(t *testing.T) {
	c := newTestClient(t, serve(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/v1/projects" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if got := r.Header.Get("region_id"); got != "<region-id-2>" {
			t.Errorf("region_id = %q", got)
		}
		if r.Header.Get("region") != "" {
			t.Errorf("region header must not be sent")
		}
		testutil.WriteFixture(t, w, fixtures+"list_projects.json")
	}))
	out, err := c.ListProjects(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Items) != 1 || out.Items[0].ID != "<project-id>" || out.Items[0].TotalQuota != 30 || out.Items[0].Period != 1 {
		t.Fatalf("unexpected projects: %+v", out.Items)
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
				if got := r.Header.Get("region_id"); got != tt.want {
					t.Errorf("region_id = %q, want %q", got, tt.want)
				}
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
	for name, body := range map[string]string{"empty": "", "html": "<html>hi</html>"} {
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
