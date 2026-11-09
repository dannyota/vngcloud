package volume

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"sync"
	"testing"

	"danny.vn/vngcloud"
)

const volumeTypeZonesBody = `{"volumeTypeZones":[
 {"id":"vtz-1","zone":{"uuid":"zone-1"}},
 {"id":"vtz-2","zone":{"uuid":"zone-1"}},
 {"id":"vtz-other","zone":{"uuid":"zone-2"}},
 {"id":"vtz-3","zone":{"uuid":"zone-1"}}]}`

var volumeTypeBodies = map[string]string{
	"/v1/project-1/vtz-1/volume_types": `{"volumeTypes":[
 {"id":"t-1","name":"SSD","iops":3000},
 {"id":"t-2","name":"NVMe","iops":5000,"volumeTypeZoneId":"vtz-api","zoneId":"zone-api"}]}`,
	"/v1/project-1/vtz-2/volume_types": `{"volumeTypes":[]}`,
	"/v1/project-1/vtz-3/volume_types": `{"volumeTypes":[{"id":"t-3","name":"SSD","iops":3000}]}`,
}

// volumeTypeServer serves the bodies above and records request paths and
// queries in arrival order. A path in failPaths answers 500.
func volumeTypeServer(t *testing.T, failPaths ...string) (*Client, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var seen []string
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.URL.RequestURI())
		mu.Unlock()
		if slices.Contains(failPaths, r.URL.Path) {
			http.Error(w, `{"message":"boom"}`, http.StatusInternalServerError)
			return
		}
		if r.URL.Path == "/v1/project-1/volume_type_zones" {
			// The server ignores the zoneId filter, as the SDK must not rely on it.
			_, _ = w.Write([]byte(volumeTypeZonesBody))
			return
		}
		body, ok := volumeTypeBodies[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	return c, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(seen)
	}
}

func TestListVolumeTypesByZoneID(t *testing.T) {
	c, seen := volumeTypeServer(t)
	out, err := c.ListVolumeTypes(context.Background(), &ListVolumeTypesInput{ZoneID: "zone-1"})
	if err != nil {
		t.Fatalf("ListVolumeTypes() error = %v", err)
	}
	want := []string{
		"/v1/project-1/volume_type_zones?zoneId=zone-1",
		"/v1/project-1/vtz-1/volume_types",
		"/v1/project-1/vtz-2/volume_types",
		"/v1/project-1/vtz-3/volume_types",
	}
	if got := seen(); !slices.Equal(got, want) {
		t.Fatalf("requests = %v, want %v", got, want)
	}
	type row struct{ id, vtz, zone string }
	wantRows := []row{
		{"t-1", "vtz-1", "zone-1"},
		{"t-2", "vtz-api", "zone-api"},
		{"t-3", "vtz-3", "zone-1"},
	}
	if len(out.Items) != len(wantRows) {
		t.Fatalf("got %d rows, want %d: %+v", len(out.Items), len(wantRows), out.Items)
	}
	for i, w := range wantRows {
		g := out.Items[i]
		if g.ID != w.id || g.VolumeTypeZoneID != w.vtz || g.ZoneID != w.zone {
			t.Errorf("row %d = {%s %s %s}, want %+v", i, g.ID, g.VolumeTypeZoneID, g.ZoneID, w)
		}
	}
}

func TestListVolumeTypesByZoneIDNoMatchingZones(t *testing.T) {
	c, seen := volumeTypeServer(t)
	out, err := c.ListVolumeTypes(context.Background(), &ListVolumeTypesInput{ZoneID: "zone-nowhere"})
	if err != nil {
		t.Fatalf("ListVolumeTypes() error = %v", err)
	}
	if len(out.Items) != 0 || len(seen()) != 1 {
		t.Fatalf("rows = %+v, requests = %v; want none and one", out.Items, seen())
	}
}

func TestListVolumeTypesFilterByIOPS(t *testing.T) {
	c, _ := volumeTypeServer(t)
	for name, in := range map[string]*ListVolumeTypesInput{
		"zone":             {ZoneID: "zone-1", IOPS: 3000},
		"volume type zone": {VolumeTypeZoneID: "vtz-3", IOPS: 3000},
	} {
		out, err := c.ListVolumeTypes(context.Background(), in)
		if err != nil {
			t.Fatalf("%s: error = %v", name, err)
		}
		for _, it := range out.Items {
			if it.IOPS != 3000 {
				t.Errorf("%s: kept IOPS %d", name, it.IOPS)
			}
		}
		if len(out.Items) == 0 {
			t.Errorf("%s: no rows", name)
		}
	}

	out, err := c.ListVolumeTypes(context.Background(), &ListVolumeTypesInput{VolumeTypeZoneID: "vtz-1", IOPS: 1})
	if err != nil || len(out.Items) != 0 {
		t.Fatalf("out = %+v, err = %v; want no rows", out, err)
	}
}

func TestListVolumeTypesFillsVolumeTypeZoneID(t *testing.T) {
	c, _ := volumeTypeServer(t)
	out, err := c.ListVolumeTypes(context.Background(), &ListVolumeTypesInput{VolumeTypeZoneID: "vtz-1"})
	if err != nil {
		t.Fatalf("ListVolumeTypes() error = %v", err)
	}
	if out.Items[0].VolumeTypeZoneID != "vtz-1" || out.Items[1].VolumeTypeZoneID != "vtz-api" {
		t.Fatalf("unexpected volume type zone IDs: %+v", out.Items)
	}
}

func TestListVolumeTypesInvalidInput(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called")
	}))
	for name, in := range map[string]*ListVolumeTypesInput{
		"both":                    {ZoneID: "zone-1", VolumeTypeZoneID: "vtz-1"},
		"negative IOPS":           {IOPS: -1},
		"negative IOPS with zone": {ZoneID: "zone-1", IOPS: -1},
		"bad volume type zone ID": {VolumeTypeZoneID: ".."},
	} {
		if _, err := c.ListVolumeTypes(context.Background(), in); !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Errorf("%s: err = %v, want ErrInvalidInput", name, err)
		}
	}
}

func TestListVolumeTypesByZoneIDFailureReturnsNoPartialResult(t *testing.T) {
	c, seen := volumeTypeServer(t, "/v1/project-1/vtz-2/volume_types")
	out, err := c.ListVolumeTypes(context.Background(), &ListVolumeTypesInput{ZoneID: "zone-1"})
	if err == nil || out != nil {
		t.Fatalf("out = %+v, err = %v; want nil and an error", out, err)
	}
	if got := seen(); len(got) != 3 {
		t.Fatalf("requests = %v, want to stop at the failing zone", got)
	}
}

func TestListVolumeTypesByZoneIDRefusesBadVolumeTypeZoneID(t *testing.T) {
	var seen []string
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.URL.Path)
		_, _ = w.Write([]byte(`{"volumeTypeZones":[{"id":"vtz-1","zone":{"uuid":"zone-1"}},{"id":"../x","zone":{"uuid":"zone-1"}}]}`))
	}))
	_, err := c.ListVolumeTypes(context.Background(), &ListVolumeTypesInput{ZoneID: "zone-1"})
	if !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
	if len(seen) != 2 {
		t.Fatalf("requests = %v, want the zone list and the first zone only", seen)
	}
}
