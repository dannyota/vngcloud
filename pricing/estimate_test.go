package pricing

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/discovery"
	"danny.vn/vngcloud/internal/testutil"
)

func estimateContext() context.Context {
	now := time.Unix(1000, 0)
	return discovery.WithTestClock(context.Background(), func() time.Time { return now }, func(ctx context.Context, d time.Duration) error {
		now = now.Add(d)
		return ctx.Err()
	})
}

func estimateServer() EstimateServer {
	return EstimateServer{Key: "web", Quantity: 2, ZoneID: "HCM03-1C", FlavorID: "flav-1", ImageID: "img-5", RootDiskSize: 20, RootDiskTypeID: "vtype-1"}
}

func estimateHandler(t *testing.T, quote http.HandlerFunc) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/v1/price" {
			quote(w, r)
			return
		}
		file := map[string]string{
			"/v1/project-1/zones":                      "portal/discovery_zones.json",
			"/v1/project-1/volume_type_zones":          "volume/list_volume_type_zones.json",
			"/v1/project-1/volume-zone-1/volume_types": "volume/discovery_types.json",
			"/v1/project-1/images/os?zoneId=HCM03-1C":  "compute/discovery_os_images.json",
			"/v1/project-1/volumes/encryption_types":   "volume/discovery_encryption_types.json",
		}[r.URL.RequestURI()]
		if file == "" || r.Method != http.MethodGet {
			t.Errorf("unexpected route %s %s", r.Method, r.URL)
			w.WriteHeader(500)
			return
		}
		testutil.WriteFixture(t, w, "../testdata/"+file)
	}
}

func TestEstimateRawServerFixtures(t *testing.T) {
	for _, tc := range []struct {
		name, file      string
		data, encrypted bool
		want            float64
		lines           int
	}{
		{"plain", "compute/discovery_quote_server_s2.json", false, false, 347800, 2},
		{"data", "pricing/estimate_quote_server_data.json", true, false, 351000, 3},
		{"encrypted root", "pricing/estimate_quote_server_encrypted_root.json", false, true, 432940, 3},
		{"encrypted data", "pricing/estimate_quote_server_encrypted_data.json", true, true, 436140, 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := estimateServer()
			if tc.data {
				server.DataDiskSize, server.DataDiskTypeID = 1, "vtype-1"
			}
			if tc.encrypted && tc.data {
				server.DataDiskEncryptionTypeID = "aes-xts-plain64_256"
			}
			if tc.encrypted {
				server.RootDiskEncryptionTypeID = "aes-xts-plain64_256"
			}
			calls := 0
			cfg := testutil.NewConfig(t, estimateHandler(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
					return
				}
				info, ok := body["resourceInfo"].(map[string]any)
				if !ok {
					t.Fatal("missing info")
				}
				wantKeys := 8
				if tc.data {
					wantKeys += 2
				}
				if len(body) != 3 || body["resourceType"] != "server" || body["action"] != "create" || len(info) != wantKeys || info["period"] != float64(1) || info["isPoc"] != false || info["encryptionVolume"] != tc.encrypted || info["flavorId"] != "flav-1" || info["imageId"] != "img-5" || info["zoneId"] != "HCM03-1C" || info["rootDiskSize"] != float64(20) || info["rootDiskTypeId"] != "vtype-1" {
					t.Errorf("unexpected priced body %v", body)
				}
				if tc.data && (info["dataDiskSize"] != float64(1) || info["dataDiskTypeId"] != "vtype-1") {
					t.Error("missing data disk")
				}
				testutil.WriteFixture(t, w, "../testdata/"+tc.file)
			}))
			out, err := New(cfg).Estimate(estimateContext(), &EstimateInput{Servers: []EstimateServer{server}})
			if err != nil || out == nil {
				t.Fatalf("output %v error %v", out, err)
			}
			item := out.Items[0]
			if calls != 1 || *item.Price.MonthlyPrice != tc.want || *item.MonthlyAmount != tc.want*2 || len(item.Breakdown) != tc.lines || *out.Total.KnownMonthlySubtotal != tc.want*2 || !out.Total.RequestedComplete || out.Total.CostComplete || !out.Metadata.Complete {
				t.Fatalf("wrong accounting %+v", out)
			}
			encoded, err := json.Marshal(out)
			if err != nil || strings.Contains(string(encoded), "description") || strings.Contains(string(encoded), "licenseKey") {
				t.Fatal("unsafe output")
			}
		})
	}
}

