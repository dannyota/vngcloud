package compute

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/discovery"
	"danny.vn/vngcloud/internal/testutil"
	"danny.vn/vngcloud/pricing"
)

func flavorDiscoveryContext() context.Context {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	return discovery.WithTestClock(context.Background(), func() time.Time { return now }, func(ctx context.Context, d time.Duration) error { now = now.Add(d); return ctx.Err() })
}

func flavorDiscoveryHandler(t *testing.T, overrides map[string]string, quote func(http.ResponseWriter, *http.Request)) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if raw, ok := overrides[r.URL.Path]; ok {
			_, _ = w.Write([]byte(raw))
			return
		}
		var file string
		switch r.URL.Path {
		case "/v1/project-1/zones":
			file = "portal/discovery_zones.json"
		case "/v1/project-1/flavor_zones/codes":
			file = "compute/discovery_platform_codes.json"
		case "/v1/project-1/flavor_zones/families":
			file = "compute/discovery_families.json"
		case "/v1/project-1/flavors/families/general-purpose/platforms/code-s2":
			file = "compute/discovery_family_flavors.json"
		case "/v1/project-1/images/os":
			file = "compute/discovery_os_images.json"
		case "/v1/project-1/volume_type_zones":
			file = "volume/list_volume_type_zones.json"
		case "/v1/project-1/volume-zone-1/volume_types":
			file = "volume/discovery_types.json"
		case "/v1/price":
			quote(w, r)
			return
		default:
			t.Errorf("unexpected route %s", r.URL)
			w.WriteHeader(500)
			return
		}
		testutil.WriteFixture(t, w, "../testdata/"+file)
	})
}

func TestListFlavorPricesReferenceFixture(t *testing.T) {
	quotes := 0
	c := newTestClient(t, flavorDiscoveryHandler(t, nil, func(w http.ResponseWriter, r *http.Request) {
		quotes++
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		info := body["resourceInfo"].(map[string]any)
		if len(body) != 3 || len(info) != 8 || info["period"] != float64(1) || info["isPoc"] != false || info["encryptionVolume"] != false || info["rootDiskSize"] != float64(20) || info["rootDiskTypeId"] != "vtype-1" || info["flavorId"] != "flav-1" || info["zoneId"] != "HCM03-1C" || body["action"] != "create" || body["resourceType"] != "server" {
			t.Fatalf("body %+v", body)
		}
		testutil.WriteFixture(t, w, "../testdata/compute/discovery_quote_server_s2.json")
	}))
	out, err := c.ListFlavorPrices(flavorDiscoveryContext(), &ListFlavorPricesInput{ZoneID: "HCM03-1C", Family: "general-purpose", Platform: "code-s2", Name: "s2-general-1x2"})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Items) != 1 || quotes != 1 || out.Metadata.Requests != 8 || !out.Metadata.Complete {
		t.Fatalf("output %+v quotes %d", out, quotes)
	}
	row := out.Items[0]
	if row.Reference == nil || row.Reference.ImageType != "Ubuntu" || row.RAMUnit != "unknown" || row.RAMGiB != nil || row.PricePerGiB != nil || *row.Price.MonthlyPrice != 283800 || *row.PricePerRAMUnit != 141900 || row.Price.OriginalPrice != nil || row.Price.DiscountPrice != nil {
		t.Fatalf("row %+v", row)
	}
}

func TestListFlavorPricesInvalidBeforeRequests(t *testing.T) {
	for _, in := range []*ListFlavorPricesInput{{MinRAM: -1}, {MinRAM: 3, MaxRAM: 2}, {MaxRequests: 2001}, {ZoneID: "../bad"}, {Family: "../bad"}, {Platform: "bad/value"}} {
		c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("unexpected request") }))
		out, err := c.ListFlavorPrices(flavorDiscoveryContext(), in)
		if out != nil || !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Fatalf("output %+v error %v", out, err)
		}
	}
}

