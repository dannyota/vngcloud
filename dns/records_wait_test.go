package dns

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/testutil"
)

// scriptedZoneAndRecordGets serves zoneBodies to successive GET requests on
// the zone path and recordBodies to successive GET requests on the record
// path, each list clamping at its last entry once exhausted, and delegates
// every other method to other. The two sequences are scripted
// independently, since settleRecord reads the zone and the record in every
// poll iteration and each needs its own status progression.
func scriptedZoneAndRecordGets(t *testing.T, zoneBodies, recordBodies []string, other http.HandlerFunc) http.Handler {
	t.Helper()
	var zn, rn atomic.Int64
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			if other == nil {
				t.Fatalf("unexpected %s request", r.Method)
			}
			other(w, r)
			return
		}
		bodies, n := zoneBodies, &zn
		if strings.Contains(r.URL.Path, "/record/") {
			bodies, n = recordBodies, &rn
		}
		if len(bodies) == 0 {
			t.Fatalf("unexpected GET %s: no scripted body for this path", r.URL.Path)
		}
		i := int(n.Add(1)) - 1
		if i >= len(bodies) {
			i = len(bodies) - 1
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(bodies[i]))
	})
}

// --- CreateRecord's pre-write wait ---

func TestCreateRecordPreWriteWaitThroughBusyToActive(t *testing.T) {
	var postCalls atomic.Int64
	client := withInstantSleep(newTestClient(t, scriptedZoneAndRecordGets(t, []string{
		zoneBody(StatusCreating, "d", []string{"vpc-1"}),
		zoneBody(StatusCreating, "d", []string{"vpc-1"}),
		zoneBody(StatusActive, "d", []string{"vpc-1"}),
	}, nil, func(w http.ResponseWriter, r *http.Request) {
		postCalls.Add(1)
		testutil.WriteFixture(t, w, "../testdata/dns/create_record.json")
	})))

	_, err := client.CreateRecord(context.Background(), &CreateRecordInput{
		HostedZoneID: "zone-1",
		Type:         "A",
		Values:       []RecordValue{{Value: "<ip>"}},
		NoWait:       true,
	})
	if err != nil {
		t.Fatalf("CreateRecord() error = %v", err)
	}
	if postCalls.Load() != 1 {
		t.Fatalf("POST calls = %d, want 1", postCalls.Load())
	}
}

func TestCreateRecordErrZoneBusyNoWriteSent(t *testing.T) {
	client := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("unexpected %s request: the pre-write wait never settled, nothing should be sent", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(zoneBody(StatusUpdating, "d", []string{"vpc-1"})))
	})))

	_, err := client.CreateRecord(context.Background(), &CreateRecordInput{
		HostedZoneID: "zone-1",
		Type:         "A",
		Values:       []RecordValue{{Value: "<ip>"}},
	})
	if !errors.Is(err, ErrZoneBusy) {
		t.Fatalf("err = %v, want ErrZoneBusy", err)
	}
}

// --- CreateRecord's post-write wait ---

func TestCreateRecordSettlesToActive(t *testing.T) {
	client := withInstantSleep(newTestClient(t, scriptedZoneAndRecordGets(t,
		[]string{zoneBody(StatusActive, "d", []string{"vpc-1"})},
		[]string{
			recordBody(Record{ID: "record-1", HostedZoneID: "zone-1", Status: StatusCreating}),
			recordBody(Record{ID: "record-1", HostedZoneID: "zone-1", Status: StatusActive}),
		},
		func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				t.Fatalf("unexpected method %s", r.Method)
			}
			testutil.WriteFixture(t, w, "../testdata/dns/create_record.json")
		},
	)))

	out, err := client.CreateRecord(context.Background(), &CreateRecordInput{
		HostedZoneID: "zone-1",
		Type:         "TXT",
		Values:       []RecordValue{{Value: "<secret>"}, {Value: "<secret>"}},
	})
	if err != nil {
		t.Fatalf("CreateRecord() error = %v", err)
	}
	if out.Record.Status != StatusActive {
		t.Fatalf("Status = %q, want %q", out.Record.Status, StatusActive)
	}
}

