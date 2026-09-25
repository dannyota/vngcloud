package dns

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/testutil"
)

func TestDNSListHostedZones(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/dns/hosted-zone" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("name") != "example" {
			t.Fatalf("unexpected query: %s", r.URL.RawQuery)
		}
		testutil.WriteFixture(t, w, "../testdata/dns/list_hosted_zones.json")
	}))

	out, err := c.ListHostedZones(context.Background(), &ListHostedZonesInput{Name: "example"})
	if err != nil {
		t.Fatalf("ListHostedZones() error = %v", err)
	}
	if len(out.Items) != 1 || out.Items[0].ID != "zone-1" || out.Items[0].AssociatedVPCIDs[0] != "vpc-1" {
		t.Fatalf("unexpected zones: %+v", out)
	}
}

func TestDNSGetHostedZone(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/dns/hosted-zone/zone-1" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		testutil.WriteFixture(t, w, "../testdata/dns/get_hosted_zone.json")
	}))

	out, err := c.GetHostedZone(context.Background(), &GetHostedZoneInput{HostedZoneID: "zone-1"})
	if err != nil {
		t.Fatalf("GetHostedZone() error = %v", err)
	}
	if out.HostedZone.ID != "zone-1" || out.HostedZone.DomainName == "" {
		t.Fatalf("unexpected zone: %+v", out.HostedZone)
	}
}

func TestDNSListRecords(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/dns/hosted-zone/zone-1/record" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("name") != "www" {
			t.Fatalf("unexpected query: %s", r.URL.RawQuery)
		}
		testutil.WriteFixture(t, w, "../testdata/dns/list_records.json")
	}))

	out, err := c.ListRecords(context.Background(), &ListRecordsInput{HostedZoneID: "zone-1", Name: "www"})
	if err != nil {
		t.Fatalf("ListRecords() error = %v", err)
	}
	if len(out.Items) != 1 || out.Items[0].ID != "record-1" || out.Items[0].Value[0].Value != "<ip>" {
		t.Fatalf("unexpected records: %+v", out)
	}
}

func TestDNSGetRecord(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/dns/hosted-zone/zone-1/record/record-1" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		testutil.WriteFixture(t, w, "../testdata/dns/get_record.json")
	}))

	out, err := c.GetRecord(context.Background(), &GetRecordInput{HostedZoneID: "zone-1", RecordID: "record-1"})
	if err != nil {
		t.Fatalf("GetRecord() error = %v", err)
	}
	if out.Record.ID != "record-1" || out.Record.HostedZoneID != "zone-1" {
		t.Fatalf("unexpected record: %+v", out.Record)
	}
}

func TestDNSRequiredInput(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("no request expected")
	}))
	_, err := c.GetRecord(context.Background(), &GetRecordInput{HostedZoneID: "zone-1"})
	if !errors.Is(err, vngcloud.ErrInvalidInput) || !strings.Contains(err.Error(), "RecordID") {
		t.Fatalf("err = %v", err)
	}
	if _, err := c.GetHostedZone(context.Background(), nil); !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("nil input err = %v", err)
	}
}

func TestDNSOperationName(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	_, err := c.GetHostedZone(context.Background(), &GetHostedZoneInput{HostedZoneID: "zone-1"})
	var apiErr *vngcloud.APIError
	if !errors.As(err, &apiErr) || apiErr.Operation != "dns.GetHostedZone" {
		t.Fatalf("err = %v", err)
	}
}

func newTestClient(t *testing.T, handler http.Handler) *Client {
	t.Helper()

	return New(testutil.NewConfig(t, handler))
}
