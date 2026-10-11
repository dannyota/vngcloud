package cli

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/pricing"
	"danny.vn/vngcloud/storage"
	"danny.vn/vngcloud/volume"
)

func TestDiscoverySortUnknownLastAndUnrounded(t *testing.T) {
	for _, descending := range []bool{false, true} {
		a, b := 1.001, 1.002
		out := &volume.ListVolumePricesOutput{Items: []volume.VolumePrice{{VolumeType: volume.VolumeType{Name: "unknown"}}, {VolumeType: volume.VolumeType{Name: "b"}, Price: pricing.DiscoveryPrice{MonthlyPrice: &b}}, {VolumeType: volume.VolumeType{Name: "a"}, Price: pricing.DiscoveryPrice{MonthlyPrice: &a}}}}
		sortDiscovery(out, "monthly-price", descending)
		if out.Items[2].VolumeType.Name != "unknown" {
			t.Fatal("unknown did not sort last")
		}
		want := "a"
		if descending {
			want = "b"
		}
		if out.Items[0].VolumeType.Name != want {
			t.Fatal("sort rounded values")
		}
	}
}

func TestDiscoveryRenderUnknownAndNotes(t *testing.T) {
	out := &volume.ListVolumePricesOutput{Items: []volume.VolumePrice{{VolumeType: volume.VolumeType{ID: "type-1"}, Price: pricing.DiscoveryPrice{Status: "unknown"}}}, Metadata: pricing.DiscoveryMetadata{Notes: []pricing.PricingNote{{Topic: "units", Text: "effective rate"}}}}
	for _, format := range []string{"text", "table"} {
		var stdout, stderr bytes.Buffer
		if err := renderDiscovery(&stdout, &stderr, format, "", out); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(stdout.String(), "unknown") || !strings.Contains(stderr.String(), "effective rate") || !strings.Contains(stderr.String(), "incomplete") {
			t.Fatalf("stdout %s stderr %s", &stdout, &stderr)
		}
		if format == "text" && !strings.Contains(stdout.String(), "\t") {
			t.Fatal("missing tabs")
		}
	}
}

func TestDiscoveryIncompleteExitPrecedence(t *testing.T) {
	for _, cause := range []error{vngcloud.ErrAuth, vngcloud.ErrNotFound, vngcloud.ErrInvalidInput} {
		err := &pricing.DiscoveryError{Errors: []error{cause}}
		if !errors.Is(err, cause) || exitCode(err) != 1 {
			t.Fatalf("exit %d", exitCode(err))
		}
	}
}

func TestGoldenDiscovery(t *testing.T) {
	amount, rate := 3200.0, 3200.0
	volumeOut := &volume.ListVolumePricesOutput{Items: []volume.VolumePrice{{VolumeType: volume.VolumeType{ID: "type-1", Name: "SSD", IOPS: 3000, MinSize: 1, MaxSize: 5000}, ZoneID: "zone-1", ZoneName: "Zone", ZoneEnabled: vngcloud.Ptr(true), SizeGB: 1, PricePerGB: &rate, Price: pricing.DiscoveryPrice{Status: "quoted", Basis: "monthly-total", Currency: "VND", Source: "pricing.GetQuote", MonthlyPrice: &amount}}}, Metadata: pricing.DiscoveryMetadata{Region: "hcm-3", Complete: true, Requests: 5, QuotedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), Failures: []pricing.DiscoveryFailure{}, Notes: []pricing.PricingNote{}}}
	storageOut := &storage.ListStoragePricesOutput{Items: []storage.StoragePrice{{Region: "HCM04", Type: "Gold", TypeID: 1, PurchaseType: "Pay monthly", PurchaseTypeID: 4, QuotaGB: 30, MinQuotaGB: 30, MaxQuotaGB: 10000, AllowPeriod: []int{1}, Price: pricing.DiscoveryPrice{Status: "unknown", Source: "storage.QuoteCreateProject"}}}, Metadata: volumeOut.Metadata}
	partial := *volumeOut
	partial.Items = append([]volume.VolumePrice{}, volumeOut.Items...)
	partial.Items[0].Price.Status = "failed"
	partial.Items[0].Price.MonthlyPrice = nil
	partial.Items[0].PricePerGB = nil
	partial.Metadata.Complete = false
	partial.Metadata.Failures = []pricing.DiscoveryFailure{{Operation: "pricing.GetQuote", ItemKey: "zone-1/type-1", Code: "InternalServerError", Status: 500}}
	for _, tc := range []struct {
		name string
		out  any
	}{{"volume-list-volume-prices", volumeOut}, {"storage-list-storage-prices", storageOut}, {"discovery-partial", &partial}} {
		for _, format := range []string{"json", "table", "text"} {
			t.Run(tc.name+"/"+format, func(t *testing.T) {
				var stdout, stderr bytes.Buffer
				if err := renderDiscovery(&stdout, &stderr, format, "", tc.out); err != nil {
					t.Fatal(err)
				}
				path := goldenPath(tc.name + "." + format + ".golden")
				if *updateGolden {
					if err := os.WriteFile(path, stdout.Bytes(), 0o644); err != nil {
						t.Fatal(err)
					}
					return
				}
				want, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(stdout.Bytes(), want) {
					t.Fatalf("golden mismatch %s: %s", path, &stdout)
				}
			})
		}
	}
}

func TestDiscoverySortBeforeQueryAndRenderFailure(t *testing.T) {
	high, low := 1.002, 1.001
	out := &volume.ListVolumePricesOutput{Items: []volume.VolumePrice{{VolumeType: volume.VolumeType{Name: "high"}, Price: pricing.DiscoveryPrice{MonthlyPrice: &high}}, {VolumeType: volume.VolumeType{Name: "low"}, Price: pricing.DiscoveryPrice{MonthlyPrice: &low}}}, Metadata: pricing.DiscoveryMetadata{Complete: true}}
	sortDiscovery(out, "monthly-price", false)
	var stdout, stderr bytes.Buffer
	if err := renderDiscovery(&stdout, &stderr, "json", "Items[0].VolumeType.Name", out); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(stdout.String()) != `"low"` {
		t.Fatalf("query output %s", &stdout)
	}
	err := renderDiscovery(discoveryFailWriter{}, &stderr, "json", "", out)
	if err == nil || exitCode(err) != 1 {
		t.Fatalf("render error %v", err)
	}
}

type discoveryFailWriter struct{}

func (discoveryFailWriter) Write([]byte) (int, error) { return 0, errors.New("writer failed") }