func TestCreateRecordErrFailedOnRecordError(t *testing.T) {
	client := withInstantSleep(newTestClient(t, scriptedZoneAndRecordGets(t,
		[]string{zoneBody(StatusActive, "d", []string{"vpc-1"})},
		[]string{
			recordBody(Record{ID: "record-1", HostedZoneID: "zone-1", Status: StatusCreating}),
			recordBody(Record{ID: "record-1", HostedZoneID: "zone-1", Status: StatusError}),
		},
		func(w http.ResponseWriter, r *http.Request) {
			testutil.WriteFixture(t, w, "../testdata/dns/create_record.json")
		},
	)))

	out, err := client.CreateRecord(context.Background(), &CreateRecordInput{
		HostedZoneID: "zone-1",
		Type:         "TXT",
		Values:       []RecordValue{{Value: "<secret>"}},
	})
	if !errors.Is(err, ErrFailed) {
		t.Fatalf("err = %v, want ErrFailed", err)
	}
	if out == nil || out.Record.Status != StatusError {
		t.Fatalf("out = %+v, want a non-nil Output holding the ERROR record", out)
	}
}

func TestCreateRecordErrFailedOnZoneError(t *testing.T) {
	client := withInstantSleep(newTestClient(t, scriptedZoneAndRecordGets(t,
		[]string{
			zoneBody(StatusActive, "d", []string{"vpc-1"}),
			zoneBody(StatusError, "d", []string{"vpc-1"}),
		},
		[]string{recordBody(Record{ID: "record-1", HostedZoneID: "zone-1", Status: StatusCreating})},
		func(w http.ResponseWriter, r *http.Request) {
			testutil.WriteFixture(t, w, "../testdata/dns/create_record.json")
		},
	)))

	out, err := client.CreateRecord(context.Background(), &CreateRecordInput{
		HostedZoneID: "zone-1",
		Type:         "TXT",
		Values:       []RecordValue{{Value: "<secret>"}},
	})
	if !errors.Is(err, ErrFailed) {
		t.Fatalf("err = %v, want ErrFailed", err)
	}
	if out == nil {
		t.Fatal("out is nil, want a non-nil Output")
	}
}

func TestCreateRecordErrNotSettled(t *testing.T) {
	client := withInstantSleep(newTestClient(t, scriptedZoneAndRecordGets(t,
		[]string{zoneBody(StatusActive, "d", []string{"vpc-1"})},
		[]string{recordBody(Record{ID: "record-1", HostedZoneID: "zone-1", Status: StatusCreating})},
		func(w http.ResponseWriter, r *http.Request) {
			testutil.WriteFixture(t, w, "../testdata/dns/create_record.json")
		},
	)))

	out, err := client.CreateRecord(context.Background(), &CreateRecordInput{
		HostedZoneID: "zone-1",
		Type:         "TXT",
		Values:       []RecordValue{{Value: "<secret>"}},
	})
	if !errors.Is(err, ErrNotSettled) {
		t.Fatalf("err = %v, want ErrNotSettled", err)
	}
	if out == nil || out.Record.Status != StatusCreating {
		t.Fatalf("out = %+v, want a non-nil Output holding the last CREATING read", out)
	}
}

// TestCreateRecordSettleReadFailureFallsBackToCreateResponse checks that a
// read failure in the post-write settle, such as this 500, wraps
// ErrNotSettled (the read never confirmed or denied the write) and that the
// Output falls back to the record the create response itself carried,
// since no read after the create ever came back to replace it.
func TestCreateRecordSettleReadFailureFallsBackToCreateResponse(t *testing.T) {
	posted := false
	client := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			if !posted {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(zoneBody(StatusActive, "d", []string{"vpc-1"})))
				return
			}
			// The settle's first read after the create fails outright,
			// rather than returning a decodable status.
			w.WriteHeader(http.StatusInternalServerError)
		case http.MethodPost:
			posted = true
			testutil.WriteFixture(t, w, "../testdata/dns/create_record.json")
		default:
			t.Fatalf("unexpected method %s", r.Method)
		}
	})))

	out, err := client.CreateRecord(context.Background(), &CreateRecordInput{
		HostedZoneID: "zone-1",
		Type:         "TXT",
		Values:       []RecordValue{{Value: "<secret>"}},
	})
	if !errors.Is(err, ErrNotSettled) {
		t.Fatalf("err = %v, want ErrNotSettled", err)
	}
	if out == nil || out.Record.ID != "record-1" {
		t.Fatalf("out = %+v, want the create response's own record as fallback", out)
	}
}

// --- UpdateRecord's pre- and post-write waits ---

