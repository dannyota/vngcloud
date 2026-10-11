package volume

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"testing"
	"time"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/discovery"
	"danny.vn/vngcloud/internal/testutil"
	"danny.vn/vngcloud/pricing"
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

func TestListVolumePricesValidationBeforeReads(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("unexpected request") }))
	for _, in := range []ListVolumePricesInput{{Size: -1}, {IOPS: -1}, {ZoneID: ".."}, {VolumeTypeID: "bad/id"}, {Encryption: "bad/key"}, {MaxRequests: -1}, {MaxRequests: 2001}} {
		if out, err := c.ListVolumePrices(discoveryTestContext(), &in); out != nil || !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Fatalf("input %+v: output %v error %v", in, out, err)
		}
	}
}

func TestListVolumePricesFixturesAndBodies(t *testing.T) {
	t.Parallel()
	quotes := 0
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/project-1/zones":
			testutil.WriteFixture(t, w, "../testdata/portal/discovery_zones.json")
		case "/v1/project-1/volume_type_zones":
			if r.URL.RawQuery != "" {
				t.Error("filtered parent read")
			}
			// Synthetic selector variant of the existing parent fixture.
			_, _ = w.Write([]byte(`{"volumeTypeZones":[{"id":"fz-82","zone":{"uuid":"HCM03-1C"}}]}`))
		case "/v1/project-1/fz-82/volume_types":
			testutil.WriteFixture(t, w, "../testdata/volume/discovery_types.json")
		case "/v1/project-1/volumes/encryption_types":
			testutil.WriteFixture(t, w, "../testdata/volume/discovery_encryption_types.json")
		case "/v1/price":
			quotes++
			var body struct {
				ResourceType string         `json:"resourceType"`
				Action       string         `json:"action"`
				Info         map[string]any `json:"resourceInfo"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if body.ResourceType != "volume" || body.Action != "create" || body.Info["period"] != float64(1) || body.Info["isPoc"] != false || body.Info["size"] != float64(1) || body.Info["volumeTypeId"] != "vtype-1" || body.Info["zoneId"] != "HCM03-1C" {
				t.Errorf("body %+v", body)
			}
			if _, encrypted := body.Info["encryptionType"]; encrypted {
				testutil.WriteFixture(t, w, "../testdata/volume/discovery_quote_encrypted.json")
			} else {
				testutil.WriteFixture(t, w, "../testdata/volume/discovery_quote_plain.json")
			}
		default:
			t.Errorf("unexpected route %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	out, err := c.ListVolumePrices(discoveryTestContext(), &ListVolumePricesInput{IOPS: 3000})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Items) != 12 || quotes != 3 || out.Metadata.Requests != 7 || !out.Metadata.Complete {
		t.Fatalf("output %+v quotes %d", out, quotes)
	}
	for _, row := range out.Items {
		if !*row.ZoneEnabled {
			if row.Price.Status != "inapplicable" || row.Price.MonthlyPrice != nil || row.PricePerGB != nil {
				t.Fatalf("disabled row %+v", row)
			}
			continue
		}
		if row.SizeGB != 1 || row.Price.MonthlyPrice == nil || *row.Price.MonthlyPrice != 3200 || row.PricePerGB == nil || *row.PricePerGB != 3200 || row.VolumeType.ID != "vtype-1" {
			t.Fatalf("row %+v", row)
		}
	}
}

func volumeDiscoveryHandler(t *testing.T, overrides map[string]string, quote func(http.ResponseWriter, *http.Request)) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if body, ok := overrides[r.URL.Path]; ok {
			_, _ = w.Write([]byte(body))
			return
		}
		switch r.URL.Path {
		case "/v1/project-1/zones":
			testutil.WriteFixture(t, w, "../testdata/portal/discovery_zones.json")
		case "/v1/project-1/volume_type_zones":
			// Synthetic selector variant joins the committed catalog shapes.
			_, _ = w.Write([]byte(`{"volumeTypeZones":[{"id":"fz-82","zone":{"uuid":"HCM03-1C"}}]}`))
		case "/v1/project-1/fz-82/volume_types":
			testutil.WriteFixture(t, w, "../testdata/volume/discovery_types.json")
		case "/v1/project-1/volumes/encryption_types":
			testutil.WriteFixture(t, w, "../testdata/volume/discovery_encryption_types.json")
		case "/v1/price":
			quote(w, r)
		default:
			t.Errorf("unexpected route %s", r.URL.Path)
			w.WriteHeader(404)
		}
	})
}

func TestListVolumePricesCatalogValidation(t *testing.T) {
	cases := map[string]map[string]string{
		"missing zones":        {"/v1/project-1/zones": `{}`},
		"null zones":           {"/v1/project-1/zones": `{"data":null}`},
		"duplicate zones":      {"/v1/project-1/zones": `{"data":[{"uuid":"z-1","isEnabled":true},{"uuid":"z-1","isEnabled":false}]}`},
		"missing enabled":      {"/v1/project-1/zones": `{"data":[{"uuid":"z-1"}]}`},
		"paging":               {"/v1/project-1/zones": `{"data":[],"nextPage":1}`},
		"duplicate parents":    {"/v1/project-1/volume_type_zones": `{"volumeTypeZones":[{"id":"p-1","zone":{"uuid":"HCM03-1C"}},{"id":"p-1","zone":{"uuid":"HCM03-1C"}}]}`},
		"null types":           {"/v1/project-1/fz-82/volume_types": `{"volumeTypes":null}`},
		"duplicate types":      {"/v1/project-1/fz-82/volume_types": `{"volumeTypes":[{"id":"t-1","name":"a","minSize":1,"maxSize":2},{"id":"t-1","name":"a","minSize":1,"maxSize":2}]}`},
		"bad bounds":           {"/v1/project-1/fz-82/volume_types": `{"volumeTypes":[{"id":"t-1","name":"a","minSize":3,"maxSize":2}]}`},
		"wrong parent":         {"/v1/project-1/fz-82/volume_types": `{"volumeTypes":[{"id":"t-1","name":"a","minSize":1,"maxSize":2,"volumeTypeZoneId":"wrong"}]}`},
		"duplicate encryption": {"/v1/project-1/volumes/encryption_types": `[{"key":"aes"},{"key":"aes"}]`},
	}
	for name, overrides := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			c := newTestClient(t, volumeDiscoveryHandler(t, overrides, func(http.ResponseWriter, *http.Request) { t.Error("unexpected quote") }))
			out, err := c.ListVolumePrices(discoveryTestContext(), nil)
			var api *vngcloud.APIError
			if out != nil || !errors.As(err, &api) || api.Code != "InvalidResponse" || api.StatusCode != 200 {
				t.Fatalf("output %v error %v", out, err)
			}
		})
	}
}

func TestListVolumePricesDisabledBoundsAndExactFilters(t *testing.T) {
	cases := []struct {
		name      string
		in        ListVolumePricesInput
		overrides map[string]string
		count     int
		status    string
	}{
		{name: "disabled", in: ListVolumePricesInput{Encryption: "none", IOPS: 3000}, overrides: map[string]string{"/v1/project-1/zones": `{"data":[{"uuid":"HCM03-1C","isEnabled":false}]}`}, count: 1, status: "inapplicable"},
		{name: "size", in: ListVolumePricesInput{Encryption: "none", IOPS: 3000, Size: 5001}, count: 4, status: "inapplicable"},
		{name: "unknown type", in: ListVolumePricesInput{VolumeTypeID: "unknown"}, count: 0},
		{name: "unknown encryption", in: ListVolumePricesInput{Encryption: "unknown"}, count: 0},
		{name: "empty", overrides: map[string]string{"/v1/project-1/fz-82/volume_types": `{"volumeTypes":[]}`}, count: 0},
		{name: "original parent", in: ListVolumePricesInput{VolumeTypeID: "unknown"}, overrides: map[string]string{"/v1/project-1/volume_type_zones": testutil.FixtureBody(t, "../testdata/volume/list_volume_type_zones.json"), "/v1/project-1/volume-zone-1/volume_types": testutil.FixtureBody(t, "../testdata/volume/discovery_types.json")}, count: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c := newTestClient(t, volumeDiscoveryHandler(t, tc.overrides, func(http.ResponseWriter, *http.Request) { t.Error("unexpected quote") }))
			out, err := c.ListVolumePrices(discoveryTestContext(), &tc.in)
			if err != nil {
				t.Fatal(err)
			}
			if len(out.Items) != tc.count || !out.Metadata.Complete {
				t.Fatalf("output %+v", out)
			}
			for _, row := range out.Items {
				if row.Price.Status != tc.status || row.Price.MonthlyPrice != nil {
					t.Fatalf("row %+v", row)
				}
			}
		})
	}
}

func TestListVolumePricesBudgetAndPartialErrors(t *testing.T) {
	for _, partial := range []bool{false, true} {
		t.Run(strconv.FormatBool(partial), func(t *testing.T) {
			t.Parallel()
			quotes := 0
			c := newTestClient(t, volumeDiscoveryHandler(t, nil, func(http.ResponseWriter, *http.Request) { quotes++ }))
			out, err := c.ListVolumePrices(discoveryTestContext(), &ListVolumePricesInput{IOPS: 3000, MaxRequests: 6, AllowPartial: partial})
			if err == nil || quotes != 0 {
				t.Fatalf("error %v quotes %d", err, quotes)
			}
			if partial {
				if out == nil || !errors.Is(err, pricing.ErrDiscoveryIncomplete) || out.Metadata.Requests != 4 || out.Metadata.Complete {
					t.Fatalf("output %+v error %v", out, err)
				}
			} else if out != nil {
				t.Fatal("fail-fast output")
			}
		})
	}
}

func TestListVolumePricesQuoteStatusIdentity(t *testing.T) {
	for _, status := range []int{400, 401, 403, 404, 500} {
		for _, partial := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d/%t", status, partial), func(t *testing.T) {
				t.Parallel()
				attempts := 0
				c := newTestClient(t, volumeDiscoveryHandler(t, nil, func(w http.ResponseWriter, _ *http.Request) {
					attempts++
					w.WriteHeader(status)
					_, _ = w.Write([]byte(`{"code":"Failure","message":"refused"}`))
				}))
				out, err := c.ListVolumePrices(discoveryTestContext(), &ListVolumePricesInput{ZoneID: "HCM03-1C", IOPS: 3000, Encryption: "none", AllowPartial: partial})
				var api *vngcloud.APIError
				if !errors.As(err, &api) || api.StatusCode != status || attempts != 1 {
					t.Fatalf("error %v attempts %d", err, attempts)
				}
				if partial {
					if out == nil || !errors.Is(err, pricing.ErrDiscoveryIncomplete) || out.Items[0].Price.Status != "failed" {
						t.Fatalf("output %+v error %v", out, err)
					}
				} else if out != nil {
					t.Fatal("unexpected output")
				}
			})
		}
	}
}

func TestListVolumePricesMalformedQuote(t *testing.T) {
	for _, body := range []string{`{`, `{}`, `{"optimumPrice":null}`, `{"optimumPrice":0}`, `{"optimumPrice":-1}`, `{"optimumPrice":1e999}`, `{"optimumPrice":1,"discountPercent":-1}`} {
		t.Run(body, func(t *testing.T) {
			t.Parallel()
			c := newTestClient(t, volumeDiscoveryHandler(t, nil, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) }))
			_, err := c.ListVolumePrices(discoveryTestContext(), &ListVolumePricesInput{Encryption: "none", IOPS: 3000})
			var api *vngcloud.APIError
			if !errors.As(err, &api) || api.Code != "InvalidResponse" {
				t.Fatalf("error %v", err)
			}
		})
	}
}

func TestListVolumePricesDefaultOrderAndPartialOrder(t *testing.T) {
	for _, partial := range []bool{false, true} {
		t.Run(strconv.FormatBool(partial), func(t *testing.T) {
			t.Parallel()
			overrides := map[string]string{"/v1/project-1/fz-82/volume_types": `{"volumeTypes":[{"id":"t-b","name":"b","minSize":1,"maxSize":2},{"id":"t-a","volumeTypeId":"q-a","name":"a","minSize":1,"maxSize":2}]}`}
			c := newTestClient(t, volumeDiscoveryHandler(t, overrides, func(w http.ResponseWriter, r *http.Request) {
				var body struct {
					Info map[string]any `json:"resourceInfo"`
				}
				_ = json.NewDecoder(r.Body).Decode(&body)
				if body.Info["volumeTypeId"] == "t-b" && partial {
					w.WriteHeader(500)
					return
				}
				testutil.WriteFixture(t, w, "../testdata/volume/discovery_quote_plain.json")
			}))
			out, err := c.ListVolumePrices(discoveryTestContext(), &ListVolumePricesInput{ZoneID: "HCM03-1C", Encryption: "none", AllowPartial: partial})
			if partial && !errors.Is(err, pricing.ErrDiscoveryIncomplete) {
				t.Fatal(err)
			}
			if !partial && err != nil {
				t.Fatal(err)
			}
			if len(out.Items) != 2 || out.Items[0].VolumeType.Name != "a" || out.Items[1].VolumeType.Name != "b" {
				t.Fatalf("order %+v", out)
			}
		})
	}
}

func TestListVolumePricesQuoteMemoizationAndFreshCalls(t *testing.T) {
	t.Parallel()
	quotes := 0
	overrides := map[string]string{
		"/v1/project-1/volume_type_zones": `{"volumeTypeZones":[{"id":"p-1","zone":{"uuid":"HCM03-1C"}},{"id":"p-2","zone":{"uuid":"HCM03-1C"}}]}`,
		"/v1/project-1/p-1/volume_types":  `{"volumeTypes":[{"id":"t-1","name":"a","iops":3000,"minSize":1,"maxSize":2}]}`,
		"/v1/project-1/p-2/volume_types":  `{"volumeTypes":[{"id":"t-1","name":"a","iops":3000,"minSize":1,"maxSize":2}]}`,
	}
	c := newTestClient(t, volumeDiscoveryHandler(t, overrides, func(w http.ResponseWriter, _ *http.Request) {
		quotes++
		_, _ = fmt.Fprintf(w, `{"optimumPrice":%d}`, quotes)
	}))
	for i := 1; i <= 2; i++ {
		out, err := c.ListVolumePrices(discoveryTestContext(), &ListVolumePricesInput{ZoneID: "HCM03-1C", Encryption: "none", MaxRequests: 6})
		if err != nil {
			t.Fatal(err)
		}
		if quotes != i || len(out.Items) != 2 || out.Metadata.Requests != 6 || *out.Items[0].Price.MonthlyPrice != float64(i) || *out.Items[1].Price.MonthlyPrice != float64(i) {
			t.Fatalf("output %+v quotes %d", out, quotes)
		}
	}
}

func TestListVolumePricesIndependentCatalogPartial(t *testing.T) {
	t.Parallel()
	overrides := map[string]string{"/v1/project-1/volume_type_zones": `{"volumeTypeZones":[{"id":"bad","zone":{"uuid":"HCM03-1C"}},{"id":"fz-82","zone":{"uuid":"HCM03-1C"}}]}`, "/v1/project-1/bad/volume_types": `{}`}
	c := newTestClient(t, volumeDiscoveryHandler(t, overrides, func(w http.ResponseWriter, _ *http.Request) {
		testutil.WriteFixture(t, w, "../testdata/volume/discovery_quote_plain.json")
	}))
	out, err := c.ListVolumePrices(discoveryTestContext(), &ListVolumePricesInput{ZoneID: "HCM03-1C", AllowPartial: true, Encryption: "none", IOPS: 3000})
	if out == nil || len(out.Items) != 1 || out.Items[0].Price.Status != "quoted" || !errors.Is(err, pricing.ErrDiscoveryIncomplete) || out.Metadata.Failures[0].ItemKey != "bad" {
		t.Fatalf("output %+v error %v", out, err)
	}
}

func TestListVolumePricesRegionWideTypesAndBudget(t *testing.T) {
	for _, tc := range []struct {
		name    string
		zone    string
		budget  int
		partial bool
		refuse  bool
	}{
		{name: "exact bound", budget: 9},
		{name: "selected enabled zone", zone: "HCM03-1C", budget: 7},
		{name: "preflight", budget: 8},
		{name: "partial preflight", budget: 8, partial: true},
		{name: "quote refusal", budget: 9, partial: true, refuse: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			quotes := 0
			overrides := map[string]string{
				"/v1/project-1/zones":             `{"data":[{"uuid":"HCM03-1A","isEnabled":false},{"uuid":"HCM03-1C","isEnabled":true},{"uuid":"HCM03-1D","isEnabled":true}]}`,
				"/v1/project-1/volume_type_zones": `{"volumeTypeZones":[{"id":"p-1","zone":{"uuid":"HCM03-1A"}},{"id":"p-2","zone":{"uuid":"HCM03-1A"}}]}`,
				"/v1/project-1/p-1/volume_types":  `{"volumeTypes":[{"id":"t-1","name":"a","minSize":1,"maxSize":2,"zoneId":"HCM03-1A"}]}`,
				"/v1/project-1/p-2/volume_types":  `{"volumeTypes":[{"id":"t-2","name":"b","minSize":1,"maxSize":2,"zoneId":"p-2"}]}`,
			}
			c := newTestClient(t, volumeDiscoveryHandler(t, overrides, func(w http.ResponseWriter, r *http.Request) {
				quotes++
				var body struct {
					Info map[string]any `json:"resourceInfo"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				zone := body.Info["zoneId"]
				if zone != "HCM03-1C" && zone != "HCM03-1D" {
					t.Errorf("quote zone %v", zone)
				}
				if tc.refuse && zone == "HCM03-1C" && body.Info["volumeTypeId"] == "t-1" {
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				testutil.WriteFixture(t, w, "../testdata/volume/discovery_quote_plain.json")
			}))
			out, err := c.ListVolumePrices(discoveryTestContext(), &ListVolumePricesInput{ZoneID: tc.zone, Encryption: "none", MaxRequests: tc.budget, AllowPartial: tc.partial})
			bound := 9
			if tc.zone != "" {
				bound = 7
			}
			if tc.budget < bound || tc.refuse {
				if err == nil {
					t.Fatal("missing error")
				}
				if !tc.partial {
					if out != nil || quotes != 0 {
						t.Fatalf("output %+v quotes %d", out, quotes)
					}
					return
				}
				if !errors.Is(err, pricing.ErrDiscoveryIncomplete) {
					t.Fatal(err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			wantQuotes, wantRequests, wantRows := 4, 9, 6
			if tc.zone != "" {
				wantQuotes, wantRequests, wantRows = 2, 7, 2
			}
			if tc.budget < bound {
				wantQuotes, wantRequests = 0, 5
			}
			if out == nil || len(out.Items) != wantRows || quotes != wantQuotes || out.Metadata.Requests != wantRequests {
				t.Fatalf("output %+v quotes %d", out, quotes)
			}
			for _, row := range out.Items {
				want := "quoted"
				switch {
				case row.ZoneID == "HCM03-1A":
					want = "inapplicable"
				case tc.budget < bound:
					want = "unknown"
				case tc.refuse && row.ZoneID == "HCM03-1C" && row.VolumeType.ID == "t-1":
					want = "failed"
				}
				if row.Price.Status != want {
					t.Fatalf("row %+v want %s", row, want)
				}
				if row.VolumeType.ID == "t-1" && row.VolumeType.ZoneID != "HCM03-1A" {
					t.Fatalf("catalog fields changed: %+v", row)
				}
			}
		})
	}
}

