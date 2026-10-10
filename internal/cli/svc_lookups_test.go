package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"danny.vn/vngcloud/compute"
	"danny.vn/vngcloud/volume"
)

// requestPaths returns the request paths the fixture saw, in arrival order.
func (f *svcFixture) requestPaths() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var paths []string
	for _, r := range f.requests {
		paths = append(paths, r.path)
	}
	return paths
}

const lookupFlavorZonesBody = `{"flavorZones":[` +
	`{"id":"fz-1","name":"General","description":"","zoneId":"zone-1"},` +
	`{"id":"fz-2","name":"Empty","description":"","zoneId":"zone-1"},` +
	`{"id":"fz-other","name":"Other","description":"","zoneId":"zone-2"}]}`

func lookupFlavorRoutes() map[string]func(http.ResponseWriter, *http.Request) {
	return map[string]func(http.ResponseWriter, *http.Request){
		"/v1/proj-1/flavor_zones/product": jsonHandler(http.StatusOK, lookupFlavorZonesBody),
		"/v1/proj-1/fz-1/flavors": jsonHandler(http.StatusOK, `{"flavors":[`+
			`{"flavorId":"f-1","name":"s2-general-1x2"},`+
			`{"flavorId":"f-2","name":"s2-general-2x4","isSoldOut":true}]}`),
		"/v1/proj-1/fz-2/flavors": jsonHandler(http.StatusOK, `{"flavors":[]}`),
	}
}

func runLookup(t *testing.T, fixture *svcFixture, args ...string) (stdout string, err error) {
	t.Helper()
	root, out, _ := newSvcRoot(t, fixture)
	root.SetArgs(append([]string{"--region", "hcm-3", "--project-id", "proj-1"}, args...))
	err = root.ExecuteContext(context.Background())
	return out.String(), err
}

func TestComputeListFlavorsByZoneIDFansOutAndFillsFlavorZoneID(t *testing.T) {
	fixture := newSvcFixture(lookupFlavorRoutes())
	stdout, err := runLookup(t, fixture, "compute", "list-flavors", "--zone-id", "zone-1")
	if err != nil {
		t.Fatalf("list-flavors: %v", err)
	}
	wantPaths := []string{
		"/v1/proj-1/flavor_zones/product", "/v1/proj-1/fz-1/flavors", "/v1/proj-1/fz-2/flavors",
	}
	if got := fixture.requestPaths(); !slices.Equal(got, wantPaths) {
		t.Fatalf("requests = %v, want %v", got, wantPaths)
	}
	var out struct{ Items []compute.Flavor }
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatalf("stdout is not valid JSON: %v (%s)", err, stdout)
	}
	if len(out.Items) != 2 || out.Items[0].FlavorID != "f-1" || out.Items[1].FlavorID != "f-2" {
		t.Fatalf("Items = %+v, want f-1 then f-2", out.Items)
	}
	for _, f := range out.Items {
		if f.FlavorZoneID != "fz-1" || f.ZoneID != "zone-1" {
			t.Errorf("row %s FlavorZoneID/ZoneID = %q/%q, want fz-1/zone-1", f.FlavorID, f.FlavorZoneID, f.ZoneID)
		}
	}
}

func TestComputeListFlavorsNameIsAnExactMatch(t *testing.T) {
	fixture := newSvcFixture(lookupFlavorRoutes())
	stdout, err := runLookup(t, fixture, "compute", "list-flavors", "--zone-id", "zone-1", "--name", "s2-general-1x2")
	if err != nil {
		t.Fatalf("list-flavors: %v", err)
	}
	var out struct{ Items []compute.Flavor }
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatalf("stdout is not valid JSON: %v (%s)", err, stdout)
	}
	if len(out.Items) != 1 || out.Items[0].FlavorID != "f-1" {
		t.Fatalf("Items = %+v, want only f-1", out.Items)
	}
	stdout, err = runLookup(t, newSvcFixture(lookupFlavorRoutes()), "compute", "list-flavors",
		"--flavor-zone-id", "fz-1", "--name", "s2-general")
	if err != nil {
		t.Fatalf("list-flavors: %v", err)
	}
	if !strings.Contains(stdout, `"Items": []`) && !strings.Contains(stdout, `"Items":[]`) {
		t.Fatalf("a partial --name matched: %s", stdout)
	}
}

func TestComputeListFlavorsFlavorZoneIDIsOneRequest(t *testing.T) {
	fixture := newSvcFixture(lookupFlavorRoutes())
	if _, err := runLookup(t, fixture, "compute", "list-flavors", "--flavor-zone-id", "fz-1"); err != nil {
		t.Fatalf("list-flavors: %v", err)
	}
	if got := fixture.requestPaths(); !slices.Equal(got, []string{"/v1/proj-1/fz-1/flavors"}) {
		t.Fatalf("requests = %v, want only fz-1 flavors", got)
	}
}