func TestUpdateRecordPreWriteWaitThroughBusyToActive(t *testing.T) {
	var putCalls atomic.Int64
	client := withInstantSleep(newTestClient(t, scriptedZoneAndRecordGets(t, []string{
		zoneBody(StatusUpdating, "d", []string{"vpc-1"}),
		zoneBody(StatusUpdating, "d", []string{"vpc-1"}),
		zoneBody(StatusActive, "d", []string{"vpc-1"}),
	}, []string{
		// NoWait's own single confirm read after the PUT.
		recordBody(Record{ID: "record-1", HostedZoneID: "zone-1", Status: StatusActive, TTL: 60}),
	}, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Fatalf("unexpected method %s", r.Method)
		}
		putCalls.Add(1)
		w.WriteHeader(http.StatusNoContent)
	})))

	_, err := client.UpdateRecord(context.Background(), &UpdateRecordInput{
		HostedZoneID: "zone-1",
		RecordID:     "record-1",
		TTL:          vngcloud.Ptr(60),
		NoWait:       true,
	})
	if err != nil {
		t.Fatalf("UpdateRecord() error = %v", err)
	}
	if putCalls.Load() != 1 {
		t.Fatalf("PUT calls = %d, want 1", putCalls.Load())
	}
}

func TestUpdateRecordErrZoneBusyNoWriteSent(t *testing.T) {
	client := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("unexpected %s request: the pre-write wait never settled, nothing should be sent", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(zoneBody(StatusCreating, "d", []string{"vpc-1"})))
	})))

	_, err := client.UpdateRecord(context.Background(), &UpdateRecordInput{
		HostedZoneID: "zone-1",
		RecordID:     "record-1",
		TTL:          vngcloud.Ptr(60),
	})
	if !errors.Is(err, ErrZoneBusy) {
		t.Fatalf("err = %v, want ErrZoneBusy", err)
	}
}

// TestUpdateRecordSettledMatchesFullSubDomainName checks that the post-write
// settle compares a sent SubDomain against the zone's domain name, since the
// server always returns SubDomain as the full name: a settle that compared
// the raw "www" against a read of "www.app.internal" would never match and
// this test would time out into ErrNotSettled instead of returning nil.
func TestUpdateRecordSettledMatchesFullSubDomainName(t *testing.T) {
	zoneJSON := `{"data":{"hostedZoneId":"zone-1","domainName":"app.internal","status":"ACTIVE","type":"PRIVATE","assocVpcIds":["vpc-1"]}}`
	client := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/dns/hosted-zone/zone-1":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(zoneJSON))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/dns/hosted-zone/zone-1/record/record-1":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(recordBody(Record{ID: "record-1", HostedZoneID: "zone-1", Status: StatusActive, SubDomain: "www.app.internal"})))
		case r.Method == http.MethodPut:
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
	})))

	out, err := client.UpdateRecord(context.Background(), &UpdateRecordInput{
		HostedZoneID: "zone-1",
		RecordID:     "record-1",
		SubDomain:    vngcloud.Ptr("www"),
	})
	if err != nil {
		t.Fatalf("UpdateRecord() error = %v", err)
	}
	if out.Record.SubDomain != "www.app.internal" {
		t.Fatalf("SubDomain = %q, want %q", out.Record.SubDomain, "www.app.internal")
	}
}

// TestUpdateRecordSettledSubDomainCaseInsensitive checks that the post-write
// settle compares a sent SubDomain case-insensitively against the zone's
// full name, since the server lowercases subDomain in what it stores: a
// settle that compared the sent "Mail" against a read of
// "mail.app.internal" byte for byte would never match and this test would
// time out into ErrNotSettled instead of returning nil.
func TestUpdateRecordSettledSubDomainCaseInsensitive(t *testing.T) {
	zoneJSON := `{"data":{"hostedZoneId":"zone-1","domainName":"app.internal","status":"ACTIVE","type":"PRIVATE","assocVpcIds":["vpc-1"]}}`
	client := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/dns/hosted-zone/zone-1":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(zoneJSON))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/dns/hosted-zone/zone-1/record/record-1":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(recordBody(Record{ID: "record-1", HostedZoneID: "zone-1", Status: StatusActive, SubDomain: "mail.app.internal"})))
		case r.Method == http.MethodPut:
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
	})))

	out, err := client.UpdateRecord(context.Background(), &UpdateRecordInput{
		HostedZoneID: "zone-1",
		RecordID:     "record-1",
		SubDomain:    vngcloud.Ptr("Mail"),
	})
	if err != nil {
		t.Fatalf("UpdateRecord() error = %v", err)
	}
	if out.Record.SubDomain != "mail.app.internal" {
		t.Fatalf("SubDomain = %q, want %q", out.Record.SubDomain, "mail.app.internal")
	}
}

