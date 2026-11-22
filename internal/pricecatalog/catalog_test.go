package pricecatalog

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/discovery"
	"danny.vn/vngcloud/internal/testutil"
)

func catalogSession(t *testing.T, max int) *discovery.Session {
	t.Helper()
	now := time.Unix(0, 0)
	ctx := discovery.WithTestClock(context.Background(), func() time.Time { return now }, func(ctx context.Context, d time.Duration) error { now = now.Add(d); return ctx.Err() })
	s, err := discovery.New(ctx, max)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s
}

func TestGuardedRegionalCatalogsShareSession(t *testing.T) {
	paths := []string{}
	c := core.ClientOf(testutil.NewConfig(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.RequestURI())
		file := map[string]string{
			"/v1/project-1/zones":                      "portal/discovery_zones.json",
			"/v1/project-1/volume_type_zones":          "volume/list_volume_type_zones.json",
			"/v1/project-1/volume-zone-1/volume_types": "volume/discovery_types.json",
			"/v1/project-1/volumes/encryption_types":   "volume/discovery_encryption_types.json",
			"/v1/project-1/images/os?zoneId=HCM03-1C":  "compute/discovery_os_images.json",
		}[r.URL.RequestURI()]
		if file == "" {
			t.Errorf("unexpected route %s", r.URL)
			w.WriteHeader(500)
			return
		}
		testutil.WriteFixture(t, w, "../../testdata/"+file)
	})))
	s := catalogSession(t, 5)
	zones, err := Zones(c, s, "project-1")
	if err != nil || len(zones) != 4 {
		t.Fatalf("zones %+v error %v", zones, err)
	}
	parents, err := VolumeParents(c, s, "project-1")
	if err != nil || len(parents) != 1 {
		t.Fatalf("parents %+v error %v", parents, err)
	}
	types, err := VolumeTypes(c, s, "project-1", parents[0].ID)
	if err != nil || len(types) != 4 || types[0].QuoteID() != "vtype-1" {
		t.Fatalf("types %+v error %v", types, err)
	}
	keys, err := Encryptions(c, s, "project-1")
	if err != nil || len(keys) != 2 {
		t.Fatalf("keys %+v error %v", keys, err)
	}
	images, err := Images(c, s, "project-1", "HCM03-1C")
	if err != nil || len(images) == 0 {
		t.Fatalf("images %d error %v", len(images), err)
	}
	encoded, err := json.Marshal(images)
	if err != nil || strings.Contains(string(encoded), "licenseKey") {
		t.Fatalf("retained image license key, error %v", err)
	}
	if _, err := Zones(c, s, "project-1"); err != nil || len(paths) != 5 || s.Requests() != 5 {
		t.Fatalf("requests %d paths %v error %v", s.Requests(), paths, err)
	}
}

func TestCatalogRejectionBeforeMemoization(t *testing.T) {
	for _, body := range []string{`{}`, `{"data":null}`, `{"data":[{"uuid":"z-1"}]}`, `{"data":[{"uuid":"z-1","isEnabled":true},{"uuid":"z-1","isEnabled":false}]}`, `{"data":[],"isNext":false}`, `{"data":[{"uuid":"z-1","isEnabled":false,"isEnabled":true}]}`, `{"data":[{"uuid":"z-1","IsEnabled":true}]}`} {
		t.Run(body, func(t *testing.T) {
			calls := 0
			c := core.ClientOf(testutil.NewConfig(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls++; _, _ = w.Write([]byte(body)) })))
			s := catalogSession(t, 2)
			for range 2 {
				_, err := Zones(c, s, "project-1")
				var api *core.APIError
				if !errors.As(err, &api) || api.Code != "InvalidResponse" || api.StatusCode != 200 {
					t.Fatalf("error %v", err)
				}
			}
			if calls != 2 {
				t.Fatalf("cached invalid catalog: %d calls", calls)
			}
		})
	}
}

func TestSafeImageSelectors(t *testing.T) {
	var image Image
	if err := json.Unmarshal([]byte(`{"id":"i-1","imageType":"uBuNtU","imageVersion":"v1","licence":false,"licenseKey":"discard","packageLimit":{"cpu":"bad","memory":null,"diskSize":20}}`), &image); err != nil {
		t.Fatal(err)
	}
	if !Ubuntu(image.ImageType) || !image.UnlicensedUbuntu() || image.PackageLimit.CPU != nil || image.PackageLimit.Memory != nil || image.PackageLimit.DiskSize == nil || *image.PackageLimit.DiskSize != 20 {
		t.Fatalf("image %+v", image)
	}
	for _, value := range []string{" Ubuntu", "ubuntü", "CentOS"} {
		if Ubuntu(value) {
			t.Fatalf("accepted %s", value)
		}
	}
}

