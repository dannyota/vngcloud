package compute

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"danny.vn/vngcloud"
)

func TestFlavorPricesRootTypeSelection(t *testing.T) {
	for _, tc := range []struct {
		name, disk, catalog, response string
		wantQuote, wantError          bool
	}{
		{name: "custom", disk: "custom", catalog: `{"volumeTypes":[{"id":"custom","name":"custom","iops":6400,"minSize":1,"maxSize":100}]}`, response: `{"propertiesPrice":[{"name":"INSTANCE TYPE","monthlyPrice":10,"optimumPrice":10},{"name":"ROOT DISK","monthlyPrice":2,"optimumPrice":2}]}`, wantQuote: true},
		{name: "unknown", disk: "missing", catalog: `{"volumeTypes":[{"id":"custom","name":"custom","iops":6400,"minSize":1,"maxSize":100}]}`, wantError: true},
		{name: "size", disk: "custom", catalog: `{"volumeTypes":[{"id":"custom","name":"custom","iops":6400,"minSize":1,"maxSize":10}]}`},
		{name: "missing root", disk: "custom", catalog: `{"volumeTypes":[{"id":"custom","name":"custom","iops":6400,"minSize":1,"maxSize":100}]}`, response: `{"propertiesPrice":[{"name":"INSTANCE TYPE","monthlyPrice":10,"optimumPrice":10}]}`, wantQuote: true, wantError: true},
		{name: "wrong line", disk: "custom", catalog: `{"volumeTypes":[{"id":"custom","name":"custom","iops":6400,"minSize":1,"maxSize":100}]}`, response: `{"propertiesPrice":[{"name":"INSTANCE TYPE","monthlyPrice":10,"optimumPrice":10},{"name":"DATA DISK","monthlyPrice":2,"optimumPrice":2}]}`, wantQuote: true, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			quotes := 0
			c := newTestClient(t, flavorDiscoveryHandler(t, map[string]string{"/v1/project-1/volume-zone-1/volume_types": tc.catalog}, func(w http.ResponseWriter, r *http.Request) {
				quotes++
				var body struct {
					Info map[string]any `json:"resourceInfo"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				if body.Info["rootDiskTypeId"] != tc.disk || body.Info["rootDiskSize"] != float64(20) {
					t.Errorf("request %v", body.Info)
				}
				_, _ = w.Write([]byte(tc.response))
			}))
			in := &ListFlavorPricesInput{ZoneID: "HCM03-1C", Family: "general-purpose", Platform: "code-s2", Name: "s2-general-1x2", RootDiskTypeID: tc.disk}
			out, err := c.ListFlavorPrices(flavorDiscoveryContext(), in)
			wantCount := 0
			if tc.wantQuote {
				wantCount = 1
			}
			if quotes != wantCount || (err != nil) != tc.wantError {
				t.Fatalf("output %+v error %v quotes %d", out, err, quotes)
			}
			if tc.wantError && !tc.wantQuote && !errors.Is(err, vngcloud.ErrInvalidInput) {
				t.Fatalf("error %v", err)
			}
			if tc.name == "size" {
				if out == nil || len(out.Items) != 1 || out.Items[0].Price.Status != "inapplicable" {
					t.Fatalf("output %+v", out)
				}
				return
			}
			if !tc.wantError && (out == nil || len(out.Items) != 1 || out.Items[0].Price.Status != "quoted" || out.Items[0].Reference.RootDiskTypeID != tc.disk) {
				t.Fatalf("output %+v", out)
			}
		})
	}
}