// TestUpdateRecordApexSettle checks that an update setting SubDomain to ""
// settles against a record whose SubDomain the server returns as the
// zone's own DomainName, the apex's full name.
func TestUpdateRecordApexSettle(t *testing.T) {
	zoneJSON := `{"data":{"hostedZoneId":"zone-1","domainName":"app.internal","status":"ACTIVE","type":"PRIVATE","assocVpcIds":["vpc-1"]}}`
	client := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/dns/hosted-zone/zone-1":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(zoneJSON))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/dns/hosted-zone/zone-1/record/record-1":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(recordBody(Record{ID: "record-1", HostedZoneID: "zone-1", Status: StatusActive, SubDomain: "app.internal"})))
		case r.Method == http.MethodPut:
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
	})))

	out, err := client.UpdateRecord(context.Background(), &UpdateRecordInput{
		HostedZoneID: "zone-1",
		RecordID:     "record-1",
		SubDomain:    vngcloud.Ptr(""),
	})
	if err != nil {
		t.Fatalf("UpdateRecord() error = %v", err)
	}
	if out.Record.SubDomain != "app.internal" {
		t.Fatalf("SubDomain = %q, want %q", out.Record.SubDomain, "app.internal")
	}
}

// TestUpdateRecordSettleRequiresPollAfterZoneLock checks that the update
// settle does not report settled on its very first poll, even when the
// server already shows the zone ACTIVE and the record matching the sent
// fields: the server always takes the zone lock on a record update, even a
// no-op, so a settle that trusted the very first read could pass before the
// lock ever appeared. The mock always returns the matching ACTIVE state, so
// a settle without this guard would need only one poll; this one needs at
// least two.
func TestUpdateRecordSettleRequiresPollAfterZoneLock(t *testing.T) {
	var recordGets atomic.Int64
	client := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/dns/hosted-zone/zone-1":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(zoneBody(StatusActive, "d", []string{"vpc-1"})))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/dns/hosted-zone/zone-1/record/record-1":
			recordGets.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(recordBody(Record{ID: "record-1", HostedZoneID: "zone-1", Status: StatusActive, TTL: 60})))
		case r.Method == http.MethodPut:
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
	})))

	out, err := client.UpdateRecord(context.Background(), &UpdateRecordInput{
		HostedZoneID: "zone-1",
		RecordID:     "record-1",
		TTL:          vngcloud.Ptr(60),
	})
	if err != nil {
		t.Fatalf("UpdateRecord() error = %v", err)
	}
	if out.Record.TTL != 60 {
		t.Fatalf("TTL = %d, want 60", out.Record.TTL)
	}
	if recordGets.Load() < 2 {
		t.Fatalf("record GET calls = %d, want at least 2: settle must not trust the very first poll", recordGets.Load())
	}
}

// TestUpdateRecordSettlePollsThroughBusyZoneWhileRecordActive checks that
// the settle does not report settled while the zone is UPDATING, even
// though the record itself already reads ACTIVE with the sent fields: the
// zone lock a record update takes matters, not just the record's own
// status, so the settle must keep polling until the zone itself reads
// ACTIVE too.
func TestUpdateRecordSettlePollsThroughBusyZoneWhileRecordActive(t *testing.T) {
	client := withInstantSleep(newTestClient(t, scriptedZoneAndRecordGets(t,
		[]string{
			zoneBody(StatusUpdating, "d", []string{"vpc-1"}),
			zoneBody(StatusUpdating, "d", []string{"vpc-1"}),
			zoneBody(StatusActive, "d", []string{"vpc-1"}),
		},
		[]string{recordBody(Record{ID: "record-1", HostedZoneID: "zone-1", Status: StatusActive, TTL: 60})},
		func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		},
	)))

	out, err := client.UpdateRecord(context.Background(), &UpdateRecordInput{
		HostedZoneID: "zone-1",
		RecordID:     "record-1",
		TTL:          vngcloud.Ptr(60),
	})
	if err != nil {
		t.Fatalf("UpdateRecord() error = %v", err)
	}
	if out.Record.TTL != 60 {
		t.Fatalf("TTL = %d, want 60", out.Record.TTL)
	}
}

