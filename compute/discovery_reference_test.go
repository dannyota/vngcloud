package compute

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"danny.vn/vngcloud/internal/testutil"
)

func TestListFlavorPricesUnusableImageSupport(t *testing.T) {
	for _, metadata := range []string{"", `null`, `{}`, `{"imageTypeSupport":null}`, `{"imageTypeSupport":[]}`, `{"imageTypeSupport":[""]}`, `{"imageTypeSupport":[1]}`, `{"imageTypeSupport":"Ubuntu"}`, `{"imageTypeSupport":["ubuntu"]}`, `{"imageTypeSupport":["CentOs"]}`, `{"imageTypeSupport":[" Ubuntu"]}`} {
		t.Run(metadata, func(t *testing.T) {
			raw, _ := json.Marshal([]Flavor{{FlavorID: "flav-1", FlavorZoneID: "fz-1", Name: "x", CPU: 1, Memory: 2, RemainingVMs: 1, Metadata: metadata}})
			c := newTestClient(t, flavorDiscoveryHandler(t, map[string]string{"/v1/project-1/flavors/families/general-purpose/platforms/code-s2": string(raw)}, func(http.ResponseWriter, *http.Request) { t.Error("unexpected quote") }))
			out, err := c.ListFlavorPrices(flavorDiscoveryContext(), &ListFlavorPricesInput{ZoneID: "HCM03-1C", Family: "general-purpose", Platform: "code-s2"})
			if err != nil || out == nil || len(out.Items) != 1 || out.Items[0].Reference != nil || out.Items[0].Price.Status != "unknown" {
				t.Fatalf("output %+v error %v", out, err)
			}
		})
	}
}

func TestListFlavorPricesReferencePresence(t *testing.T) {
	for _, image := range []string{
		`{"id":"i-1","imageType":"Ubuntu","imageVersion":"a","packageLimit":{"cpu":0,"memory":0,"diskSize":20}}`,
		`{"id":"i-1","imageType":"Ubuntu","imageVersion":"a","licence":null,"packageLimit":{"cpu":0,"memory":0,"diskSize":20}}`,
		`{"id":"i-1","imageType":"Ubuntu","imageVersion":"a","licence":true,"packageLimit":{"cpu":0,"memory":0,"diskSize":20}}`,
		`{"id":"i-1","imageType":"Ubuntu","imageVersion":"a","licence":false,"packageLimit":{"memory":0,"diskSize":20}}`,
		`{"id":"i-1","imageType":"Ubuntu","imageVersion":"a","licence":false,"packageLimit":{"cpu":null,"memory":0,"diskSize":20}}`,
		`{"id":"i-1","imageType":"Ubuntu","imageVersion":"a","licence":false,"packageLimit":{"cpu":-1,"memory":0,"diskSize":20}}`,
		`{"id":"i-1","imageType":"Ubuntu","imageVersion":"a","licence":false,"packageLimit":{"cpu":0,"memory":3,"diskSize":20}}`,
		`{"id":"i-1","imageType":"Ubuntu","imageVersion":"a","licence":false,"packageLimit":{"cpu":0,"memory":0,"diskSize":0}}`,
	} {
		t.Run(image, func(t *testing.T) {
			c := newTestClient(t, flavorDiscoveryHandler(t, map[string]string{"/v1/project-1/images/os": `{"images":[` + image + `]}`}, func(http.ResponseWriter, *http.Request) { t.Error("unexpected quote") }))
			out, err := c.ListFlavorPrices(flavorDiscoveryContext(), &ListFlavorPricesInput{ZoneID: "HCM03-1C", Family: "general-purpose", Platform: "code-s2", Name: "s2-general-1x2"})
			if err != nil || out.Items[0].Reference != nil || out.Items[0].Price.MonthlyPrice != nil {
				t.Fatalf("output %+v error %v", out, err)
			}
		})
	}
}

func TestListFlavorPricesInstanceLinePresence(t *testing.T) {
	for _, raw := range []string{`{}`, `{"propertiesPrice":null}`, `{"propertiesPrice":[]}`, `{"propertiesPrice":[{"name":"ROOT DISK","monthlyPrice":1,"optimumPrice":1}]}`, `{"propertiesPrice":[{"name":"INSTANCE TYPE","monthlyPrice":null,"optimumPrice":1}]}`, `{"propertiesPrice":[{"name":"INSTANCE TYPE","monthlyPrice":1}]}`, `{"propertiesPrice":[{"name":"INSTANCE TYPE","monthlyPrice":0,"optimumPrice":0}]}`, `{"propertiesPrice":[{"name":"INSTANCE TYPE","monthlyPrice":1,"optimumPrice":2}]}`, `{"propertiesPrice":[{"name":"INSTANCE TYPE","monthlyPrice":1,"optimumPrice":1},{"name":"INSTANCE TYPE","monthlyPrice":1,"optimumPrice":1}]}`} {
		t.Run(raw, func(t *testing.T) {
			c := newTestClient(t, flavorDiscoveryHandler(t, nil, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(raw)) }))
			out, err := c.ListFlavorPrices(flavorDiscoveryContext(), &ListFlavorPricesInput{ZoneID: "HCM03-1C", Family: "general-purpose", Platform: "code-s2", Name: "s2-general-1x2"})
			if err == nil || out != nil {
				t.Fatalf("output %+v error %v", out, err)
			}
		})
	}
}