func TestListVolumePricesFailedQuoteGroup(t *testing.T) {
	for _, budget := range []int{6, 7} {
		for _, partial := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d/%t", budget, partial), func(t *testing.T) {
				t.Parallel()
				quotes := 0
				overrides := map[string]string{
					"/v1/project-1/volume_type_zones": `{"volumeTypeZones":[{"id":"p-1"},{"id":"p-2"}]}`,
					"/v1/project-1/p-1/volume_types":  `{"volumeTypes":[{"id":"t-1","name":"a","minSize":1,"maxSize":2}]}`,
					"/v1/project-1/p-2/volume_types":  `{"volumeTypes":[{"id":"t-1","name":"a","minSize":1,"maxSize":2}]}`,
				}
				c := newTestClient(t, volumeDiscoveryHandler(t, overrides, func(w http.ResponseWriter, _ *http.Request) {
					quotes++
					if quotes == 1 {
						w.WriteHeader(500)
						return
					}
					testutil.WriteFixture(t, w, "../testdata/volume/discovery_quote_plain.json")
				}))
				out, err := c.ListVolumePrices(discoveryTestContext(), &ListVolumePricesInput{ZoneID: "HCM03-1C", Encryption: "none", MaxRequests: budget, AllowPartial: partial})
				var api *vngcloud.APIError
				if !errors.As(err, &api) || api.StatusCode != 500 || quotes != 1 {
					t.Fatalf("error %v quotes %d", err, quotes)
				}
				if !partial {
					if out != nil {
						t.Fatal("fail-fast output")
					}
					return
				}
				if out == nil || len(out.Items) != 2 || out.Metadata.Requests != 6 || len(out.Metadata.Failures) != 2 || !errors.Is(err, pricing.ErrDiscoveryIncomplete) {
					t.Fatalf("output %+v error %v", out, err)
				}
				for _, row := range out.Items {
					if row.Price.Status != "failed" || row.Price.MonthlyPrice != nil {
						t.Fatalf("row %+v", row)
					}
				}
				for _, failure := range out.Metadata.Failures {
					if failure.Status != 500 {
						t.Fatalf("failure %+v", failure)
					}
				}
			})
		}
	}
}