func TestUpdateRecordPostWriteErrFailed(t *testing.T) {
	client := withInstantSleep(newTestClient(t, scriptedZoneAndRecordGets(t,
		[]string{zoneBody(StatusActive, "d", []string{"vpc-1"})},
		[]string{
			recordBody(Record{ID: "record-1", HostedZoneID: "zone-1", Status: StatusActive, TTL: 300}),
			recordBody(Record{ID: "record-1", HostedZoneID: "zone-1", Status: StatusError, TTL: 300}),
		},
		func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		},
	)))

	out, err := client.UpdateRecord(context.Background(), &UpdateRecordInput{
		HostedZoneID: "zone-1",
		RecordID:     "record-1",
		TTL:          vngcloud.Ptr(60),
	})
	if !errors.Is(err, ErrFailed) {
		t.Fatalf("err = %v, want ErrFailed", err)
	}
	if out == nil || out.Record.Status != StatusError {
		t.Fatalf("out = %+v, want a non-nil Output holding the ERROR record", out)
	}
}

func TestUpdateRecordPostWriteErrNotSettled(t *testing.T) {
	client := withInstantSleep(newTestClient(t, scriptedZoneAndRecordGets(t,
		[]string{zoneBody(StatusActive, "d", []string{"vpc-1"})},
		// The TTL never changes: the sent field never appears, so the wait
		// can only time out.
		[]string{recordBody(Record{ID: "record-1", HostedZoneID: "zone-1", Status: StatusActive, TTL: 300})},
		func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		},
	)))

	out, err := client.UpdateRecord(context.Background(), &UpdateRecordInput{
		HostedZoneID: "zone-1",
		RecordID:     "record-1",
		TTL:          vngcloud.Ptr(60),
	})
	if !errors.Is(err, ErrNotSettled) {
		t.Fatalf("err = %v, want ErrNotSettled", err)
	}
	if out == nil {
		t.Fatal("out is nil, want a non-nil Output holding the last read")
	}
}

// --- DeleteRecord's pre- and post-write waits ---

func TestDeleteRecordPreWriteWaitThroughBusyToActive(t *testing.T) {
	var deleteCalls atomic.Int64
	client := withInstantSleep(newTestClient(t, scriptedGets(t, []string{
		zoneBody(StatusCreating, "d", []string{"vpc-1"}),
		zoneBody(StatusActive, "d", []string{"vpc-1"}),
	}, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Fatalf("unexpected method %s", r.Method)
		}
		deleteCalls.Add(1)
		w.WriteHeader(http.StatusNoContent)
	})))

	if _, err := client.DeleteRecord(context.Background(), &DeleteRecordInput{HostedZoneID: "zone-1", RecordID: "record-1", NoWait: true}); err != nil {
		t.Fatalf("DeleteRecord() error = %v", err)
	}
	if deleteCalls.Load() != 1 {
		t.Fatalf("DELETE calls = %d, want 1", deleteCalls.Load())
	}
}

func TestDeleteRecordErrZoneBusyNoWriteSent(t *testing.T) {
	client := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("unexpected %s request: the pre-write wait never settled, nothing should be sent", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(zoneBody(StatusCreating, "d", []string{"vpc-1"})))
	})))

	_, err := client.DeleteRecord(context.Background(), &DeleteRecordInput{HostedZoneID: "zone-1", RecordID: "record-1"})
	if !errors.Is(err, ErrZoneBusy) {
		t.Fatalf("err = %v, want ErrZoneBusy", err)
	}
}