func TestListFlavorPricesSDKOrderAndPartial(t *testing.T) {
	for _, partial := range []bool{false, true} {
		for _, order := range [][]int{{2, 0, 1}, {1, 2, 0}} {
			items := []Flavor{{FlavorID: "f-3", FlavorZoneID: "fz-1", Name: "a", CPU: 2, Memory: 4}, {FlavorID: "f-1", FlavorZoneID: "fz-1", Name: "z", CPU: 1, Memory: 2}, {FlavorID: "f-2", FlavorZoneID: "fz-1", Name: "b", CPU: 2, Memory: 2}}
			shuffled := []Flavor{items[order[0]], items[order[1]], items[order[2]]}
			raw, _ := json.Marshal(shuffled)
			overrides := map[string]string{"/v1/project-1/flavors/families/general-purpose/platforms/code-s2": string(raw)}
			if partial {
				overrides["/v1/project-1/images/os"] = `{}`
			}
			c := newTestClient(t, flavorDiscoveryHandler(t, overrides, func(http.ResponseWriter, *http.Request) { t.Error("unexpected quote") }))
			out, err := c.ListFlavorPrices(flavorDiscoveryContext(), &ListFlavorPricesInput{ZoneID: "HCM03-1C", Family: "general-purpose", Platform: "code-s2", AllowPartial: partial})
			if (err != nil) != partial || out == nil {
				t.Fatalf("output %+v error %v", out, err)
			}
			for i, want := range []string{"f-1", "f-2", "f-3"} {
				if out.Items[i].Flavor.FlavorID != want {
					t.Fatalf("order %+v", out.Items)
				}
			}
		}
	}
}

func TestListFlavorPricesReferenceTiesAndBounds(t *testing.T) {
	images := `{"images":[{"id":"i-b","imageType":"Ubuntu","imageVersion":"b","licence":false,"zoneId":"ignored","packageLimit":{"cpu":0,"memory":0,"diskSize":20}},{"id":"i-c","imageType":"Ubuntu","imageVersion":"a","licence":false,"packageLimit":{"cpu":0,"memory":0,"diskSize":20}},{"id":"i-a","imageType":"Ubuntu","imageVersion":"a","licence":false,"packageLimit":{"cpu":0,"memory":0,"diskSize":20}}]}`
	for _, maximum := range []int{19, 20} {
		overrides := map[string]string{"/v1/project-1/images/os": images, "/v1/project-1/volume-zone-1/volume_types": fmt.Sprintf(`{"volumeTypes":[{"id":"d-b","iops":3000,"minSize":1,"maxSize":%d},{"id":"d-a","iops":3000,"minSize":1,"maxSize":%d}]}`, maximum, maximum)}
		quotes := 0
		c := newTestClient(t, flavorDiscoveryHandler(t, overrides, func(w http.ResponseWriter, r *http.Request) {
			quotes++
			var body struct {
				Info map[string]any `json:"resourceInfo"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body.Info["imageId"] != "i-a" || body.Info["rootDiskTypeId"] != "d-a" {
				t.Fatalf("body %+v", body)
			}
			testutil.WriteFixture(t, w, "../testdata/compute/discovery_quote_server_s.json")
		}))
		out, err := c.ListFlavorPrices(flavorDiscoveryContext(), &ListFlavorPricesInput{ZoneID: "HCM03-1C", Family: "general-purpose", Platform: "code-s2", Name: "s2-general-1x2"})
		if err != nil || quotes != maximum-19 || (out.Items[0].Reference != nil) != (maximum == 20) {
			t.Fatalf("output %+v error %v quotes %d", out, err, quotes)
		}
	}
}

func TestListFlavorPricesWrongTypeSelectorsStayUnknown(t *testing.T) {
	for _, overrides := range []map[string]string{
		{"/v1/project-1/flavors/families/general-purpose/platforms/code-s2": `[{"flavorId":"f-1","flavorZoneId":"fz-1","name":"x","cpu":1,"memory":2,"remainingVms":1,"isSoldOut":false,"metaData":{}}]`},
		{"/v1/project-1/images/os": `{"images":[{"id":"i-1","imageType":"Ubuntu","imageVersion":"a","licence":false,"packageLimit":{"cpu":0.5,"memory":0,"diskSize":20}}]}`},
		{"/v1/project-1/images/os": `{"images":[{"id":"i-1","imageType":"Ubuntu","imageVersion":"a","licence":false,"packageLimit":{"cpu":0,"memory":"0","diskSize":20}}]}`},
	} {
		c := newTestClient(t, flavorDiscoveryHandler(t, overrides, func(http.ResponseWriter, *http.Request) { t.Error("unexpected quote") }))
		out, err := c.ListFlavorPrices(flavorDiscoveryContext(), &ListFlavorPricesInput{ZoneID: "HCM03-1C", Family: "general-purpose", Platform: "code-s2"})
		if err != nil || out == nil || len(out.Items) == 0 || out.Items[0].Reference != nil {
			t.Fatalf("output %+v error %v", out, err)
		}
	}
}