func TestListVolumePricesRejectsContinuation(t *testing.T) {
	for _, marker := range []string{"true", "false", "null"} {
		t.Run(marker, func(t *testing.T) {
			t.Parallel()
			overrides := map[string]string{"/v1/project-1/fz-82/volume_types": `{"volumeTypes":[{"id":"t-1","name":"a","minSize":1,"maxSize":2}],"isNext":` + marker + `}`}
			c := newTestClient(t, volumeDiscoveryHandler(t, overrides, func(http.ResponseWriter, *http.Request) { t.Error("unexpected quote") }))
			out, err := c.ListVolumePrices(discoveryTestContext(), nil)
			var api *vngcloud.APIError
			if out != nil || !errors.As(err, &api) || api.Code != "InvalidResponse" || api.StatusCode != 200 {
				t.Fatalf("output %+v error %v", out, err)
			}
		})
	}
}

func TestVolumeDiscoveryTaxMetadata(t *testing.T) {
	for _, tax := range []string{"", `,"tax":"inclusive"`} {
		price, err := decodeVolumeDiscoveryPrice("pricing.GetQuote", 200, json.RawMessage(`{"optimumPrice":3200,"originalPrice":4000,"discountPrice":800`+tax+`}`))
		if err != nil || price.Tax != "as-returned" || *price.MonthlyPrice != 3200 || *price.OriginalPrice != 4000 || *price.DiscountPrice != 800 {
			t.Fatalf("price %+v error %v", price, err)
		}
	}
}
