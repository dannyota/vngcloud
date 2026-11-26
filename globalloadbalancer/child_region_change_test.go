package globalloadbalancer

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"danny.vn/vngcloud"
)

func regionCatalogFixture(t *testing.T, name string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("..", "testdata", "globalloadbalancer", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func regionTestClient(t *testing.T, f *fixture, region string) *Client {
	t.Helper()
	for key, name := range map[string]string{"GET /v1/regions": "child_regions.json", "GET /v1/packages": "child_packages.json"} {
		if f.override[key] == nil {
			f.on(key, jsonBody(regionCatalogFixture(t, name)))
		}
	}
	api := glbRoundTrip(func(r *http.Request) (*http.Response, error) {
		clone := r.Clone(r.Context())
		u := *r.URL
		clone.URL = &u
		clone.URL.Path = strings.TrimPrefix(u.Path, "/glb-controller")
		w := httptest.NewRecorder()
		f.ServeHTTP(w, clone)
		return w.Result(), nil
	})
	auth := glbRoundTrip(func(r *http.Request) (*http.Response, error) {
		w := httptest.NewRecorder()
		switch {
		case r.URL.Host == "signin.greennode.ai" && r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`<html><input name="_csrf" value="csrf"></html>`))
		case r.URL.Host == "signin.greennode.ai":
			w.Header().Set("Location", "https://dashboard.console.greennode.ai/?code=synthetic")
			w.WriteHeader(http.StatusFound)
		default:
			_, _ = w.Write([]byte(`{"accessToken":"synthetic","expiresIn":3600}`))
		}
		return w.Result(), nil
	})
	cfg, err := vngcloud.NewConfig(vngcloud.WithRegion(region), vngcloud.WithProjectID(fxProject), vngcloud.WithIAMUser(&vngcloud.IAMUserAuth{RootEmail: "root", Username: "user", Password: "synthetic", HTTPClient: &http.Client{Transport: auth}}), vngcloud.WithTransport(api), vngcloud.WithRetry(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	return New(cfg)
}

func TestChildRegionCatalogRefusals(t *testing.T) {
	regions := regionCatalogFixture(t, "child_regions.json")
	packages := regionCatalogFixture(t, "child_packages.json")
	for name, tc := range map[string]struct {
		key, body string
		invalid   bool
	}{
		"missing status":          {"GET /v1/regions", strings.Replace(regions, `,"status":""`, ``, 1), true},
		"null status":             {"GET /v1/regions", strings.Replace(regions, `"status":""`, `"status":null`, 1), true},
		"ambiguous name":          {"GET /v1/regions", strings.Replace(regions, `"name":"HAN-1"`, `"name":"hcm-3"`, 1), false},
		"ambiguous package alias": {"GET /v1/regions", strings.Replace(regions, `"name":"HAN-1"`, `"name":"hcm"`, 1), false},
		"shared id":               {"GET /v1/regions", strings.Replace(regions, `"id":"han"`, `"id":"HCM"`, 1), false},
		"foreign package":         {"GET /v1/packages", strings.Replace(packages, `"glb_package_id":"pkg-1"`, `"glb_package_id":"pkg-2"`, 1), false},
		"conflicting aliases":     {"GET /v1/packages", strings.Replace(packages, `"region":"han"`, `"region":"HCM-3"`, 1), false},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t).on(tc.key, jsonBody(tc.body))
			_, err := regionTestClient(t, f, "hcm-3").CreatePool(bg(), poolInput())
			var api *vngcloud.APIError
			if err == nil || f.writes() != 0 || (tc.invalid && (!errors.As(err, &api) || api.Code != "InvalidResponse")) {
				t.Fatalf("err=%v writes=%d", err, f.writes())
			}
		})
	}
}

func TestChildWritesInAnotherRegion(t *testing.T) {
	f := newFixture(t)
	f.regionID = "han"
	c := regionTestClient(t, f, "han-1")
	if _, err := c.CreatePool(bg(), poolInput()); err != nil {
		t.Fatal(err)
	}
	if _, err := c.CreateListener(bg(), listenerInput()); err != nil {
		t.Fatal(err)
	}
	if _, err := c.DeleteListener(bg(), &DeleteListenerInput{LoadBalancerID: fxGLB, ListenerID: fxListener}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.DeletePool(bg(), &DeletePoolInput{LoadBalancerID: fxGLB, PoolID: fxPool}); err != nil {
		t.Fatal(err)
	}
}

func TestChildDeletesRefuseMismatchedGroupRegion(t *testing.T) {
	for _, tc := range deleteCases() {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			tc.setup(f)
			f.regionID = "han"
			_, _, err := tc.call(bg(), regionTestClient(t, f, "hcm-3"))
			if err == nil || f.writes() != 0 {
				t.Fatalf("err=%v writes=%d", err, f.writes())
			}
		})
	}
}

func TestChildDeletesRefuseSharedGLBRegionID(t *testing.T) {
	for _, tc := range deleteCases() {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			tc.setup(f)
			f.on("GET /v1/regions", jsonBody(strings.Replace(regionCatalogFixture(t, "child_regions.json"), `"id":"han"`, `"id":"hcm"`, 1)))
			_, _, err := tc.call(bg(), regionTestClient(t, f, "hcm-3"))
			if err == nil || f.writes() != 0 {
				t.Fatalf("err=%v writes=%d", err, f.writes())
			}
		})
	}
}
