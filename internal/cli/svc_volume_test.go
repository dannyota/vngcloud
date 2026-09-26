package cli

import (
	"context"
	"net/http"
	"testing"

	"danny.vn/vngcloud/volume"
)

// exampleVolume is a representative Volume, reused by the golden tests
// below so they exercise a realistic row instead of an empty struct.
func exampleVolume() volume.Volume {
	return volume.Volume{
		UUID:         "volume-1",
		Name:         "data",
		Size:         100,
		Status:       "IN-USE",
		VolumeTypeID: "type-1",
		IOPS:         "3000",
		ServerID:     "server-1",
		CreatedAt:    "2026-01-01T00:00:00Z",
		Bootable:     false,
		Zone:         volume.Zone{UUID: "zone-a", Name: "zone-a-name"},
	}
}

// TestGoldenVolumeListVolumes checks list-volumes' exact output shape
// through the generic renderer, using the real volume.ListVolumesOutput
// type (a core.PagedList[Volume]).
func TestGoldenVolumeListVolumes(t *testing.T) {
	v := &volume.ListVolumesOutput{Items: []volume.Volume{exampleVolume()}, Page: 1, PageSize: 10000, TotalPage: 1, TotalItem: 1}
	checkGolden(t, "volume-list-volumes.json.golden", "json", "", v)
	checkGolden(t, "volume-list-volumes.table.golden", "table", "", v)
}

// TestGoldenVolumeGetVolume checks get-volume's exact output shape,
// {"Volume": {...}}, the shape every Get in this design wraps its resource
// in.
func TestGoldenVolumeGetVolume(t *testing.T) {
	v := &volume.GetVolumeOutput{Volume: exampleVolume()}
	checkGolden(t, "volume-get-volume.json.golden", "json", "", v)
	checkGolden(t, "volume-get-volume.table.golden", "table", "", v)
}

// TestVolumeCommandsMatchDesignTable checks the CLI reads design's "volume"
// table: the nine command names (get-default-volume-type stays out, per the
// design's decision 3), each with the flags the table names.
func TestVolumeCommandsMatchDesignTable(t *testing.T) {
	wantFlags := map[string][]string{
		"list-volumes":           {"name", "page", "size"},
		"get-volume":             {"volume-id"},
		"get-underlying-volume":  {"volume-id"},
		"list-volume-type-zones": {"zone-id"},
		"list-volume-types":      {"volume-type-zone-id"},
		"get-volume-type":        {"volume-type-id"},
		"list-encryption-types":  nil,
		"list-snapshots":         {"volume-id", "page", "size"},
		"list-all-snapshots":     nil,
	}
	if got := opNames(volumeOps); len(got) != len(wantFlags) {
		t.Fatalf("volume ops = %v, want %d commands", got, len(wantFlags))
	}
	for _, op := range volumeOps {
		want, ok := wantFlags[op.name]
		if !ok {
			t.Fatalf("unexpected volume command %q", op.name)
		}
		specs, err := flagSpecsFor(op.newInput())
		if err != nil {
			t.Fatalf("%s: flagSpecsFor: %v", op.name, err)
		}
		var got []string
		for _, s := range specs {
			got = append(got, s.flagName)
		}
		if !equalStringSlices(got, want) {
			t.Errorf("%s flags = %v, want %v", op.name, got, want)
		}
	}
}

// TestVolumeGetVolumeMissingVolumeIDStopsBeforeAnyRequest checks the
// required-flag guard: get-volume's --volume-id is vngcloud:"required", so a
// missing flag must refuse the command with exit code 2 before any request
// reaches the server.
func TestVolumeGetVolumeMissingVolumeIDStopsBeforeAnyRequest(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/volumes/": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "--project-id", "proj-1", "volume", "get-volume"})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatalf("expected an error for a missing --volume-id")
	}
	if exitCode(err) != 2 {
		t.Fatalf("exitCode = %d, want 2 (stderr=%s)", exitCode(err), stderr.String())
	}
	if n := fixture.requestCount(); n != 0 {
		t.Fatalf("requestCount = %d, want 0", n)
	}
}

// TestVolumeListSnapshotsMissingVolumeIDStopsBeforeAnyRequest is the same
// required-flag guard for list-snapshots' --volume-id.
func TestVolumeListSnapshotsMissingVolumeIDStopsBeforeAnyRequest(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/volumes/": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "--project-id", "proj-1", "volume", "list-snapshots"})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatalf("expected an error for a missing --volume-id")
	}
	if exitCode(err) != 2 {
		t.Fatalf("exitCode = %d, want 2 (stderr=%s)", exitCode(err), stderr.String())
	}
	if n := fixture.requestCount(); n != 0 {
		t.Fatalf("requestCount = %d, want 0", n)
	}
}

// TestVolumeListVolumesUsesTheProjectScopedPath checks that list-volumes (a
// Project-scoped read, per the CLI reads design's scope table) sends its
// request under the given --project-id, with its Name/Page/Size flags
// reaching the query string.
func TestVolumeListVolumesUsesTheProjectScopedPath(t *testing.T) {
	body := `{"listData":[{"uuid":"volume-1","name":"data","status":"IN-USE"}],"page":2,"pageSize":10,"totalPage":1,"totalItem":1}`
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/volumes": jsonHandler(http.StatusOK, body),
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1", "volume", "list-volumes",
		"--name", "data", "--page", "2", "--size", "10",
	})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("execute: %v (stderr=%s)", err, stderr.String())
	}
	q, ok := fixture.queryFor("/v2/proj-1/volumes")
	if !ok {
		t.Fatalf("no request observed")
	}
	if q != "name=data&page=2&size=10" {
		t.Fatalf("query string = %q, want name=data&page=2&size=10", q)
	}
	if got, ok := fixture.methodFor("/v2/proj-1/volumes"); !ok || got != http.MethodGet {
		t.Fatalf("method = %q, ok=%v, want GET", got, ok)
	}
	if got := stdout.String(); got == "" {
		t.Fatalf("stdout is empty")
	}
}
