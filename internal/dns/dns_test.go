package dns

import (
	"context"
	"net/http"
	"testing"

	"danny.vn/vngcloud/internal/testutil"
)

func TestDNSListHostedZones(t *testing.T) {
	service := newTestService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/dns/hosted-zone" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("name") != "example" {
			t.Fatalf("unexpected query: %s", r.URL.RawQuery)
		}
		testutil.WriteFixture(t, w, "../../testdata/dns/list_hosted_zones.json")
	}))

	zones, err := service.ListHostedZones(context.Background(), &ListHostedZonesOptions{Name: "example"})
	if err != nil {
		t.Fatalf("ListHostedZones() error = %v", err)
	}
	if len(zones.Items) != 1 || zones.Items[0].ID != "zone-1" || zones.Items[0].AssociatedVPCIDs[0] != "vpc-1" {
		t.Fatalf("unexpected zones: %+v", zones)
	}
}

func TestDNSGetHostedZone(t *testing.T) {
	service := newTestService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/dns/hosted-zone/zone-1" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		testutil.WriteFixture(t, w, "../../testdata/dns/get_hosted_zone.json")
	}))

	zone, err := service.GetHostedZone(context.Background(), "zone-1")
	if err != nil {
		t.Fatalf("GetHostedZone() error = %v", err)
	}
	if zone.ID != "zone-1" || zone.DomainName == "" {
		t.Fatalf("unexpected zone: %+v", zone)
	}
}

func TestDNSListRecords(t *testing.T) {
	service := newTestService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/dns/hosted-zone/zone-1/record" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("name") != "www" {
			t.Fatalf("unexpected query: %s", r.URL.RawQuery)
		}
		testutil.WriteFixture(t, w, "../../testdata/dns/list_records.json")
	}))

	records, err := service.ListRecords(context.Background(), "zone-1", &ListRecordsOptions{Name: "www"})
	if err != nil {
		t.Fatalf("ListRecords() error = %v", err)
	}
	if len(records.Items) != 1 || records.Items[0].ID != "record-1" || records.Items[0].Value[0].Value != "<ip>" {
		t.Fatalf("unexpected records: %+v", records)
	}
}

func TestDNSGetRecord(t *testing.T) {
	service := newTestService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/dns/hosted-zone/zone-1/record/record-1" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		testutil.WriteFixture(t, w, "../../testdata/dns/get_record.json")
	}))

	record, err := service.GetRecord(context.Background(), "zone-1", "record-1")
	if err != nil {
		t.Fatalf("GetRecord() error = %v", err)
	}
	if record.ID != "record-1" || record.HostedZoneID != "zone-1" {
		t.Fatalf("unexpected record: %+v", record)
	}
}

func newTestService(t *testing.T, handler http.Handler) *Service {
	t.Helper()

	client := testutil.NewCoreClient(t, handler)
	return New(client)
}
