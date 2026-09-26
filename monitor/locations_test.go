package monitor

import (
	"context"
	"net/http"
	"testing"

	"danny.vn/vngcloud/internal/testutil"
)

func TestListLocationsDecodesFixture(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("method = %s", r.Method)
		}
		if r.URL.Path != "/vmonitor-uptime-manager/v1/locations" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		testutil.WriteFixture(t, w, "../testdata/monitor/ListLocations.json")
	}))

	out, err := client.ListLocations(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListLocations() error = %v", err)
	}
	if len(out.Items) != 2 {
		t.Fatalf("unexpected locations: %+v", out.Items)
	}

	first := out.Items[0]
	if first.ID != "loc-1" || first.Name != "SYNTT-VN-HCM01" || first.Type != "PUBLIC" {
		t.Fatalf("unexpected location: %+v", first)
	}
	if first.Description != "Public Location 02" || first.Status != "REPORTING" {
		t.Fatalf("unexpected location: %+v", first)
	}
	if first.CreatedAt != "Jan 1, 2026, 12:00:00 AM" || first.UpdatedAt != "Jan 2, 2026, 1:00:00 PM" {
		t.Fatalf("unexpected timestamps: %+v", first)
	}

	second := out.Items[1]
	if second.ID != "loc-2" || second.Name != "SYNTT-VN-HAN01" {
		t.Fatalf("unexpected location: %+v", second)
	}
}