func TestListFlavorPricesPreflightAndRefusal(t *testing.T) {
	for _, budget := range []int{7, 8} {
		for _, partial := range []bool{false, true} {
			quotes := 0
			c := newTestClient(t, flavorDiscoveryHandler(t, nil, func(w http.ResponseWriter, _ *http.Request) {
				quotes++
				w.WriteHeader(500)
				_, _ = w.Write([]byte(`{"code":"ServerError"}`))
			}))
			out, err := c.ListFlavorPrices(flavorDiscoveryContext(), &ListFlavorPricesInput{ZoneID: "HCM03-1C", Family: "general-purpose", Platform: "code-s2", Name: "s2-general-1x2", MaxRequests: budget, AllowPartial: partial})
			if err == nil || quotes != budget-7 {
				t.Fatalf("error %v quotes %d", err, quotes)
			}
			if partial {
				if out == nil || !errors.Is(err, pricing.ErrDiscoveryIncomplete) || len(out.Items) != 1 {
					t.Fatalf("output %+v error %v", out, err)
				}
			} else if out != nil {
				t.Fatal("fail-fast output")
			}
			if budget == 8 {
				var api *vngcloud.APIError
				if !errors.As(err, &api) || api.StatusCode != 500 {
					t.Fatal(err)
				}
			}
		}
	}
}

func TestListFlavorPricesCatalogTuplesAndAvailability(t *testing.T) {
	for _, available := range []bool{false, true} {
		items := []Flavor{{FlavorID: "f-1", FlavorZoneID: "fz-1", Name: "x", CPU: 1, Memory: 2, RemainingVMs: 1}, {FlavorID: "f-2", FlavorZoneID: "fz-1", Name: "y", CPU: 2, Memory: 4, IsSoldOut: true, RemainingVMs: 1}}
		items = append(items, items[0])
		raw, _ := json.Marshal(items)
		c := newTestClient(t, flavorDiscoveryHandler(t, map[string]string{"/v1/project-1/flavors/families/general-purpose/platforms/code-s2": string(raw)}, func(http.ResponseWriter, *http.Request) { t.Error("unexpected quote") }))
		out, err := c.ListFlavorPrices(flavorDiscoveryContext(), &ListFlavorPricesInput{ZoneID: "HCM03-1C", Family: "general-purpose", Platform: "code-s2", AvailableOnly: available})
		want := 2
		if available {
			want = 1
		}
		if err != nil || len(out.Items) != want {
			t.Fatalf("output %+v error %v", out, err)
		}
	}
}

func TestListFlavorPricesCatalogErrors(t *testing.T) {
	for _, raw := range []string{`null`, `{}`, `[{"flavorId":"f-1","flavorZoneId":"fz-1","name":"x","zoneId":"different"}]`, `[{"flavorId":"f-1","flavorZoneId":"fz-1","name":"x"},{"flavorId":"f-1","flavorZoneId":"fz-1","name":"different"}]`, `[{"flavorId":"f-1","name":"x"}]`} {
		c := newTestClient(t, flavorDiscoveryHandler(t, map[string]string{"/v1/project-1/flavors/families/general-purpose/platforms/code-s2": raw}, func(http.ResponseWriter, *http.Request) { t.Error("unexpected quote") }))
		out, err := c.ListFlavorPrices(flavorDiscoveryContext(), &ListFlavorPricesInput{ZoneID: "HCM03-1C", Family: "general-purpose", Platform: "code-s2", AllowPartial: true})
		var api *vngcloud.APIError
		if out != nil || !errors.As(err, &api) || api.Code != "InvalidResponse" {
			t.Fatalf("output %+v error %v", out, err)
		}
	}
}

func TestListFlavorPricesInvocationFreshness(t *testing.T) {
	quotes := 0
	c := newTestClient(t, flavorDiscoveryHandler(t, nil, func(w http.ResponseWriter, _ *http.Request) {
		quotes++
		testutil.WriteFixture(t, w, "../testdata/compute/discovery_quote_server_s2.json")
	}))
	for range 2 {
		out, err := c.ListFlavorPrices(flavorDiscoveryContext(), &ListFlavorPricesInput{ZoneID: "HCM03-1C", Family: "general-purpose", Platform: "code-s2", Name: "s2-general-1x2"})
		if err != nil || out.Metadata.Requests != 8 {
			t.Fatalf("output %+v error %v", out, err)
		}
	}
	if quotes != 2 {
		t.Fatalf("quotes %d", quotes)
	}
}

