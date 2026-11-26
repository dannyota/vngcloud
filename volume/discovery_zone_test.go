package volume

import (
	"errors"
	"net/http"
	"testing"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/testutil"
)

func TestVolumePricesSelectedDisabledZone(t *testing.T) {
	for _, zone := range []string{"HCM03-1C", "unknown"} {
		t.Run(zone, func(t *testing.T) {
			quotes := 0
			c := newTestClient(t, volumeDiscoveryHandler(t, map[string]string{"/v1/project-1/zones": `{"data":[{"uuid":"HCM03-1C","isEnabled":false}]}`}, func(w http.ResponseWriter, r *http.Request) {
				quotes++
				testutil.WriteFixture(t, w, "../testdata/volume/discovery_quote_plain.json")
			}))
			out, err := c.ListVolumePrices(discoveryTestContext(), &ListVolumePricesInput{ZoneID: zone, IOPS: 3000, Encryption: "none"})
			if zone == "unknown" {
				if !errors.Is(err, vngcloud.ErrInvalidInput) || quotes != 0 {
					t.Fatalf("error %v quotes %d", err, quotes)
				}
				return
			}
			if err != nil || out == nil || len(out.Items) != 1 || quotes != 1 {
				t.Fatalf("output %+v error %v quotes %d", out, err, quotes)
			}
			if out.Items[0].ZoneEnabled == nil || *out.Items[0].ZoneEnabled || out.Items[0].Price.Status != "quoted" {
				t.Fatalf("row %+v", out.Items[0])
			}
		})
	}
}
