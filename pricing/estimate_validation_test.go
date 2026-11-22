package pricing

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/testutil"
)

func TestEstimateLocalValidationBeforeRequests(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   *EstimateInput
	}{
		{"nil", nil}, {"empty", &EstimateInput{}},
		{"quantity omitted", &EstimateInput{PublicIPs: []EstimatePublicIP{{Key: "a", Kind: "reservation"}}}},
		{"quantity bound", &EstimateInput{PublicIPs: []EstimatePublicIP{{Key: "a", Kind: "reservation", Quantity: 1001}}}},
		{"key", &EstimateInput{PublicIPs: []EstimatePublicIP{{Key: "secret/value", Kind: "reservation", Quantity: 1}}}},
		{"kind", &EstimateInput{PublicIPs: []EstimatePublicIP{{Key: "a", Kind: "public", Quantity: 1}}}},
		{"type forbidden", &EstimateInput{PublicIPs: []EstimatePublicIP{{Key: "a", Kind: "reservation", Type: "public", Quantity: 1}}}},
		{"type required", &EstimateInput{PublicIPs: []EstimatePublicIP{{Key: "a", Kind: "public-vip", Quantity: 1}}}},
		{"request range", &EstimateInput{MaxRequests: 2001, Servers: []EstimateServer{estimateServer()}}},
		{"duplicate", &EstimateInput{Servers: []EstimateServer{estimateServer()}, PublicIPs: []EstimatePublicIP{{Key: "web", Kind: "reservation", Quantity: 1}}}},
		{"disk pair", &EstimateInput{Servers: []EstimateServer{{Key: "web", Quantity: 1, ZoneID: "z", FlavorID: "f", ImageID: "i", RootDiskSize: 20, RootDiskTypeID: "t", DataDiskSize: 1}}}},
		{"association", &EstimateInput{Volumes: []EstimateVolume{{Key: "v", Quantity: 1, ServerKey: "absent", ZoneID: "z", VolumeTypeID: "t", Size: 1}}}},
		{"quota", &EstimateInput{Storage: []EstimateStorage{{Key: "s", Quantity: 1, Type: "Gold", QuotaGB: 0}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := testutil.NewConfig(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("request before validation") }))
			out, err := New(cfg).Estimate(estimateContext(), tc.in)
			if out != nil || !errors.Is(err, vngcloud.ErrInvalidInput) || strings.Contains(err.Error(), "secret/value") {
				t.Fatalf("output %v error %v", out, err)
			}
		})
	}
}

func TestEstimateCatalogValidationBeforeAnyQuote(t *testing.T) {
	for _, partial := range []bool{false, true} {
		for _, field := range []string{"zone", "type", "image", "root size", "encryption"} {
			t.Run(field+strconv.FormatBool(partial), func(t *testing.T) {
				server := estimateServer()
				switch field {
				case "zone":
					server.ZoneID = "absent"
				case "type":
					server.RootDiskTypeID = "absent"
				case "image":
					server.ImageID = "absent"
				case "root size":
					server.RootDiskSize = 19
				case "encryption":
					server.RootDiskEncryptionTypeID = "absent"
				}
				cfg := testutil.NewConfig(t, estimateHandler(t, func(http.ResponseWriter, *http.Request) { t.Error("quote before complete validation") }))
				out, err := New(cfg).Estimate(estimateContext(), &EstimateInput{AllowPartial: partial, Servers: []EstimateServer{server}, PublicIPs: []EstimatePublicIP{{Key: "vip", Kind: "public-vip", Type: "public", Quantity: 1}}})
				if out != nil || !errors.Is(err, vngcloud.ErrInvalidInput) {
					t.Fatalf("output %v error %v", out, err)
				}
			})
		}
	}
}

func TestEstimateInputBoundsAndAssociations(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*EstimateInput)
	}{
		{"rows", func(in *EstimateInput) {
			for i := range 1001 {
				in.PublicIPs = append(in.PublicIPs, EstimatePublicIP{Key: strconv.Itoa(i), Kind: "reservation", Quantity: 1})
			}
		}},
		{"quantity sum", func(in *EstimateInput) {
			for i := range 11 {
				in.PublicIPs = append(in.PublicIPs, EstimatePublicIP{Key: strconv.Itoa(i), Kind: "reservation", Quantity: 1000})
			}
		}},
		{"path", func(in *EstimateInput) {
			s := estimateServer()
			s.FlavorID = "../orders"
			in.Servers = []EstimateServer{s}
		}},
		{"root", func(in *EstimateInput) { s := estimateServer(); s.RootDiskSize = 0; in.Servers = []EstimateServer{s} }},
		{"data encryption pair", func(in *EstimateInput) {
			s := estimateServer()
			s.DataDiskEncryptionTypeID = "aes_256"
			in.Servers = []EstimateServer{s}
		}},
		{"zone association", func(in *EstimateInput) {
			in.Servers = []EstimateServer{estimateServer()}
			in.Volumes = []EstimateVolume{{Key: "v", ServerKey: "web", ZoneID: "other", VolumeTypeID: "t", Size: 1, Quantity: 1}}
		}},
		{"encryption association", func(in *EstimateInput) {
			in.Servers = []EstimateServer{estimateServer()}
			in.Volumes = []EstimateVolume{{Key: "v", ServerKey: "web", ZoneID: "HCM03-1C", VolumeTypeID: "t", Size: 1, Quantity: 1, EncryptionTypeID: "aes_256"}}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := &EstimateInput{AllowPartial: true}
			tc.change(in)
			cfg := testutil.NewConfig(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("request before validation") }))
			out, err := New(cfg).Estimate(estimateContext(), in)
			if out != nil || !errors.Is(err, vngcloud.ErrInvalidInput) {
				t.Fatalf("output %v error %v", out, err)
			}
		})
	}
}

func TestEstimateStorageCatalogDependentInvalidAborts(t *testing.T) {
	for _, partial := range []bool{false, true} {
		for _, tc := range []struct {
			name, typ string
			quota     int64
		}{{"unknown", "absent", 30}, {"minimum", "Gold", 29}, {"maximum", "Gold", 1 << 62}} {
			t.Run(tc.name+strconv.FormatBool(partial), func(t *testing.T) {
				cfg := testutil.NewConfig(t, estimateStorageHandler(t, func(http.ResponseWriter, *http.Request) { t.Error("quoted invalid storage") }))
				out, err := New(cfg).Estimate(estimateContext(), &EstimateInput{AllowPartial: partial, Storage: []EstimateStorage{{Key: "storage", Type: tc.typ, QuotaGB: tc.quota, Quantity: 1}}})
				if out != nil || !errors.Is(err, vngcloud.ErrInvalidInput) {
					t.Fatalf("output %v error %v", out, err)
				}
			})
		}
	}
}
