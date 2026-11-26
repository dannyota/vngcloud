package compute

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestFlavorPricesMixedRootBounds(t *testing.T) {
	for _, partial := range []bool{false, true} {
		t.Run(map[bool]string{false: "normal", true: "partial"}[partial], func(t *testing.T) {
			flavors := []Flavor{{FlavorID: "small", FlavorZoneID: "fz", Name: "small", CPU: 1, Memory: 2, RemainingVMs: 1, Metadata: `{"imageTypeSupport":["Ubuntu"]}`}, {FlavorID: "large", FlavorZoneID: "fz", Name: "large", CPU: 2, Memory: 2, RemainingVMs: 1, Metadata: `{"imageTypeSupport":["Ubuntu"]}`}}
			raw, err := json.Marshal(flavors)
			if err != nil {
				t.Fatal(err)
			}
			overrides := map[string]string{
				"/v1/project-1/flavors/families/general-purpose/platforms/code-s2": string(raw),
				"/v1/project-1/images/os":                  `{"images":[{"id":"high-disk","imageType":"Ubuntu","imageVersion":"a","licence":false,"packageLimit":{"cpu":1,"memory":1,"diskSize":40}},{"id":"low-disk","imageType":"Ubuntu","imageVersion":"b","licence":false,"packageLimit":{"cpu":2,"memory":1,"diskSize":20}}]}`,
				"/v1/project-1/volume-zone-1/volume_types": `{"volumeTypes":[{"id":"custom","iops":6400,"minSize":1,"maxSize":30}]}`,
			}
			quotes := 0
			c := newTestClient(t, flavorDiscoveryHandler(t, overrides, func(w http.ResponseWriter, r *http.Request) {
				quotes++
				var body struct {
					Info map[string]any `json:"resourceInfo"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				if body.Info["flavorId"] != "large" || body.Info["rootDiskSize"] != float64(20) {
					t.Errorf("body %v", body.Info)
				}
				_, _ = w.Write([]byte(`{"propertiesPrice":[{"name":"INSTANCE TYPE","monthlyPrice":10,"optimumPrice":10},{"name":"ROOT DISK"}]}`))
			}))
			out, err := c.ListFlavorPrices(flavorDiscoveryContext(), &ListFlavorPricesInput{ZoneID: "HCM03-1C", Family: "general-purpose", Platform: "code-s2", RootDiskTypeID: "custom", AllowPartial: partial})
			if err != nil || out == nil || quotes != 1 || len(out.Items) != 2 || !out.Metadata.Complete {
				t.Fatalf("output %+v error %v quotes %d", out, err, quotes)
			}
			statuses := map[string]string{}
			for _, row := range out.Items {
				statuses[row.Flavor.FlavorID] = row.Price.Status
			}
			if statuses["small"] != "inapplicable" || statuses["large"] != "quoted" {
				t.Fatalf("statuses %v", statuses)
			}
			found := false
			for _, note := range out.Metadata.Notes {
				if note.Topic == "inapplicable" && note.Scope == flavorDiscoveryKey(out.Items[0]) && strings.Contains(note.Text, "size") && strings.Contains(note.Text, "maximum") {
					found = true
				}
			}
			if !found {
				t.Fatalf("missing reason %+v", out.Metadata.Notes)
			}
		})
	}
}
