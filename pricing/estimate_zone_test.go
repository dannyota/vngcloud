package pricing

import (
	"errors"
	"net/http"
	"testing"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/testutil"
)

func TestEstimateSelectedDisabledZone(t *testing.T) {
	for _, kind := range []string{"server", "volume"} {
		for _, zone := range []string{"HCM03-1C", "unknown"} {
			t.Run(kind+"/"+zone, func(t *testing.T) {
				quotes := 0
				base := estimateHandler(t, func(w http.ResponseWriter, r *http.Request) {
					quotes++
					file := "volume/discovery_quote_plain.json"
					if kind == "server" {
						file = "compute/discovery_quote_server_s2.json"
					}
					testutil.WriteFixture(t, w, "../testdata/"+file)
				})
				cfg := testutil.NewConfig(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == "/v1/project-1/zones" {
						_, _ = w.Write([]byte(`{"data":[{"uuid":"HCM03-1C","isEnabled":false}]}`))
						return
					}
					base(w, r)
				}))
				in := &EstimateInput{}
				if kind == "server" {
					s := estimateServer()
					s.ZoneID = zone
					in.Servers = []EstimateServer{s}
				} else {
					in.Volumes = []EstimateVolume{{Key: "v", ZoneID: zone, VolumeTypeID: "vtype-1", Size: 1, Quantity: 1}}
				}
				out, err := New(cfg).Estimate(estimateContext(), in)
				if zone == "unknown" {
					if !errors.Is(err, vngcloud.ErrInvalidInput) || quotes != 0 {
						t.Fatalf("error %v quotes %d", err, quotes)
					}
					return
				}
				if err != nil || out == nil || len(out.Items) != 1 || quotes != 1 || out.Items[0].Price.Status != "quoted" {
					t.Fatalf("output %+v error %v quotes %d", out, err, quotes)
				}
			})
		}
	}
}
