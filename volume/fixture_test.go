package volume

import (
	"encoding/json"
	"os"
	"testing"
)

// TestSanitizedCreateVolumeFixtureNotLive decodes create_volume.json, built
// from the API reference rather than a live capture (no live run has priced
// or ordered a volume yet). It exists so the fixture is machine-checked
// today and gets a real assertion once a live create replaces it.
func TestSanitizedCreateVolumeFixtureNotLive(t *testing.T) {
	data, err := os.ReadFile("../testdata/volume/create_volume.json")
	if err != nil {
		t.Fatal(err)
	}
	var resp createVolumeResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Data.UUID != "volume-1" {
		t.Fatalf("UUID = %q, want volume-1", resp.Data.UUID)
	}
}

// TestSanitizedVolumeStatusesFixtureNotLive decodes volume_statuses.json,
// one Volume per status in the design's status table, built from the
// reference rather than a live capture.
func TestSanitizedVolumeStatusesFixtureNotLive(t *testing.T) {
	data, err := os.ReadFile("../testdata/volume/volume_statuses.json")
	if err != nil {
		t.Fatal(err)
	}
	var volumes []Volume
	if err := json.Unmarshal(data, &volumes); err != nil {
		t.Fatal(err)
	}
	wantStatuses := []string{
		"CREATING", "CREATING-BILLING", "AVAILABLE", "ERROR", "RESIZING",
		"CHANGING-IOPS", "IN-USE", "ATTACHING", "DETACHING", "DELETING",
	}
	if len(volumes) != len(wantStatuses) {
		t.Fatalf("got %d volumes, want %d", len(volumes), len(wantStatuses))
	}
	for i, want := range wantStatuses {
		if volumes[i].UUID != "volume-1" {
			t.Fatalf("volumes[%d].UUID = %q, want volume-1", i, volumes[i].UUID)
		}
		if volumes[i].Status != want {
			t.Fatalf("volumes[%d].Status = %q, want %q", i, volumes[i].Status, want)
		}
	}
}
