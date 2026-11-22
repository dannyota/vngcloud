package pricing

import (
	"errors"
	"net/http"
	"strconv"
	"testing"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/testutil"
)

func TestEstimateRejectsQuoteFieldAliases(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		storage    bool
	}{
		{"refusal", `{"Success":false,"optimumPrice":10}`, false},
		{"breakdown", `{"optimumPrice":10,"PropertiesPrice":[{"name":"bad","monthlyPrice":-1}]}`, false},
		{"price", `{"OptimumPrice":10}`, false},
		{"currency", `{"optimumPrice":10,"Currency":"VND"}`, false},
		{"line", `{"optimumPrice":10,"propertiesPrice":[{"Name":"line","MonthlyPrice":10}]}`, false},
		{"pagination", `{"optimumPrice":10,"PageSize":1}`, false},
		{"continuation", `{"optimumPrice":10,"IsNext":false}`, false},
		{"error", `{"optimumPrice":10,"ErrorCode":"refused"}`, false},
		{"storage envelope", `{"Success":true,"data":{"optimumPrice":10}}`, true},
		{"storage data", `{"success":true,"Data":{"optimumPrice":10}}`, true},
		{"storage breakdown", `{"success":true,"data":{"optimumPrice":10,"PropertiesPrice":[{"name":"bad","monthlyPrice":-1}]}}`, true},
		{"storage pagination", `{"success":true,"data":{"optimumPrice":10},"HasMore":true}`, true},
	} {
		for _, partial := range []bool{false, true} {
			t.Run(tc.name+"/"+strconv.FormatBool(partial), func(t *testing.T) {
				calls := 0
				quote := func(w http.ResponseWriter, _ *http.Request) { calls++; _, _ = w.Write([]byte(tc.body)) }
				in := &EstimateInput{AllowPartial: partial, PublicIPs: []EstimatePublicIP{{Key: "unknown", Kind: "reservation", Quantity: 1}, {Key: "quoted", Kind: "public-vip", Type: "public", Quantity: 1}}}
				var handler http.Handler = estimateHandler(t, quote)
				if tc.storage {
					in.PublicIPs = in.PublicIPs[:1]
					in.Storage = []EstimateStorage{{Key: "objects", Type: "Gold", QuotaGB: 30, Quantity: 1}}
					handler = estimateStorageHandler(t, quote)
				}
				out, err := New(testutil.NewConfig(t, handler)).Estimate(estimateContext(), in)
				var api *vngcloud.APIError
				if !errors.As(err, &api) || api.Code != "InvalidResponse" || api.StatusCode != 200 || calls != 1 {
					t.Fatalf("output_nil=%t error %v calls %d", out == nil, err, calls)
				}
				if !partial && out != nil {
					t.Fatal("fail-fast output")
				}
				if partial && (out == nil || !errors.Is(err, ErrDiscoveryIncomplete) || out.Metadata.Complete || out.Total.RequestedMonthlyTotal != nil || out.Total.KnownMonthlySubtotal != nil) {
					t.Fatalf("partial output %+v error %v", out, err)
				}
			})
		}
	}
}

func TestEstimateRejectsAmbiguousCatalogFields(t *testing.T) {
	for _, tc := range []struct {
		name, path, body string
		encrypted        bool
	}{
		{"duplicate enabled", "/v1/project-1/zones", `{"data":[{"uuid":"HCM03-1C","isEnabled":false,"isEnabled":true}]}`, false},
		{"enabled alias", "/v1/project-1/zones", `{"data":[{"uuid":"HCM03-1C","IsEnabled":true}]}`, false},
		{"paging alias", "/v1/project-1/zones", `{"data":[{"uuid":"HCM03-1C","isEnabled":true}],"HasMore":false}`, false},
		{"parent alias", "/v1/project-1/volume_type_zones", `{"volumeTypeZones":[{"ID":"volume-zone-1"}]}`, false},
		{"duplicate type", "/v1/project-1/volume-zone-1/volume_types", `{"volumeTypes":[{"id":"vtype-1","minSize":1,"maxSize":5000,"minSize":20}]}`, false},
		{"duplicate licence", "/v1/project-1/images/os", `{"images":[{"id":"img-5","imageType":"Ubuntu","imageVersion":"1","licence":true,"licence":false,"packageLimit":{"diskSize":20}}]}`, false},
		{"licence alias", "/v1/project-1/images/os", `{"images":[{"id":"img-5","imageType":"Ubuntu","imageVersion":"1","Licence":false,"packageLimit":{"diskSize":20}}]}`, false},
		{"selector alias", "/v1/project-1/images/os", `{"images":[{"id":"img-5","imageType":"Ubuntu","imageVersion":"1","licence":false,"packageLimit":{"DiskSize":20}}]}`, false},
		{"duplicate encryption", "/v1/project-1/volumes/encryption_types", `[{"key":"unknown","key":"aes-xts-plain64_256"}]`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			quotes := 0
			base := estimateHandler(t, func(w http.ResponseWriter, _ *http.Request) { quotes++; _, _ = w.Write([]byte(`{"optimumPrice":10}`)) })
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == tc.path {
					_, _ = w.Write([]byte(tc.body))
					return
				}
				base(w, r)
			})
			server := estimateServer()
			if tc.encrypted {
				server.RootDiskEncryptionTypeID = "aes-xts-plain64_256"
			}
			out, err := New(testutil.NewConfig(t, handler)).Estimate(estimateContext(), &EstimateInput{Servers: []EstimateServer{server}})
			var api *vngcloud.APIError
			if out != nil || !errors.As(err, &api) || api.Code != "InvalidResponse" || api.StatusCode != 200 || quotes != 0 {
				t.Fatalf("output_nil=%t error %v quotes %d", out == nil, err, quotes)
			}
		})
	}
}
