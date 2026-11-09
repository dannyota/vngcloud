package compute

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/testutil"
)

func TestListFlavorPricesExcludesGPUCatalogs(t *testing.T) {
	// The synthetic GPU fixture uses public names and CPU/memory/GPU counts
	// from the manager's sanitized structural diagnostic. All other values are
	// invented; the fixture establishes no GPU reference or pricing contract.
	for _, tc := range []struct {
		name                       string
		in                         *ListFlavorPricesInput
		rows, requests, exclusions int
		gpuFirst                   bool
	}{
		{name: "defaults", rows: 3, requests: 12, exclusions: 8},
		{name: "available-small", in: &ListFlavorPricesInput{AvailableOnly: true, MaxRAM: 8}, rows: 1, requests: 10, exclusions: 8},
		{name: "partial-enabled", in: &ListFlavorPricesInput{AllowPartial: true}, rows: 3, requests: 12, exclusions: 8},
		{name: "gpu-first", rows: 3, requests: 12, exclusions: 8, gpuFirst: true},
		{name: "gpu-only", in: &ListFlavorPricesInput{Family: "gpu", Platform: "code-g"}, requests: 3, exclusions: 1},
		{name: "cpu-platform", in: &ListFlavorPricesInput{Platform: "code-s2"}, rows: 3, requests: 10},
	} {
		t.Run(tc.name, func(t *testing.T) {
			flavors := []Flavor{
				{FlavorID: "cpu-flav-1", FlavorZoneID: "cpu-zone-1", Name: "cpu-small", CPU: 1, Memory: 2, RemainingVMs: 1, Metadata: `{"imageTypeSupport":["Ubuntu"]}`},
				{FlavorID: "cpu-flav-2", FlavorZoneID: "cpu-zone-1", Name: "cpu-large", CPU: 2, Memory: 16, RemainingVMs: 1, Metadata: `{"imageTypeSupport":["Ubuntu"]}`},
				{FlavorID: "cpu-flav-3", FlavorZoneID: "cpu-zone-1", Name: "cpu-sold-out", CPU: 2, Memory: 4, RemainingVMs: 1, IsSoldOut: true, Metadata: `{"imageTypeSupport":["Ubuntu"]}`},
			}
			raw, err := json.Marshal(flavors)
			if err != nil {
				t.Fatal(err)
			}
			overrides := map[string]string{
				"/v1/project-1/zones": `{"data":[{"uuid":"HCM03-1C","name":"HCM-1C","isEnabled":true}]}`,
				"/v1/project-1/flavors/families/general-purpose/platforms/code-s2": string(raw),
				"/v1/project-1/flavors/families/general-purpose/platforms/code-s":  `[]`,
				"/v1/project-1/flavors/families/general-purpose/platforms/code-a":  `[]`,
			}
			if tc.gpuFirst {
				var families []json.RawMessage
				if err := json.Unmarshal([]byte(testutil.FixtureBody(t, "../testdata/compute/discovery_families.json")), &families); err != nil {
					t.Fatal(err)
				}
				families[0], families[1] = families[1], families[0]
				ordered, err := json.Marshal(families)
				if err != nil {
					t.Fatal(err)
				}
				overrides["/v1/project-1/flavor_zones/families"] = string(ordered)
			}
			quotes, gpuReads := 0, 0
			base := flavorDiscoveryHandler(t, overrides, func(w http.ResponseWriter, r *http.Request) {
				quotes++
				var body struct {
					Info struct {
						FlavorID string `json:"flavorId"`
					} `json:"resourceInfo"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if !strings.HasPrefix(body.Info.FlavorID, "cpu-flav-") {
					t.Errorf("unexpected flavor quote %q", body.Info.FlavorID)
				}
				testutil.WriteFixture(t, w, "../testdata/compute/discovery_quote_server_s2.json")
			})
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasPrefix(r.URL.Path, "/v1/project-1/flavors/families/gpu/platforms/") {
					gpuReads++
					testutil.WriteFixture(t, w, "../testdata/compute/discovery_gpu_flavors.json")
					return
				}
				base.ServeHTTP(w, r)
			}))
			out, err := c.ListFlavorPrices(flavorDiscoveryContext(), tc.in)
			if err != nil {
				t.Fatal(err)
			}
			if out == nil || len(out.Items) != tc.rows || !out.Metadata.Complete || len(out.Metadata.Failures) != 0 || out.Metadata.Requests != tc.requests || out.Metadata.ExcludedGroups != nil || quotes != tc.rows || gpuReads != 0 {
				t.Fatalf("output %+v quotes %d GPU reads %d", out, quotes, gpuReads)
			}
			for _, row := range out.Items {
				if row.Family != "general-purpose" || row.Flavor.GPU != 0 || row.Price.Status != "quoted" || row.Reference == nil || row.Price.MonthlyPrice == nil {
					t.Fatalf("row %+v", row)
				}
			}
			exclusions := 0
			for _, note := range out.Metadata.Notes {
				if note.Topic != "excluded-family" {
					continue
				}
				exclusions++
				if !strings.HasPrefix(note.Scope, "HCM03-1C/gpu/code-") || !strings.Contains(note.Text, "excluded") || !strings.Contains(note.Text, "unverified") {
					t.Fatalf("exclusion note %+v", note)
				}
			}
			if exclusions != tc.exclusions {
				t.Fatalf("exclusion notes %d, want %d", exclusions, tc.exclusions)
			}
		})
	}
}

func TestListFlavorPricesGPUExclusionPreservesValidation(t *testing.T) {
	for _, tc := range []struct {
		name, field string
		value       any
		missing     bool
		duplicate   bool
	}{
		{name: "missing-cpu", field: "cpu", missing: true},
		{name: "null-capacity", field: "remainingVms"},
		{name: "wrong-memory-type", field: "memory", value: "2"},
		{name: "negative-memory", field: "memory", value: -1},
		{name: "gpu-in-general-family", field: "gpu", value: 1},
		{name: "bad-flavor-id", field: "flavorId", value: "../bad"},
		{name: "conflicting-zone", field: "zoneId", value: "other-zone"},
		{name: "conflicting-identity", field: "memory", value: 4, duplicate: true},
	} {
		for _, partial := range []bool{false, true} {
			t.Run(tc.name+"/partial="+strconv.FormatBool(partial), func(t *testing.T) {
				row := map[string]any{"flavorId": "f-1", "flavorZoneId": "fz-1", "name": "cpu", "cpu": 1, "memory": 2, "remainingVms": 1, "isSoldOut": false}
				rows := []map[string]any{}
				if tc.duplicate {
					previous := map[string]any{}
					for key, value := range row {
						previous[key] = value
					}
					rows = append(rows, previous)
				}
				row[tc.field] = tc.value
				if tc.missing {
					delete(row, tc.field)
				}
				rows = append(rows, row)
				raw, err := json.Marshal(rows)
				if err != nil {
					t.Fatal(err)
				}
				overrides := map[string]string{
					"/v1/project-1/flavors/families/general-purpose/platforms/code-s2": string(raw),
					"/v1/project-1/flavors/families/general-purpose/platforms/code-s":  `[]`,
					"/v1/project-1/flavors/families/general-purpose/platforms/code-a":  `[]`,
				}
				c := newTestClient(t, flavorDiscoveryHandler(t, overrides, func(http.ResponseWriter, *http.Request) { t.Error("unexpected quote") }))
				out, err := c.ListFlavorPrices(flavorDiscoveryContext(), &ListFlavorPricesInput{ZoneID: "HCM03-1C", Family: "general-purpose", AllowPartial: partial})
				var api *vngcloud.APIError
				if out != nil || !errors.As(err, &api) || api.Code != "InvalidResponse" || api.StatusCode != 200 || api.Operation != "compute.ListFamilyFlavors" {
					t.Fatalf("output %+v error %v", out, err)
				}
			})
		}
	}
}