func TestEstimateUnknownNeedsNoRequests(t *testing.T) {
	cfg := testutil.NewConfig(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("unexpected request") }))
	out, err := New(cfg).Estimate(estimateContext(), &EstimateInput{PublicIPs: []EstimatePublicIP{{Key: "ip", Quantity: 1, Kind: "floating-ip"}}})
	if err != nil || out == nil {
		t.Fatalf("output %v error %v", out, err)
	}
	if !out.Metadata.Complete || out.Total.RequestedComplete || out.Total.KnownMonthlySubtotal != nil || out.Total.RequestedMonthlyTotal != nil || out.Metadata.Requests != 0 || len(out.Coverage) < 10 {
		t.Fatalf("unknown accounting %+v", out)
	}
	raw, err := json.Marshal(out)
	if err != nil || !strings.Contains(string(raw), `"Breakdown":[]`) {
		t.Fatalf("arrays %s error %v", raw, err)
	}
}

func TestEstimateQuoteFailureMemoizationAndTerminal(t *testing.T) {
	for _, status := range []int{400, 401, 403, 404, 429, 500, 502, 503, 504} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			calls := 0
			cfg := testutil.NewConfig(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls++
				w.WriteHeader(status)
				_, _ = w.Write([]byte(`{"message":"untrusted response"}`))
			}))
			in := &EstimateInput{AllowPartial: true, PublicIPs: []EstimatePublicIP{{Key: "unknown", Kind: "reservation", Quantity: 1}, {Key: "a", Kind: "public-vip", Type: "public", Quantity: 1}, {Key: "b", Kind: "public-vip", Type: "public", Quantity: 2}, {Key: "c", Kind: "public-vip", Type: "public-vm", Quantity: 1}}}
			out, err := New(cfg).Estimate(estimateContext(), in)
			var api *vngcloud.APIError
			if out == nil || !errors.Is(err, ErrDiscoveryIncomplete) || !errors.As(err, &api) || api.StatusCode != status {
				t.Fatalf("output %v error %v", out, err)
			}
			wantCalls := 2
			if status == 401 || status == 429 {
				wantCalls = 1
			}
			if calls != wantCalls || len(out.Items) != 4 || out.Metadata.Complete || out.Total.RequestedMonthlyTotal != nil {
				t.Fatalf("calls %d output %+v", calls, out)
			}
			for _, row := range out.Items[1:] {
				if row.MonthlyAmount != nil || row.Price.Status != "failed" {
					t.Fatalf("failed row %+v", row)
				}
			}
		})
	}
}

func TestEstimatePreflightRefusesWholeQuotePhase(t *testing.T) {
	calls := 0
	cfg := testutil.NewConfig(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls++ }))
	out, err := New(cfg).Estimate(estimateContext(), &EstimateInput{MaxRequests: 1, PublicIPs: []EstimatePublicIP{{Key: "a", Kind: "public-vip", Type: "public", Quantity: 1}, {Key: "b", Kind: "public-vip", Type: "public-vm", Quantity: 1}}})
	if out != nil || !errors.Is(err, ErrEstimateLimit) || calls != 0 || !strings.Contains(err.Error(), "requires 2 requests") {
		t.Fatalf("output %v error %v calls %d", out, err, calls)
	}
}