func TestListFlavorPricesDisabledAndExactFilters(t *testing.T) {
	for _, name := range []string{"s2-general-1x2", "S2-general-1x2"} {
		c := newTestClient(t, flavorDiscoveryHandler(t, nil, func(http.ResponseWriter, *http.Request) { t.Error("unexpected quote") }))
		out, err := c.ListFlavorPrices(flavorDiscoveryContext(), &ListFlavorPricesInput{ZoneID: "HCM03-1A", Family: "general-purpose", Platform: "code-s2", Name: name})
		want := 1
		if name != "s2-general-1x2" {
			want = 0
		}
		if err != nil || len(out.Items) != want || out.Metadata.Requests != 4 {
			t.Fatalf("output %+v error %v", out, err)
		}
		if want == 1 && out.Items[0].Price.Status != "inapplicable" {
			t.Fatal(out.Items[0])
		}
	}
}

func TestListFlavorPricesNonPublicFixtureGates(t *testing.T) {
	for _, populated := range []bool{false, true} {
		overrides := map[string]string{"/v1/project-1/flavor_zones/product": `{"flavorZones":[]}`, "/v1/project-1/flavor_zones/customs": `[]`, "/v1/project-1/flavors/customs": `[]`}
		if populated {
			overrides["/v1/project-1/flavors/customs"] = `[{"id":"unproven"}]`
		}
		c := newTestClient(t, flavorDiscoveryHandler(t, overrides, func(w http.ResponseWriter, _ *http.Request) {
			testutil.WriteFixture(t, w, "../testdata/compute/discovery_quote_server_s2.json")
		}))
		out, err := c.ListFlavorPrices(flavorDiscoveryContext(), &ListFlavorPricesInput{ZoneID: "HCM03-1C", Family: "general-purpose", Platform: "code-s2", Name: "s2-general-1x2", IncludeNonPublic: true, AllowPartial: true})
		if (err != nil) != populated || out == nil || out.Metadata.ExcludedGroups == nil || *out.Metadata.ExcludedGroups != 0 || out.Metadata.Requests != 11 {
			t.Fatalf("output %+v error %v", out, err)
		}
	}
}

func TestListFlavorPricesDistinctFlavorZoneTuples(t *testing.T) {
	items := []Flavor{{FlavorID: "f-1", FlavorZoneID: "fz-1", Name: "x", CPU: 1, Memory: 2}, {FlavorID: "f-1", FlavorZoneID: "fz-2", Name: "x", CPU: 1, Memory: 2}}
	raw, _ := json.Marshal(items)
	c := newTestClient(t, flavorDiscoveryHandler(t, map[string]string{"/v1/project-1/flavors/families/general-purpose/platforms/code-s2": string(raw)}, func(http.ResponseWriter, *http.Request) { t.Error("unexpected quote") }))
	out, err := c.ListFlavorPrices(flavorDiscoveryContext(), &ListFlavorPricesInput{ZoneID: "HCM03-1C", Family: "general-purpose", Platform: "code-s2"})
	if err != nil || len(out.Items) != 2 {
		t.Fatalf("output %+v error %v", out, err)
	}
}

