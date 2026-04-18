package sdk

import (
	"encoding/json"
	"os"
	"testing"
)

func TestSanitizedServerInstanceFixture(t *testing.T) {
	data, err := os.ReadFile("../../testdata/server/instance.json")
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