func estimateStorageHandler(t *testing.T, quote http.HandlerFunc) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/billing-api/v2/price" && r.Method == http.MethodPost {
			if r.URL.Query().Get("region_id") != "region-2" || r.Header.Get("region") != "region-2" || r.Header.Get("region_id") != "region-2" {
				t.Error("missing storage price scope")
			}
			if quote != nil {
				quote(w, r)
			} else {
				testutil.WriteFixture(t, w, "../testdata/storage/discovery_quote_gold.json")
			}
			return
		}
		file := ""
		switch r.URL.Path {
		case "/internal/v1/regions":
			file = "discovery_regions.json"
		case "/internal/v1/billing/project_types":
			file = "discovery_project_types.json"
		case "/internal/v1/billing/purchase_types":
			file = "discovery_purchase_types.json"
		case "/billing-api/v1/configurations":
			file = map[string]string{"vos_billing_normal_min_quota": "discovery_config_min_quota.json", "vos_billing_normal_max_quota": "discovery_config_max_quota.json", "max_project_per_user_per_region": "discovery_config_max_projects.json", "enable_iam_checkout": "discovery_config_iam_checkout.json"}[r.URL.Query().Get("keys")]
		}
		if file == "" || r.Method != http.MethodGet {
			t.Errorf("unexpected storage route %s %s", r.Method, r.URL)
			w.WriteHeader(500)
			return
		}
		if r.URL.Path != "/internal/v1/regions" && (r.Header.Get("region") != "region-2" || r.Header.Get("region_id") != "region-2") {
			t.Error("missing catalog region headers")
		}
		testutil.WriteFixture(t, w, "../testdata/storage/"+file)
	}
}

func TestEstimateStoragePackageRawAndMemoization(t *testing.T) {
	calls := 0
	cfg := testutil.NewConfig(t, estimateStorageHandler(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		info, ok := body["resourceInfo"].(map[string]any)
		if !ok || len(body) != 3 || body["resourceType"] != "object_storage" || body["action"] != "create" || len(info) != 3 || info["quota"] != float64(30) || info["projectType"] != float64(1) || info["purchaseTypeId"] != float64(4) {
			t.Errorf("unexpected body %v", body)
		}
		testutil.WriteFixture(t, w, "../testdata/storage/discovery_quote_gold.json")
	}))
	out, err := New(cfg).Estimate(estimateContext(), &EstimateInput{Storage: []EstimateStorage{{Key: "a", Type: "Gold", QuotaGB: 30, Quantity: 2}, {Key: "b", Type: "Gold", QuotaGB: 30, Quantity: 1}}})
	if err != nil || out == nil {
		t.Fatalf("output %v error %v", out, err)
	}
	if calls != 1 || out.Metadata.Requests != 8 || out.Items[0].Region != "HCM04" || *out.Items[0].MonthlyAmount != *out.Items[0].Price.MonthlyPrice*2 || *out.Total.KnownMonthlySubtotal != *out.Items[0].Price.MonthlyPrice*3 {
		t.Fatalf("accounting %+v", out)
	}
}

