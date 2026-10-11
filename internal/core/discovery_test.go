package core

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"danny.vn/vngcloud/internal/discovery"
	"danny.vn/vngcloud/internal/transport"
)

func discoveryTestContext() context.Context {
	now := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	return discovery.WithTestClock(context.Background(), func() time.Time { return now }, func(ctx context.Context, d time.Duration) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		now = now.Add(d)
		return nil
	})
}

func TestDiscoveryProjectFixtureAndGuard(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/project/list_projects.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, reflect := range []bool{false, true} {
		t.Run(map[bool]string{false: "fixture", true: "reflection"}[reflect], func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/projects" {
					t.Errorf("path=%s", r.URL.Path)
				}
				if reflect {
					_, _ = w.Write([]byte(`{"projects":[{"projectId":"canary-secret","region":"hcm-3"}]}`))
					return
				}
				_, _ = w.Write(raw)
			}))
			defer srv.Close()
			c, err := newClient(WithRegion("hcm-3"), WithStaticToken("canary-secret"), WithEndpointOverrides(EndpointOverrides{VServer: srv.URL}))
			if err != nil {
				t.Fatal(err)
			}
			s, err := discovery.New(discoveryTestContext(), 2)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			id, err := c.RequireDiscoveryProjectID(s)
			if reflect {
				var api *APIError
				if !errors.As(err, &api) || api.Code != "InvalidResponse" || id != "" || c.ProjectID() != "" {
					t.Fatalf("id=%q err=%v", id, err)
				}
				return
			}
			if err != nil || id == "" || s.Requests() != 1 {
				t.Fatalf("id=%q requests=%d err=%v", id, s.Requests(), err)
			}
			if _, err := c.RequireDiscoveryProjectID(s); err != nil || s.Requests() != 1 {
				t.Fatalf("cache requests=%d err=%v", s.Requests(), err)
			}
		})
	}
}

func TestGuardedAuxiliaryFixtures(t *testing.T) {
	fixtures := []struct{ file, path string }{
		{"portal/discovery_zones.json", "/v1/p-1/zones"},
		{"volume/list_volume_type_zones.json", "/v1/p-1/volume_type_zones"},
		{"volume/discovery_types.json", "/v1/p-1/tz-1/volume_types"},
		{"volume/discovery_encryption_types.json", "/v1/p-1/volumes/encryption_types"},
		{"storage/discovery_regions.json", "/internal/v1/regions"},
		{"storage/discovery_project_types.json", "/internal/v1/billing/project_types"},
		{"storage/discovery_purchase_types.json", "/internal/v1/billing/purchase_types"},
		{"storage/discovery_config_min_quota.json", "/billing-api/v1/configurations"},
		{"storage/discovery_config_max_quota.json", "/billing-api/v1/configurations"},
		{"storage/discovery_config_max_projects.json", "/billing-api/v1/configurations"},
		{"storage/discovery_config_iam_checkout.json", "/billing-api/v1/configurations"},
	}
	for _, fixture := range fixtures {
		t.Run(fixture.file, func(t *testing.T) {
			raw, err := os.ReadFile("../../testdata/" + fixture.file)
			if err != nil {
				t.Fatal(err)
			}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" || r.URL.Path != fixture.path {
					t.Errorf("request=%s %s", r.Method, r.URL.Path)
				}
				_, _ = w.Write(raw)
			}))
			defer srv.Close()
			c, err := newClient(WithRegion("hcm-3"), WithStaticToken("canary-secret"))
			if err != nil {
				t.Fatal(err)
			}
			s, _ := discovery.New(discoveryTestContext(), 1)
			defer s.Close()
			var out any
			status, err := s.DoJSON(c, transport.Request{Operation: "test.Catalog", Method: "GET", URL: srv.URL + fixture.path}, &out)
			if err != nil || status != 200 || out == nil || s.Requests() != 1 {
				t.Fatalf("status=%d requests=%d err=%v", status, s.Requests(), err)
			}
		})
	}
}

