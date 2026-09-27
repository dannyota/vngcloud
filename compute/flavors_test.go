package compute

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/testutil"
)

func TestListFlavorZones(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/project-1/flavor_zones/product" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		testutil.WriteFixture(t, w, "../testdata/compute/list_flavor_zones.json")
	}))

	out, err := c.ListFlavorZones(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListFlavorZones() error = %v", err)
	}
	if len(out.Items) != 2 || out.Items[0].ID != "flavor-zone-1" {
		t.Fatalf("unexpected flavor zones: %+v", out.Items)
	}
}

func TestListFlavorZonesFiltersByZoneID(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		testutil.WriteFixture(t, w, "../testdata/compute/list_flavor_zones.json")
	}))

	out, err := c.ListFlavorZones(context.Background(), &ListFlavorZonesInput{ZoneID: "zone-1"})
	if err != nil {
		t.Fatalf("ListFlavorZones() error = %v", err)
	}
	if len(out.Items) != 2 {
		t.Fatalf("unexpected filtered zones: %+v", out.Items)
	}

	out, err = c.ListFlavorZones(context.Background(), &ListFlavorZonesInput{ZoneID: "zone-nowhere"})
	if err != nil {
		t.Fatalf("ListFlavorZones() error = %v", err)
	}
	if len(out.Items) != 0 {
		t.Fatalf("unexpected zones for zone-nowhere: %+v", out.Items)
	}
}

func TestListFlavors(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/project-1/flavor-zone-1/flavors" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		testutil.WriteFixture(t, w, "../testdata/compute/list_flavors.json")
	}))

	out, err := c.ListFlavors(context.Background(), &ListFlavorsInput{FlavorZoneID: "flavor-zone-1"})
	if err != nil {
		t.Fatalf("ListFlavors() error = %v", err)
	}
	if len(out.Items) != 2 {
		t.Fatalf("unexpected flavors: %+v", out.Items)
	}
	if out.Items[0].RemainingVMs != 10 || out.Items[0].IsSoldOut {
		t.Fatalf("unexpected flavor 0: %+v", out.Items[0])
	}
	if !out.Items[1].IsSoldOut {
		t.Fatalf("unexpected flavor 1 IsSoldOut: %+v", out.Items[1])
	}
}

func TestListFlavorsRequiresFlavorZoneID(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called")
	}))
	if _, err := c.ListFlavors(context.Background(), &ListFlavorsInput{}); !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
}

func TestListFlavorsRejectsBadFlavorZoneID(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called")
	}))
	for _, bad := range []string{"..", ".", "/", "?"} {
		if _, err := c.ListFlavors(context.Background(), &ListFlavorsInput{FlavorZoneID: bad}); !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Fatalf("FlavorZoneID=%q err = %v, want ErrInvalidInput", bad, err)
		}
	}
}