func TestDeleteRecordSettlesOnNotFound(t *testing.T) {
	getN := 0
	client := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			if r.URL.Path == "/v1/dns/hosted-zone/zone-1" {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(zoneBody(StatusActive, "d", []string{"vpc-1"})))
				return
			}
			getN++
			if getN <= 2 {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(recordBody(Record{ID: "record-1", HostedZoneID: "zone-1", Status: StatusActive})))
				return
			}
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"not found"}`))
		case http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected method %s", r.Method)
		}
	})))

	if _, err := client.DeleteRecord(context.Background(), &DeleteRecordInput{HostedZoneID: "zone-1", RecordID: "record-1"}); err != nil {
		t.Fatalf("DeleteRecord() error = %v", err)
	}
}

func TestDeleteRecordErrNotSettled(t *testing.T) {
	client := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			if r.URL.Path == "/v1/dns/hosted-zone/zone-1" {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(zoneBody(StatusActive, "d", []string{"vpc-1"})))
				return
			}
			// The record never goes away within the wait's bound.
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(recordBody(Record{ID: "record-1", HostedZoneID: "zone-1", Status: StatusActive})))
		case http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected method %s", r.Method)
		}
	})))

	_, err := client.DeleteRecord(context.Background(), &DeleteRecordInput{HostedZoneID: "zone-1", RecordID: "record-1"})
	if !errors.Is(err, ErrNotSettled) {
		t.Fatalf("err = %v, want ErrNotSettled", err)
	}
}

func TestDeleteRecordPreWriteWaitUnknownZonePropagatesNotFound(t *testing.T) {
	var getCalls atomic.Int64
	client := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("unexpected %s request", r.Method)
		}
		getCalls.Add(1)
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"not found"}`))
	})))

	_, err := client.DeleteRecord(context.Background(), &DeleteRecordInput{HostedZoneID: "zone-x", RecordID: "record-1"})
	if !vngcloud.IsNotFound(err) {
		t.Fatalf("IsNotFound(err) = false, err = %v", err)
	}
	if errors.Is(err, ErrZoneBusy) {
		t.Fatal("err wraps ErrZoneBusy, want the read's own NotFound unwrapped")
	}
	if getCalls.Load() != 1 {
		t.Fatalf("GET calls = %d, want 1: a read failure must not be polled", getCalls.Load())
	}
}

// --- writeLock is shared across zone and record writes, and honors ctx ---

// TestRecordWritesSerializeWithinOneClient mirrors
// TestZoneWritesSerializeWithinOneClient for two DeleteRecord calls sharing
// one Client.
func TestRecordWritesSerializeWithinOneClient(t *testing.T) {
	var inFlight atomic.Int32
	var overlapped atomic.Bool
	client := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if inFlight.Add(1) > 1 {
			overlapped.Store(true)
		}
		time.Sleep(5 * time.Millisecond)
		inFlight.Add(-1)

		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(zoneBody(StatusActive, "d", []string{"vpc-1"})))
		case http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected method %s", r.Method)
		}
	})))

	var wg sync.WaitGroup
	for _, id := range []string{"record-1", "record-2"} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			if _, err := client.DeleteRecord(context.Background(), &DeleteRecordInput{HostedZoneID: "zone-1", RecordID: id, NoWait: true}); err != nil {
				t.Errorf("DeleteRecord(%s) error = %v", id, err)
			}
		}(id)
	}
	wg.Wait()

	if overlapped.Load() {
		t.Fatal("two writes overlapped; writeLock did not serialize them")
	}
}

// TestRecordWriteRespectsCanceledContext checks that a record write goes
// through the same writeLock as a zone write, honoring ctx: a caller waiting
// for the lock gives up as soon as its own ctx ends rather than blocking
// until the holder releases it.
func TestRecordWriteRespectsCanceledContext(t *testing.T) {
	gotGet := make(chan struct{})
	release := make(chan struct{})
	var gotGetOnce sync.Once
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			gotGetOnce.Do(func() { close(gotGet) })
			<-release
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(zoneBody(StatusActive, "d", []string{"vpc-1"})))
		case http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected method %s", r.Method)
		}
	}))

	holderDone := make(chan struct{})
	go func() {
		defer close(holderDone)
		if _, err := client.DeleteRecord(context.Background(), &DeleteRecordInput{HostedZoneID: "zone-1", RecordID: "record-1", NoWait: true}); err != nil {
			t.Errorf("holder DeleteRecord() error = %v", err)
		}
	}()

	<-gotGet // the holder now has writeLock and is blocked in its pre-write read.

	waiterCtx, waiterCancel := context.WithCancel(context.Background())
	waiterCancel()
	if _, err := client.DeleteRecord(waiterCtx, &DeleteRecordInput{HostedZoneID: "zone-1", RecordID: "record-2", NoWait: true}); !errors.Is(err, context.Canceled) {
		t.Fatalf("waiter err = %v, want context.Canceled: lock acquisition must honor ctx", err)
	}

	close(release)
	<-holderDone
}
