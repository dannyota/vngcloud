package compute

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"sync"
	"testing"

	"danny.vn/vngcloud"
)

const flavorZonesBody = `{"flavorZones":[
 {"id":"fz-1","name":"General","zoneId":"zone-1"},
 {"id":"fz-2","name":"Empty","zoneId":"zone-1"},
 {"id":"fz-other","name":"Other","zoneId":"zone-2"},
 {"id":"fz-3","name":"CPU","zoneId":"zone-1"}]}`

var flavorBodies = map[string]string{
	"/v1/project-1/fz-1/flavors": `{"flavors":[
 {"flavorId":"f-1","name":"s2-general-1x2"},
 {"flavorId":"f-2","name":"s2-general-2x4","flavorZoneId":"fz-api","zoneId":"zone-api","isSoldOut":true}]}`,
	"/v1/project-1/fz-2/flavors": `{"flavors":[]}`,
	"/v1/project-1/fz-3/flavors": `{"flavors":[
 {"flavorId":"f-1","name":"s2-general-1x2"}]}`,
}

// flavorServer serves flavorZonesBody and flavorBodies, and records the
// request paths in arrival order. A path in failPaths answers 500.
func flavorServer(t *testing.T, failPaths ...string) (*Client, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var paths []string
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		if slices.Contains(failPaths, r.URL.Path) {
			http.Error(w, `{"message":"boom"}`, http.StatusInternalServerError)
			return
		}
		if r.URL.Path == "/v1/project-1/flavor_zones/product" {
			_, _ = w.Write([]byte(flavorZonesBody))
			return
		}
		body, ok := flavorBodies[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	return c, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(paths)
	}
}

func TestListFlavorsByZoneID(t *testing.T) {
	c, paths := flavorServer(t)
	out, err := c.ListFlavors(context.Background(), &ListFlavorsInput{ZoneID: "zone-1"})
	if err != nil {
		t.Fatalf("ListFlavors() error = %v", err)
	}
	wantPaths := []string{
		"/v1/project-1/flavor_zones/product",
		"/v1/project-1/fz-1/flavors",
		"/v1/project-1/fz-2/flavors",
		"/v1/project-1/fz-3/flavors",
	}
	if got := paths(); !slices.Equal(got, wantPaths) {
		t.Fatalf("requests = %v, want %v", got, wantPaths)
	}
	type row struct{ id, fz, zone string }
	want := []row{
		{"f-1", "fz-1", "zone-1"},
		{"f-2", "fz-api", "zone-api"},
		{"f-1", "fz-3", "zone-1"},
	}
	if len(out.Items) != len(want) {
		t.Fatalf("got %d rows, want %d: %+v", len(out.Items), len(want), out.Items)
	}
	for i, w := range want {
		g := out.Items[i]
		if g.FlavorID != w.id || g.FlavorZoneID != w.fz || g.ZoneID != w.zone {
			t.Errorf("row %d = {%s %s %s}, want %+v", i, g.FlavorID, g.FlavorZoneID, g.ZoneID, w)
		}
	}
}

func TestListFlavorsByZoneIDNoFlavorZones(t *testing.T) {
	c, paths := flavorServer(t)
	out, err := c.ListFlavors(context.Background(), &ListFlavorsInput{ZoneID: "zone-nowhere"})
	if err != nil {
		t.Fatalf("ListFlavors() error = %v", err)
	}
	if len(out.Items) != 0 {
		t.Fatalf("rows = %+v, want none", out.Items)
	}
	if got := paths(); len(got) != 1 {
		t.Fatalf("requests = %v, want only the flavor zone list", got)
	}
}

func TestListFlavorsFillsFlavorZoneID(t *testing.T) {
	c, _ := flavorServer(t)
	out, err := c.ListFlavors(context.Background(), &ListFlavorsInput{FlavorZoneID: "fz-1"})
	if err != nil {
		t.Fatalf("ListFlavors() error = %v", err)
	}
	if out.Items[0].FlavorZoneID != "fz-1" || out.Items[1].FlavorZoneID != "fz-api" {
		t.Fatalf("unexpected flavor zone IDs: %+v", out.Items)
	}
}

func TestListFlavorsFilterByName(t *testing.T) {
	c, _ := flavorServer(t)
	out, err := c.ListFlavors(context.Background(), &ListFlavorsInput{ZoneID: "zone-1", Name: "s2-general-1x2"})
	if err != nil {
		t.Fatalf("ListFlavors() error = %v", err)
	}
	if len(out.Items) != 2 || out.Items[0].FlavorZoneID != "fz-1" || out.Items[1].FlavorZoneID != "fz-3" {
		t.Fatalf("unexpected rows: %+v", out.Items)
	}

	out, err = c.ListFlavors(context.Background(), &ListFlavorsInput{FlavorZoneID: "fz-1", Name: "s2-general"})
	if err != nil {
		t.Fatalf("ListFlavors() error = %v", err)
	}
	if len(out.Items) != 0 {
		t.Fatalf("a name prefix must not match: %+v", out.Items)
	}
}

func TestListFlavorsOneOfZoneIDAndFlavorZoneID(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called")
	}))
	for name, in := range map[string]*ListFlavorsInput{
		"nil":     nil,
		"neither": {Name: "x"},
		"both":    {ZoneID: "zone-1", FlavorZoneID: "fz-1"},
	} {
		_, err := c.ListFlavors(context.Background(), in)
		if !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Errorf("%s: err = %v, want ErrInvalidInput", name, err)
		}
	}
}

func TestListFlavorsByZoneIDFailureReturnsNoPartialResult(t *testing.T) {
	c, paths := flavorServer(t, "/v1/project-1/fz-2/flavors")
	out, err := c.ListFlavors(context.Background(), &ListFlavorsInput{ZoneID: "zone-1"})
	if err == nil {
		t.Fatalf("ListFlavors() = %+v, want an error", out)
	}
	if out != nil {
		t.Fatalf("out = %+v, want nil", out)
	}
	if got := paths(); len(got) != 3 || got[2] != "/v1/project-1/fz-2/flavors" {
		t.Fatalf("requests = %v, want to stop at the failing flavor zone", got)
	}
}

func TestListFlavorsByZoneIDFlavorZoneListFailure(t *testing.T) {
	c, paths := flavorServer(t, "/v1/project-1/flavor_zones/product")
	if _, err := c.ListFlavors(context.Background(), &ListFlavorsInput{ZoneID: "zone-1"}); err == nil {
		t.Fatal("want an error")
	}
	if got := paths(); len(got) != 1 {
		t.Fatalf("requests = %v, want one", got)
	}
}

func TestListFlavorsByZoneIDRefusesBadFlavorZoneID(t *testing.T) {
	var paths []string
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		_, _ = w.Write([]byte(`{"flavorZones":[{"id":"fz-1","zoneId":"zone-1"},{"id":"../x","zoneId":"zone-1"}]}`))
	}))
	// fz-1 answers with the same body (no flavors key), so only the second
	// flavor zone ID is at issue.
	_, err := c.ListFlavors(context.Background(), &ListFlavorsInput{ZoneID: "zone-1"})
	if !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
	if len(paths) != 2 {
		t.Fatalf("requests = %v, want the zone list and the first flavor zone only", paths)
	}
}