func TestListFlavorPricesNonPublicRawFixtures(t *testing.T) {
	for _, fixture := range []string{"discovery_product_zones.json", "discovery_custom_zones_empty.json", "discovery_custom_flavors_empty.json", "discovery_legacy_flavors.json"} {
		t.Run(fixture, func(t *testing.T) {
			base := flavorDiscoveryHandler(t, map[string]string{
				"/v1/project-1/flavor_zones/product": `{"flavorZones":[]}`,
				"/v1/project-1/flavor_zones/customs": `[]`,
				"/v1/project-1/flavors/customs":      `[]`,
			}, func(w http.ResponseWriter, _ *http.Request) {
				testutil.WriteFixture(t, w, "../testdata/compute/discovery_quote_server_s2.json")
			})
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.URL.Path == "/v1/project-1/flavor_zones/product" && fixture == "discovery_product_zones.json":
					testutil.WriteFixture(t, w, "../testdata/compute/"+fixture)
				case r.URL.Path == "/v1/project-1/flavor_zones/customs" && fixture == "discovery_custom_zones_empty.json":
					testutil.WriteFixture(t, w, "../testdata/compute/"+fixture)
				case r.URL.Path == "/v1/project-1/flavors/customs" && fixture == "discovery_custom_flavors_empty.json":
					testutil.WriteFixture(t, w, "../testdata/compute/"+fixture)
				case r.URL.Path == "/v1/project-1/flavor_zones/product" && fixture == "discovery_legacy_flavors.json":
					_, _ = w.Write([]byte(`{"flavorZones":[{"id":"fz-5","zoneId":"HCM03-1C"}]}`))
				case r.URL.Path == "/v1/project-1/fz-5/flavors" && fixture == "discovery_legacy_flavors.json":
					testutil.WriteFixture(t, w, "../testdata/compute/"+fixture)
				case len(r.URL.Path) > 8 && r.URL.Path[len(r.URL.Path)-8:] == "/flavors":
					_, _ = w.Write([]byte(`{"flavors":[]}`))
				default:
					base.ServeHTTP(w, r)
				}
			}))
			out, err := c.ListFlavorPrices(flavorDiscoveryContext(), &ListFlavorPricesInput{ZoneID: "HCM03-1C", Family: "general-purpose", Platform: "code-s2", Name: "s2-general-1x2", IncludeNonPublic: true, AllowPartial: true})
			if fixture == "discovery_legacy_flavors.json" {
				var api *vngcloud.APIError
				if !errors.As(err, &api) || api.Code != "InvalidResponse" {
					t.Fatalf("error %v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if out == nil || out.Metadata.ExcludedGroups == nil {
				t.Fatalf("output %+v", out)
			}
			if fixture == "discovery_product_zones.json" && *out.Metadata.ExcludedGroups != 69 {
				t.Fatalf("count %d", *out.Metadata.ExcludedGroups)
			}
		})
	}
}

func TestListFlavorPricesIdenticalQuoteMemoization(t *testing.T) {
	flavor := Flavor{FlavorID: "f-1", FlavorZoneID: "fz-1", Name: "x", CPU: 1, Memory: 2, Metadata: `{"imageTypeSupport":["Ubuntu"]}`}
	other := flavor
	other.FlavorZoneID = "fz-2"
	raw, _ := json.Marshal([]Flavor{flavor, other, flavor})
	quotes := 0
	c := newTestClient(t, flavorDiscoveryHandler(t, map[string]string{"/v1/project-1/flavors/families/general-purpose/platforms/code-s2": string(raw)}, func(w http.ResponseWriter, _ *http.Request) {
		quotes++
		testutil.WriteFixture(t, w, "../testdata/compute/discovery_quote_server_s2.json")
	}))
	out, err := c.ListFlavorPrices(flavorDiscoveryContext(), &ListFlavorPricesInput{ZoneID: "HCM03-1C", Family: "general-purpose", Platform: "code-s2", MaxRequests: 8})
	if err != nil || out.Metadata.Requests != 8 || len(out.Items) != 2 || quotes != 1 {
		t.Fatalf("output %+v error %v quotes %d", out, err, quotes)
	}
	for _, row := range out.Items {
		if row.Price.Status != "quoted" {
			t.Fatal(row)
		}
	}
}