func TestComputeListFlavorsZoneFlagRuleExitsTwoWithoutRequests(t *testing.T) {
	for name, args := range map[string][]string{
		"none": {"compute", "list-flavors"},
		"both": {"compute", "list-flavors", "--zone-id", "zone-1", "--flavor-zone-id", "fz-1"},
	} {
		t.Run(name, func(t *testing.T) {
			fixture := newSvcFixture(lookupFlavorRoutes())
			_, err := runLookup(t, fixture, args...)
			if err == nil {
				t.Fatalf("expected an error")
			}
			if exitCode(err) != 2 {
				t.Fatalf("exitCode = %d, want 2 (err=%v)", exitCode(err), err)
			}
			if !strings.Contains(err.Error(), "exactly one of FlavorZoneID and ZoneID") {
				t.Fatalf("error does not carry the SDK message: %v", err)
			}
			if n := fixture.requestCount(); n != 0 {
				t.Fatalf("requestCount = %d, want 0", n)
			}
		})
	}
}

const lookupVolumeTypeZonesBody = `{"volumeTypeZones":[` +
	`{"id":"vtz-1","name":"SSD","zone":{"uuid":"zone-1"}},` +
	`{"id":"vtz-other","name":"Other","zone":{"uuid":"zone-2"}}]}`

func lookupVolumeRoutes() map[string]func(http.ResponseWriter, *http.Request) {
	return map[string]func(http.ResponseWriter, *http.Request){
		"/v1/proj-1/volume_type_zones": jsonHandler(http.StatusOK, lookupVolumeTypeZonesBody),
		"/v1/proj-1/vtz-1/volume_types": jsonHandler(http.StatusOK, `{"volumeTypes":[`+
			`{"id":"t-1","name":"SSD","iops":3000},`+
			`{"id":"t-2","name":"NVMe","iops":5000}]}`),
		"/v1/proj-1/volume_types": jsonHandler(http.StatusOK, `{"volumeTypes":[`+
			`{"id":"t-1","name":"SSD","iops":3000},`+
			`{"id":"t-2","name":"NVMe","iops":5000}]}`),
	}
}

func TestVolumeListVolumeTypesByZoneIDAndIOPS(t *testing.T) {
	fixture := newSvcFixture(lookupVolumeRoutes())
	stdout, err := runLookup(t, fixture, "volume", "list-volume-types", "--zone-id", "zone-1", "--iops", "3000")
	if err != nil {
		t.Fatalf("list-volume-types: %v", err)
	}
	wantPaths := []string{"/v1/proj-1/volume_type_zones", "/v1/proj-1/vtz-1/volume_types"}
	if got := fixture.requestPaths(); !slices.Equal(got, wantPaths) {
		t.Fatalf("requests = %v, want %v", got, wantPaths)
	}
	var out struct{ Items []volume.VolumeType }
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatalf("stdout is not valid JSON: %v (%s)", err, stdout)
	}
	if len(out.Items) != 1 || out.Items[0].ID != "t-1" {
		t.Fatalf("Items = %+v, want only t-1", out.Items)
	}
	if out.Items[0].VolumeTypeZoneID != "vtz-1" || out.Items[0].ZoneID != "zone-1" {
		t.Errorf("row zone fields = %q/%q, want vtz-1/zone-1", out.Items[0].VolumeTypeZoneID, out.Items[0].ZoneID)
	}
}

func TestVolumeListVolumeTypesWithoutZoneKeepsProjectList(t *testing.T) {
	fixture := newSvcFixture(lookupVolumeRoutes())
	stdout, err := runLookup(t, fixture, "volume", "list-volume-types")
	if err != nil {
		t.Fatalf("list-volume-types: %v", err)
	}
	if got := fixture.requestPaths(); !slices.Equal(got, []string{"/v1/proj-1/volume_types"}) {
		t.Fatalf("requests = %v, want the project-wide list only", got)
	}
	if !strings.Contains(stdout, "t-2") {
		t.Fatalf("stdout lacks t-2: %s", stdout)
	}
}

func TestVolumeListVolumeTypesBothZoneFlagsExitsTwoWithoutRequests(t *testing.T) {
	fixture := newSvcFixture(lookupVolumeRoutes())
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "--project-id", "proj-1", "volume", "list-volume-types",
		"--zone-id", "zone-1", "--volume-type-zone-id", "vtz-1"})
	err := root.ExecuteContext(context.Background())
	if err == nil || exitCode(err) != 2 {
		t.Fatalf("exitCode = %d, err = %v, want exit 2 (stderr=%s)", exitCode(err), err, stderr.String())
	}
	if n := fixture.requestCount(); n != 0 {
		t.Fatalf("requestCount = %d, want 0", n)
	}
}

// TestComputeFlavorReadsMatchDesignTable checks the flags of list-flavor-zones
// and list-flavors. No flag on list-flavors is required, so the SDK's
// one-of rule is the only check and its message reaches the caller.
func TestComputeFlavorReadsMatchDesignTable(t *testing.T) {
	wantFlags := map[string][]string{
		"list-flavor-zones": {"zone-id"},
		"list-flavors":      {"flavor-zone-id", "zone-id", "name"},
	}
	for _, op := range computeOps {
		want, ok := wantFlags[op.name]
		if !ok {
			continue
		}
		specs, err := flagSpecsFor(op.newInput())
		if err != nil {
			t.Fatalf("%s: flagSpecsFor: %v", op.name, err)
		}
		var got []string
		for _, s := range specs {
			got = append(got, s.flagName)
		}
		if !slices.Equal(got, want) {
			t.Errorf("%s flags = %v, want %v", op.name, got, want)
		}
		delete(wantFlags, op.name)
	}
	if len(wantFlags) != 0 {
		t.Errorf("commands missing from computeOps: %v", wantFlags)
	}
}
