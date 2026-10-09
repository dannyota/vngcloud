package compute

import (
	"encoding/json"
	"os"
	"testing"
)

func TestSanitizedServerInstanceFixture(t *testing.T) {
	data, err := os.ReadFile("../testdata/server/instance.json")
	if err != nil {
		t.Fatal(err)
	}

	var fixture struct {
		Regions []struct {
			Region    string   `json:"region"`
			ProjectID string   `json:"projectId"`
			Count     int      `json:"count"`
			Items     []Server `json:"items"`
		} `json:"regions"`
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}

	if len(fixture.Regions) == 0 {
		t.Fatal("expected at least one region")
	}
	for _, region := range fixture.Regions {
		if region.Count == 0 {
			t.Fatalf("expected non-zero server count for region fixture %+v", region)
		}
		if len(region.Items) == 0 {
			t.Fatalf("expected server items for region fixture %+v", region)
		}
		first := region.Items[0]
		if first.UUID == "" {
			t.Fatal("expected server UUID field to decode")
		}
		if first.Flavor.CPU == 0 {
			t.Fatal("expected flavor CPU field to decode")
		}
		if len(first.InternalInterfaces) == 0 {
			t.Fatal("expected internal interfaces to decode")
		}
	}
}

// TestSanitizedCreateServerFixtureNotLive decodes create_server.json, built
// from the API reference rather than a live capture (no live run has
// priced or ordered a server yet).
func TestSanitizedCreateServerFixtureNotLive(t *testing.T) {
	data, err := os.ReadFile("../testdata/compute/create_server.json")
	if err != nil {
		t.Fatal(err)
	}
	var resp createServerResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Data.UUID != "server-1" {
		t.Fatalf("UUID = %q, want server-1", resp.Data.UUID)
	}
}

// TestSanitizedServerStatusesFixtureNotLive decodes server_statuses.json,
// one Server per status in the design's status table, built from the
// reference rather than a live capture.
func TestSanitizedServerStatusesFixtureNotLive(t *testing.T) {
	data, err := os.ReadFile("../testdata/compute/server_statuses.json")
	if err != nil {
		t.Fatal(err)
	}
	var servers []Server
	if err := json.Unmarshal(data, &servers); err != nil {
		t.Fatal(err)
	}
	wantStatuses := []string{
		"CREATING", "CREATING-BILLING", "ACTIVE", "ERROR", "TURNING-OFF",
		"STOPPED", "STARTING", "REBOOTING", "CHANGING-FLAVOR", "VERIFYING-FLAVOR", "DELETING",
	}
	if len(servers) != len(wantStatuses) {
		t.Fatalf("got %d servers, want %d", len(servers), len(wantStatuses))
	}
	for i, want := range wantStatuses {
		if servers[i].UUID != "server-1" {
			t.Fatalf("servers[%d].UUID = %q, want server-1", i, servers[i].UUID)
		}
		if servers[i].Status != want {
			t.Fatalf("servers[%d].Status = %q, want %q", i, servers[i].Status, want)
		}
	}
}