func TestEstimateVolumeQuantityIndependentOfServer(t *testing.T) {
	calls := 0
	cfg := testutil.NewConfig(t, estimateHandler(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		var body struct {
			Type string         `json:"resourceType"`
			Info map[string]any `json:"resourceInfo"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		file := "compute/discovery_quote_server_s2.json"
		if body.Type == "volume" {
			file = "volume/discovery_quote_plain.json"
			if len(body.Info) != 5 || body.Info["size"] != float64(1) || body.Info["volumeTypeId"] != "vtype-1" || body.Info["period"] != float64(1) || body.Info["isPoc"] != false || body.Info["zoneId"] != "HCM03-1C" {
				t.Errorf("volume body %v", body.Info)
			}
		}
		testutil.WriteFixture(t, w, "../testdata/"+file)
	}))
	out, err := New(cfg).Estimate(estimateContext(), &EstimateInput{Servers: []EstimateServer{estimateServer()}, Volumes: []EstimateVolume{{Key: "disk", ServerKey: "web", ZoneID: "HCM03-1C", VolumeTypeID: "vtype-1", Size: 1, Quantity: 3}}})
	if err != nil || out == nil {
		t.Fatalf("output %v error %v", out, err)
	}
	if calls != 2 || *out.Items[1].MonthlyAmount != *out.Items[1].Price.MonthlyPrice*3 || *out.Total.KnownMonthlySubtotal != 695600+*out.Items[1].MonthlyAmount {
		t.Fatalf("accounting %+v", out)
	}
}

func TestEstimateRawPublicAndEncryptedVolumeFixtures(t *testing.T) {
	for _, tc := range []struct {
		name, file string
		volume     bool
	}{
		{"public", "pricing/estimate_quote_public_flagged.json", false},
		{"public-vm", "network/discovery_quote_public_vm.json", false},
		{"public-mkp", "network/discovery_quote_public_mkp.json", false},
		{"encrypted volume", "volume/discovery_quote_encrypted.json", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			quote := func(w http.ResponseWriter, r *http.Request) {
				calls++
				var body struct {
					Type, Action string
					Info         map[string]any `json:"resourceInfo"`
				}
				_ = json.NewDecoder(r.Body).Decode(&body)
				if tc.volume {
					if body.Info["encryptionType"] != "aes-xts-plain64_256" || len(body.Info) != 6 {
						t.Errorf("encrypted volume body %v", body.Info)
					}
				} else if len(body.Info) != 3 || body.Info["type"] != tc.name || body.Info["isPoc"] != false || body.Info["isBuyMorePo"] != false {
					t.Errorf("IP body %v", body.Info)
				}
				testutil.WriteFixture(t, w, "../testdata/"+tc.file)
			}
			in := &EstimateInput{PublicIPs: []EstimatePublicIP{{Key: "ip", Kind: "public-vip", Type: tc.name, Quantity: 2}}}
			var handler http.Handler = http.HandlerFunc(quote)
			if tc.volume {
				in = &EstimateInput{Volumes: []EstimateVolume{{Key: "v", ZoneID: "HCM03-1C", VolumeTypeID: "vtype-1", EncryptionTypeID: "aes-xts-plain64_256", Size: 1, Quantity: 3}}}
				handler = estimateHandler(t, quote)
			}
			out, err := New(testutil.NewConfig(t, handler)).Estimate(estimateContext(), in)
			if err != nil || out == nil {
				t.Fatalf("output %v error %v", out, err)
			}
			if calls != 1 || !out.Total.RequestedComplete || *out.Total.RequestedMonthlyTotal != *out.Items[0].MonthlyAmount {
				t.Fatal("incorrect raw quote accounting")
			}
		})
	}
}

func TestEstimateCatalogFailuresPropagateAndIndependentRowsContinue(t *testing.T) {
	calls := 0
	cfg := testutil.NewConfig(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if strings.HasSuffix(r.URL.Path, "/zones") {
			w.WriteHeader(500)
			_, _ = w.Write([]byte(`{}`))
			return
		}
		if r.URL.Path == "/v1/price" {
			_, _ = w.Write([]byte(`{"optimumPrice":10}`))
			return
		}
		t.Errorf("unexpected request %s", r.URL)
	}))
	server := estimateServer()
	second := server
	second.Key = "web2"
	in := &EstimateInput{AllowPartial: true, Servers: []EstimateServer{server, second}, PublicIPs: []EstimatePublicIP{{Key: "ip", Kind: "public-vip", Type: "public", Quantity: 1}}}
	out, err := New(cfg).Estimate(estimateContext(), in)
	var api *vngcloud.APIError
	if out == nil || !errors.Is(err, ErrDiscoveryIncomplete) || !errors.As(err, &api) || api.StatusCode != 500 || calls != 2 || len(out.Metadata.Failures) != 2 || out.Metadata.Failures[0].ItemKey != "web" || out.Metadata.Failures[1].ItemKey != "web2" || *out.Total.KnownMonthlySubtotal != 10 || out.Total.RequestedMonthlyTotal != nil {
		t.Fatalf("output %+v error %v calls %d", out, err, calls)
	}
}

func TestEstimateDeliberateImageAndZoneUnknowns(t *testing.T) {
	for _, tc := range []struct{ name, zone, image, status string }{
		{"disabled", "HCM03-1A", "img-5", "inapplicable"},
		{"licensed", "HCM03-1C", "img-3", "unknown"},
		{"not Ubuntu", "HCM03-1C", "img-1", "unknown"},
		{"unknown licence", "HCM03-1C", "img-9", "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := estimateServer()
			server.ZoneID, server.ImageID = tc.zone, tc.image
			cfg := testutil.NewConfig(t, estimateHandler(t, func(http.ResponseWriter, *http.Request) { t.Error("quoted unusable selector") }))
			out, err := New(cfg).Estimate(estimateContext(), &EstimateInput{Servers: []EstimateServer{server}})
			if err != nil || out == nil || out.Items[0].Price.Status != tc.status || !out.Metadata.Complete || out.Total.KnownMonthlySubtotal != nil {
				t.Fatalf("output %+v error %v", out, err)
			}
		})
	}
}

func TestEstimateRowLimitIncludesBreakdowns(t *testing.T) {
	lines := make([]map[string]any, 10000)
	for i := range lines {
		lines[i] = map[string]any{"name": "synthetic", "monthlyPrice": 1}
	}
	cfg := testutil.NewConfig(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"optimumPrice": 10000, "propertiesPrice": lines})
	}))
	out, err := New(cfg).Estimate(estimateContext(), &EstimateInput{PublicIPs: []EstimatePublicIP{{Key: "ip", Kind: "public-vip", Type: "public", Quantity: 1}}})
	if out != nil || !errors.Is(err, ErrEstimateLimit) {
		t.Fatalf("output %+v error %v", out, err)
	}
}

func TestEstimatePreflightIncludesAllCatalogs(t *testing.T) {
	quotes := 0
	cfg := testutil.NewConfig(t, estimateHandler(t, func(http.ResponseWriter, *http.Request) { quotes++ }))
	out, err := New(cfg).Estimate(estimateContext(), &EstimateInput{MaxRequests: 4, Servers: []EstimateServer{estimateServer()}})
	if out != nil || !errors.Is(err, ErrEstimateLimit) || quotes != 0 || !strings.Contains(err.Error(), "requires 5 requests") {
		t.Fatalf("output %v error %v quotes %d", out, err, quotes)
	}
}

func TestEstimateOptionalBreakdownAndMismatch(t *testing.T) {
	cfg := testutil.NewConfig(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"optimumPrice":10,"discountPercent":0,"propertiesPrice":[{"name":"known","monthlyPrice":3},{"name":"unknown","monthlyPrice":null}]}`))
	}))
	out, err := New(cfg).Estimate(estimateContext(), &EstimateInput{PublicIPs: []EstimatePublicIP{{Key: "ip", Kind: "public-vip", Type: "public", Quantity: 2}}})
	if err != nil || out == nil {
		t.Fatalf("output %v error %v", out, err)
	}
	if *out.Total.RequestedMonthlyTotal != 20 || out.Items[0].Price.OriginalPrice != nil || out.Items[0].Price.DiscountPercent == nil || *out.Items[0].Price.DiscountPercent != 0 || out.Items[0].Breakdown[1].MonthlyPrice != nil {
		t.Fatal("lost optional numeric presence")
	}
	found := false
	for _, n := range out.Metadata.Notes {
		found = found || n.Topic == "breakdown-mismatch" && n.Scope == "ip"
	}
	if !found {
		t.Fatal("missing mismatch note")
	}
}