func TestDiscoveryProjectSchemaAndErrors(t *testing.T) {
	cases := []struct {
		body   string
		status int
		want   error
	}{
		{`{}`, 200, nil}, {`{"projects":null}`, 200, nil},
		{`{"projects":[],"nextPage":1}`, 200, nil},
		{`{"projects":[{"projectId":"p-1"},{"projectId":"p-1"}]}`, 200, nil},
		{`{"projects":[{"projectId":"p-1"},{"projectId":"p-2"}]}`, 200, ErrProjectAmbiguous},
		{`{"projects":[]}`, 200, ErrProjectNotFound},
		{`{"message":"refused"}`, 400, nil}, {`{"message":"refused"}`, 401, ErrAuth},
		{`{"message":"refused"}`, 403, ErrPermission}, {`{"message":"refused"}`, 404, ErrNotFound},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("%d/%s", tc.status, tc.body), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			c, err := newClient(WithRegion("hcm-3"), WithStaticToken("canary-secret"), WithEndpointOverrides(EndpointOverrides{VServer: srv.URL}))
			if err != nil {
				t.Fatal(err)
			}
			s, _ := discovery.New(discoveryTestContext(), 2)
			defer s.Close()
			id, err := c.RequireDiscoveryProjectID(s)
			if err == nil || id != "" || c.ProjectID() != "" {
				t.Fatalf("id=%q err=%v", id, err)
			}
			if tc.want != nil {
				if !errors.Is(err, tc.want) {
					t.Fatalf("err=%v want=%v", err, tc.want)
				}
				return
			}
			var api *APIError
			if !errors.As(err, &api) || api.StatusCode != tc.status {
				t.Fatalf("err=%v", err)
			}
			if tc.status == 200 && api.Code != "InvalidResponse" {
				t.Fatalf("code=%s", api.Code)
			}
		})
	}
}

func TestGuardedAuxiliaryDecodeErrorIdentity(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"zones":[]}`)) }))
	defer srv.Close()
	c, err := newClient(WithRegion("hcm-3"), WithStaticToken("canary-secret"))
	if err != nil {
		t.Fatal(err)
	}
	s, _ := discovery.New(discoveryTestContext(), 1)
	defer s.Close()
	var out []string
	_, err = s.DoJSON(c, transport.Request{Method: "GET", URL: srv.URL + "/v1/p-1/zones"}, &out)
	var api *APIError
	if !errors.As(err, &api) || api.StatusCode != 200 || api.Code != "InvalidResponse" {
		t.Fatalf("error=%v", err)
	}
}

func TestDiscoveryProjectContinuationNotCached(t *testing.T) {
	for _, marker := range []string{"true", "false", "null"} {
		t.Run(marker, func(t *testing.T) {
			reads := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				reads++
				body := `{"projects":[{"projectId":"p-1","region":"hcm-3"}]`
				if reads == 1 {
					body += `,"isNext":` + marker
				}
				_, _ = w.Write([]byte(body + `}`))
			}))
			defer srv.Close()
			c, err := newClient(WithRegion("hcm-3"), WithStaticToken("canary-secret"), WithEndpointOverrides(EndpointOverrides{VServer: srv.URL}))
			if err != nil {
				t.Fatal(err)
			}
			s, _ := discovery.New(discoveryTestContext(), 2)
			defer s.Close()
			id, err := c.RequireDiscoveryProjectID(s)
			var api *APIError
			if id != "" || c.ProjectID() != "" || !errors.As(err, &api) || api.Code != "InvalidResponse" || api.StatusCode != 200 {
				t.Fatalf("id=%q err=%v", id, err)
			}
			id, err = c.RequireDiscoveryProjectID(s)
			if err != nil || id != "p-1" || reads != 2 || s.Requests() != 2 {
				t.Fatalf("id=%q err=%v reads=%d", id, err, reads)
			}
		})
	}
}