func TestGuardedStorageCatalogAndResolution(t *testing.T) {
	c := core.ClientOf(testutil.NewConfig(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var file string
		switch r.URL.Path {
		case "/internal/v1/regions":
			file = "discovery_regions.json"
		case "/internal/v1/billing/project_types":
			file = "discovery_project_types.json"
		case "/internal/v1/billing/purchase_types":
			file = "discovery_purchase_types.json"
		case "/billing-api/v1/configurations":
			file = map[string]string{"vos_billing_normal_min_quota": "discovery_config_min_quota.json", "vos_billing_normal_max_quota": "discovery_config_max_quota.json", "max_project_per_user_per_region": "discovery_config_max_projects.json", "enable_iam_checkout": "discovery_config_iam_checkout.json"}[r.URL.Query().Get("keys")]
		default:
			t.Errorf("unexpected route %s", r.URL)
			w.WriteHeader(500)
			return
		}
		if r.URL.Path != "/internal/v1/regions" && (r.Header.Get("region") != "region-2" || r.Header.Get("region_id") != "region-2") {
			t.Error("missing region scope")
		}
		testutil.WriteFixture(t, w, "../../testdata/storage/"+file)
	})))
	s := catalogSession(t, 7)
	regions, err := StorageRegions(c, s)
	if err != nil {
		t.Fatal(err)
	}
	id, name, err := ResolveStorageRegion("storage.ListStoragePrices", c.Region(), "", regions)
	if err != nil || name != "HCM04" {
		t.Fatalf("region %s %s error %v", id, name, err)
	}
	catalog, err := ReadStorage(c, s, "storage.ListStoragePrices", id)
	if err != nil {
		t.Fatal(err)
	}
	spec, err := catalog.Resolve("estimate", "Gold", 30)
	if err != nil || spec.ProjectTypeID != 1 || spec.PurchaseTypeID != 4 || s.Requests() != 7 {
		t.Fatalf("spec %+v requests %d error %v", spec, s.Requests(), err)
	}
	if _, err := catalog.Resolve("estimate", "gold", 30); !errors.Is(err, core.ErrInvalidInput) {
		t.Fatalf("unknown type error %v", err)
	}
	if _, err := catalog.Resolve("estimate", "Gold", 29); !errors.Is(err, core.ErrInvalidInput) {
		t.Fatalf("quota error %v", err)
	}
}

func TestStorageCatalogAmbiguity(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"missing join", `{"id":1,"name":"Gold","status":1,"allowPeriod":[1],"billableResources":[]}`},
		{"conflicting join", `{"id":1,"name":"Gold","status":1,"allowPeriod":[1],"billableResources":[{"purchaseTypeId":4,"projectTypeId":2}]}`},
		{"duplicate join", `{"id":1,"name":"Gold","status":1,"allowPeriod":[1],"billableResources":[{"purchaseTypeId":4,"projectTypeId":1},{"purchaseTypeId":4,"projectTypeId":1}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := core.ClientOf(testutil.NewConfig(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "project_types") {
					_, _ = w.Write([]byte(`{"success":true,"datas":[` + tc.body + `]}`))
					return
				}
				if strings.HasSuffix(r.URL.Path, "purchase_types") {
					testutil.WriteFixture(t, w, "../../testdata/storage/discovery_purchase_types.json")
					return
				}
				key := r.URL.Query().Get("keys")
				value := "30"
				if key == "enable_iam_checkout" {
					value = "true"
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "datas": []map[string]string{{"key": key, "value": value}}})
			})))
			_, err := ReadStorage(c, catalogSession(t, 6), "estimate", "region-2")
			var api *core.APIError
			if !errors.As(err, &api) || api.Code != "InvalidResponse" || errors.Is(err, core.ErrInvalidInput) {
				t.Fatalf("error %v", err)
			}
		})
	}
}

func TestCatalogTypedFieldsCompatibility(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		encryption bool
	}{
		{"parent zone", `{"volumeTypeZones":[{"id":"p-1","zone":"invalid"}]}`, false},
		{"parent description", `{"volumeTypeZones":[{"id":"p-1","description":1}]}`, false},
		{"encryption display", `[{"key":"aes_256","displayKey":{}}]`, true},
		{"encryption alias", `[{"key":"aes_256","id":1}]`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := core.ClientOf(testutil.NewConfig(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(tc.body)) })))
			s := catalogSession(t, 1)
			var err error
			if tc.encryption {
				_, err = Encryptions(c, s, "project-1")
			} else {
				_, err = VolumeParents(c, s, "project-1")
			}
			var api *core.APIError
			if !errors.As(err, &api) || api.Code != "InvalidResponse" {
				t.Fatalf("error %v", err)
			}
		})
	}
}

func TestStorageCatalogRefusalWithholdsServerText(t *testing.T) {
	c := core.ClientOf(testutil.NewConfig(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"success":false,"code":"reflected-code","errorMsg":"reflected-message"}`))
	})))
	_, err := ReadStorage(c, catalogSession(t, 1), "estimate", "region-2")
	var api *core.APIError
	if !errors.As(err, &api) || api.StatusCode != 200 || strings.Contains(err.Error(), "reflected") || strings.Contains(api.Code, "reflected") {
		t.Fatalf("unsafe refusal error %v", err)
	}
}

func TestStorageRegionValidation(t *testing.T) {
	for _, tc := range []struct {
		configured, name string
		valid            bool
	}{
		{"hcm-3", "", true}, {"han-1", "hcm04", true}, {"hcm-3", "HAN02", true},
		{"unknown", "", false}, {"unknown", "HCM04", false}, {"hcm-3", "foreign", false},
	} {
		err := ValidateStorageRegion("quote", tc.configured, tc.name)
		if (err == nil) != tc.valid || (err != nil && !errors.Is(err, core.ErrInvalidInput)) {
			t.Fatalf("region %s %s error %v", tc.configured, tc.name, err)
		}
	}
}

func TestImageDecoderRejectsAmbiguousFields(t *testing.T) {
	for _, body := range []string{
		`{"id":"i-1","licence":true,"licence":false}`,
		`{"id":"i-1","Licence":false}`,
		`{"id":"i-1","packageLimit":{"DiskSize":20}}`,
		`{"id":"i-1","packageLimit":{"diskSize":40,"diskSize":20}}`,
	} {
		t.Run(body, func(t *testing.T) {
			var image Image
			if err := json.Unmarshal([]byte(body), &image); err == nil {
				t.Fatal("accepted ambiguous image fields")
			}
		})
	}
}