func TestEstimateRetriesSharePacingAndLogicalBudget(t *testing.T) {
	for _, status := range []int{429, 500, 502, 503, 504} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			calls := 0
			cfg := testutil.NewRetryConfig(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls++
				w.WriteHeader(status)
				_, _ = w.Write([]byte(`{}`))
			}))
			now := time.Unix(1000, 0)
			waits := 0
			ctx := discovery.WithTestClock(context.Background(), func() time.Time { return now }, func(ctx context.Context, d time.Duration) error {
				if d < 500*time.Millisecond {
					t.Errorf("attempt spacing %v", d)
				}
				waits++
				now = now.Add(d)
				return ctx.Err()
			})
			_, err := New(cfg).Estimate(ctx, &EstimateInput{MaxRequests: 1, PublicIPs: []EstimatePublicIP{{Key: "ip", Kind: "public-vip", Type: "public", Quantity: 1}}})
			var api *vngcloud.APIError
			if !errors.As(err, &api) || api.StatusCode != status {
				t.Fatalf("error %v", err)
			}
			want := 4
			if status == 500 {
				want = 1
			}
			if calls != want || waits != want-1 {
				t.Fatalf("calls %d waits %d", calls, waits)
			}
		})
	}
}

func TestEstimateStorageEnvelopeRefusal(t *testing.T) {
	cfg := testutil.NewConfig(t, estimateStorageHandler(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"success":false,"code":112,"errorMsg":"untrusted refusal"}`))
	}))
	out, err := New(cfg).Estimate(estimateContext(), &EstimateInput{Storage: []EstimateStorage{{Key: "s", Type: "Gold", QuotaGB: 30, Quantity: 1}}})
	var api *vngcloud.APIError
	if out != nil || !errors.Is(err, vngcloud.ErrInvalidInput) || !errors.As(err, &api) || api.StatusCode != 200 || api.Code != "112" || strings.Contains(err.Error(), "untrusted") {
		t.Fatalf("output %v error %v code %v", out, err, api)
	}
}

func TestEstimateMixedServicesShareBudgetAndDeferQuotes(t *testing.T) {
	for _, max := range []int{14, 15} {
		t.Run(strconv.Itoa(max), func(t *testing.T) {
			quotes, configs := 0, 0
			storage := estimateStorageHandler(t, func(w http.ResponseWriter, _ *http.Request) {
				quotes++
				testutil.WriteFixture(t, w, "../testdata/storage/discovery_quote_gold.json")
			})
			regional := estimateHandler(t, func(w http.ResponseWriter, r *http.Request) {
				if configs != 4 {
					t.Error("quote before complete catalog validation")
				}
				quotes++
				var body struct {
					Type string `json:"resourceType"`
				}
				_ = json.NewDecoder(r.Body).Decode(&body)
				file := "pricing/estimate_quote_public_flagged.json"
				switch body.Type {
				case "server":
					file = "compute/discovery_quote_server_s2.json"
				case "volume":
					file = "volume/discovery_quote_plain.json"
				}
				testutil.WriteFixture(t, w, "../testdata/"+file)
			})
			cfg := testutil.NewConfig(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.Contains(r.URL.Path, "internal/") || strings.Contains(r.URL.Path, "billing-api/") {
					if strings.HasSuffix(r.URL.Path, "configurations") {
						configs++
					}
					storage(w, r)
					return
				}
				regional(w, r)
			}))
			in := &EstimateInput{MaxRequests: max, Servers: []EstimateServer{estimateServer()}, Volumes: []EstimateVolume{{Key: "disk", ZoneID: "HCM03-1C", VolumeTypeID: "vtype-1", Size: 1, Quantity: 3}}, PublicIPs: []EstimatePublicIP{{Key: "ip", Kind: "public-vip", Type: "public", Quantity: 2}}, Storage: []EstimateStorage{{Key: "objects", Type: "Gold", QuotaGB: 30, Quantity: 2}}}
			out, err := New(cfg).Estimate(estimateContext(), in)
			if max == 14 {
				if out != nil || !errors.Is(err, ErrEstimateLimit) || quotes != 0 || !strings.Contains(err.Error(), "requires 15 requests") {
					t.Fatalf("output %v error %v quotes %d", out, err, quotes)
				}
				return
			}
			if err != nil || out == nil || out.Metadata.Requests != 15 || quotes != 4 {
				t.Fatalf("output %+v error %v quotes %d", out, err, quotes)
			}
			for i, key := range []string{"web", "disk", "ip", "objects"} {
				if out.Items[i].Key != key {
					t.Fatal("changed input order")
				}
			}
		})
	}
}

func TestEstimateFailfastAndNoSurvivor(t *testing.T) {
	for _, partial := range []bool{false, true} {
		t.Run(strconv.FormatBool(partial), func(t *testing.T) {
			calls := 0
			cfg := testutil.NewConfig(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls++
				w.WriteHeader(500)
				_, _ = w.Write([]byte(`{}`))
			}))
			out, err := New(cfg).Estimate(estimateContext(), &EstimateInput{AllowPartial: partial, PublicIPs: []EstimatePublicIP{{Key: "a", Kind: "public-vip", Type: "public", Quantity: 1}, {Key: "b", Kind: "public-vip", Type: "public-vm", Quantity: 1}}})
			var api *vngcloud.APIError
			want := 1
			if partial {
				want = 2
			}
			if out != nil || !errors.As(err, &api) || api.StatusCode != 500 || errors.Is(err, ErrDiscoveryIncomplete) || calls != want {
				t.Fatalf("output %v error %v calls %d", out, err, calls)
			}
		})
	}
}