func TestListFlavorPricesRequestStartPacingAndCompletion(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.FixedZone("test", 7*3600))
	starts := []time.Time{}
	base := flavorDiscoveryHandler(t, nil, func(w http.ResponseWriter, _ *http.Request) {
		testutil.WriteFixture(t, w, "../testdata/compute/discovery_quote_server_s2.json")
	})
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { starts = append(starts, now); base.ServeHTTP(w, r) }))
	ctx := discovery.WithTestClock(context.Background(), func() time.Time { return now }, func(ctx context.Context, d time.Duration) error { now = now.Add(d); return ctx.Err() })
	out, err := c.ListFlavorPrices(ctx, &ListFlavorPricesInput{ZoneID: "HCM03-1C", Family: "general-purpose", Platform: "code-s2", Name: "s2-general-1x2"})
	if err != nil || len(starts) != 8 || !out.Metadata.QuotedAt.Equal(now) || out.Metadata.QuotedAt.Location() != time.UTC {
		t.Fatalf("output %+v error %v starts %d", out, err, len(starts))
	}
	for i := 1; i < len(starts); i++ {
		if starts[i].Sub(starts[i-1]) < 500*time.Millisecond {
			t.Fatalf("starts %v", starts)
		}
	}
}

func TestListFlavorPricesMissingCatalogSpecifications(t *testing.T) {
	for _, raw := range []string{
		`[{"flavorId":"f-1","flavorZoneId":"fz-1","name":"x","memory":2,"remainingVms":1,"isSoldOut":false}]`,
		`[{"flavorId":"f-1","flavorZoneId":"fz-1","name":"x","cpu":null,"memory":2,"remainingVms":1,"isSoldOut":false}]`,
	} {
		c := newTestClient(t, flavorDiscoveryHandler(t, map[string]string{"/v1/project-1/flavors/families/general-purpose/platforms/code-s2": raw}, func(http.ResponseWriter, *http.Request) { t.Error("unexpected quote") }))
		out, err := c.ListFlavorPrices(flavorDiscoveryContext(), &ListFlavorPricesInput{ZoneID: "HCM03-1C", Family: "general-purpose", Platform: "code-s2"})
		if out != nil || err == nil {
			t.Fatalf("output %+v error %v", out, err)
		}
	}
}

func TestListFlavorPricesSkipsUnusedReferenceReads(t *testing.T) {
	for _, tc := range []struct {
		name           string
		in             ListFlavorPricesInput
		rows, requests int
	}{
		{name: "unmatched-family", in: ListFlavorPricesInput{ZoneID: "HCM03-1C", Family: "unknown-family"}, requests: 3},
		{name: "unmatched-platform", in: ListFlavorPricesInput{ZoneID: "HCM03-1C", Family: "general-purpose", Platform: "unknown-platform"}, requests: 3},
		{name: "unmatched-name", in: ListFlavorPricesInput{ZoneID: "HCM03-1C", Family: "general-purpose", Platform: "code-s2", Name: "unknown-name"}, requests: 4},
		{name: "unmatched-ram", in: ListFlavorPricesInput{ZoneID: "HCM03-1C", Family: "general-purpose", Platform: "code-s2", MinRAM: 1000000}, requests: 4},
		{name: "disabled-only", in: ListFlavorPricesInput{ZoneID: "HCM03-1A", Family: "general-purpose", Platform: "code-s2", Name: "s2-general-1x2"}, rows: 1, requests: 4},
	} {
		for _, tightBudget := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/%t", tc.name, tightBudget), func(t *testing.T) {
				referenceReads := 0
				base := flavorDiscoveryHandler(t, nil, func(http.ResponseWriter, *http.Request) { t.Error("unexpected quote") })
				c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == "/v1/project-1/volume_type_zones" || r.URL.Path == "/v1/project-1/images/os" {
						referenceReads++
						w.WriteHeader(500)
						return
					}
					base.ServeHTTP(w, r)
				}))
				in := tc.in
				if tightBudget {
					in.MaxRequests = tc.requests
				}
				out, err := c.ListFlavorPrices(flavorDiscoveryContext(), &in)
				if err != nil || out == nil || !out.Metadata.Complete || len(out.Items) != tc.rows || out.Metadata.Requests != tc.requests || referenceReads != 0 {
					t.Fatalf("output %+v error %v reference reads %d", out, err, referenceReads)
				}
				if tc.rows > 0 && (out.Items[0].Price.Status != "inapplicable" || out.Items[0].Reference != nil) {
					t.Fatalf("row %+v", out.Items[0])
				}
			})
		}
	}
}
