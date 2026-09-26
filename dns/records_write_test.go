package dns

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/testutil"
)

// recordBody builds a {"data": {...}} record envelope from r, using Record's
// own json tags, so a test only sets the fields it cares about.
func recordBody(r Record) string {
	var fixture struct {
		Data Record `json:"data"`
	}
	fixture.Data = r
	b, err := json.Marshal(fixture)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// --- CreateRecord ---

func TestCreateRecordRequestBody(t *testing.T) {
	cases := []struct {
		name string
		in   *CreateRecordInput
		want map[string]any
	}{
		{
			name: "defaults: apex, TTL, and routing policy",
			in: &CreateRecordInput{
				HostedZoneID: "zone-1",
				Type:         "A",
				Values:       []RecordValue{{Value: "<ip>"}},
				NoWait:       true,
			},
			want: map[string]any{
				"subDomain":     "",
				"ttl":           float64(300),
				"type":          "A",
				"routingPolicy": "simple-routing",
				"value":         []any{map[string]any{"value": "<ip>"}},
			},
		},
		{
			name: "every field set, weighted values with location and weight",
			in: &CreateRecordInput{
				HostedZoneID:  "zone-1",
				SubDomain:     "www",
				Type:          "A",
				TTL:           120,
				RoutingPolicy: "weighted",
				Values: []RecordValue{
					{Value: "<ip>", Location: vngcloud.Ptr("hcm"), Weight: vngcloud.Ptr(10)},
					{Value: "<ip>", Location: vngcloud.Ptr("han"), Weight: vngcloud.Ptr(20)},
				},
				StickySession: vngcloud.Ptr(true),
				NoWait:        true,
			},
			want: map[string]any{
				"subDomain":     "www",
				"ttl":           float64(120),
				"type":          "A",
				"routingPolicy": "weighted",
				"value": []any{
					map[string]any{"value": "<ip>", "location": "hcm", "weight": float64(10)},
					map[string]any{"value": "<ip>", "location": "han", "weight": float64(20)},
				},
				"enableStickySession": true,
			},
		},
		{
			name: "MX values pass through unvalidated",
			in: &CreateRecordInput{
				HostedZoneID: "zone-1",
				Type:         "MX",
				Values: []RecordValue{
					{Value: "10 mx1.example.com"},
					{Value: "20 mx2.example.com"},
				},
				NoWait: true,
			},
			want: map[string]any{
				"subDomain":     "",
				"ttl":           float64(300),
				"type":          "MX",
				"routingPolicy": "simple-routing",
				"value": []any{
					map[string]any{"value": "10 mx1.example.com"},
					map[string]any{"value": "20 mx2.example.com"},
				},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.Method {
				case http.MethodGet:
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(zoneBody(StatusActive, "d", []string{"vpc-1"})))
				case http.MethodPost:
					if r.URL.Path != "/v1/dns/hosted-zone/zone-1/record" {
						t.Fatalf("path = %s", r.URL.Path)
					}
					body := decodeBody(t, r)
					if len(body) != len(tc.want) {
						t.Fatalf("body = %+v, want %+v", body, tc.want)
					}
					for k, v := range tc.want {
						if !deepEqualJSON(body[k], v) {
							t.Fatalf("body[%q] = %#v, want %#v", k, body[k], v)
						}
					}
					testutil.WriteFixture(t, w, "../testdata/dns/create_record.json")
				default:
					t.Fatalf("unexpected method %s", r.Method)
				}
			}))

			out, err := client.CreateRecord(context.Background(), tc.in)
			if err != nil {
				t.Fatalf("CreateRecord() error = %v", err)
			}
			if out.Record.ID != "record-1" {
				t.Fatalf("unexpected record: %+v", out.Record)
			}
		})
	}
}

// deepEqualJSON compares two values decoded from JSON (map[string]any,
// []any, string, float64, bool) for equality.
func deepEqualJSON(a, b any) bool {
	aj, err := json.Marshal(a)
	if err != nil {
		return false
	}
	bj, err := json.Marshal(b)
	if err != nil {
		return false
	}
	return string(aj) == string(bj)
}

func TestCreateRecordDecodesFixture(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(zoneBody(StatusActive, "d", []string{"vpc-1"})))
		case http.MethodPost:
			testutil.WriteFixture(t, w, "../testdata/dns/create_record.json")
		default:
			t.Fatalf("unexpected method %s", r.Method)
		}
	}))

	out, err := client.CreateRecord(context.Background(), &CreateRecordInput{
		HostedZoneID: "zone-1",
		Type:         "TXT",
		Values:       []RecordValue{{Value: "<secret>"}, {Value: "<secret>"}},
		NoWait:       true,
	})
	if err != nil {
		t.Fatalf("CreateRecord() error = %v", err)
	}
	if out.Record.Status != StatusCreating {
		t.Fatalf("Status = %q, want %q", out.Record.Status, StatusCreating)
	}
	if out.Record.Type != "TXT" || len(out.Record.Value) != 2 {
		t.Fatalf("unexpected record: %+v", out.Record)
	}
}

func TestCreateRecordNoWaitReturnsCreatingResource(t *testing.T) {
	var recordGetCalls atomic.Int64
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			if r.URL.Path != "/v1/dns/hosted-zone/zone-1" {
				recordGetCalls.Add(1)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(zoneBody(StatusActive, "d", []string{"vpc-1"})))
		case http.MethodPost:
			testutil.WriteFixture(t, w, "../testdata/dns/create_record.json")
		default:
			t.Fatalf("unexpected method %s", r.Method)
		}
	}))

	out, err := client.CreateRecord(context.Background(), &CreateRecordInput{
		HostedZoneID: "zone-1",
		Type:         "TXT",
		Values:       []RecordValue{{Value: "<secret>"}},
		NoWait:       true,
	})
	if err != nil {
		t.Fatalf("CreateRecord() error = %v", err)
	}
	if out.Record.Status != StatusCreating {
		t.Fatalf("Status = %q, want %q (the response's own status, no wait)", out.Record.Status, StatusCreating)
	}
	if recordGetCalls.Load() != 0 {
		t.Fatalf("record GET calls = %d, want 0: NoWait must send no post-write read", recordGetCalls.Load())
	}
}

func TestCreateRecordRequiredFields(t *testing.T) {
	failIfCalled := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("handler should not be called")
	})
	cases := []struct {
		name string
		in   *CreateRecordInput
	}{
		{"nil input", nil},
		{"missing hosted zone id", &CreateRecordInput{Type: "A", Values: []RecordValue{{Value: "<ip>"}}}},
		{"missing type", &CreateRecordInput{HostedZoneID: "zone-1", Values: []RecordValue{{Value: "<ip>"}}}},
		{"missing values", &CreateRecordInput{HostedZoneID: "zone-1", Type: "A"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := newTestClient(t, failIfCalled)
			_, err := client.CreateRecord(context.Background(), tc.in)
			if !errors.Is(err, vngcloud.ErrInvalidInput) {
				t.Fatalf("err = %v, want ErrInvalidInput", err)
			}
		})
	}
}

func TestCreateRecordMissingID(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(zoneBody(StatusActive, "d", []string{"vpc-1"})))
		case http.MethodPost:
			_, _ = w.Write([]byte(`{"data":{"subDomain":"","status":"CREATING"}}`))
		default:
			t.Fatalf("unexpected method %s", r.Method)
		}
	}))

	_, err := client.CreateRecord(context.Background(), &CreateRecordInput{
		HostedZoneID: "zone-1",
		Type:         "A",
		Values:       []RecordValue{{Value: "<ip>"}},
		NoWait:       true,
	})
	var apiErr *vngcloud.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *vngcloud.APIError, got %v", err)
	}
	if apiErr.Message != "create response had no id" || apiErr.Operation != "dns.CreateRecord" {
		t.Fatalf("unexpected error: %+v", apiErr)
	}
}

func TestCreateRecordNotRetriedAfter502(t *testing.T) {
	var postCalls atomic.Int64
	client := New(testutil.NewRetryConfig(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(zoneBody(StatusActive, "d", []string{"vpc-1"})))
		case http.MethodPost:
			postCalls.Add(1)
			w.WriteHeader(http.StatusBadGateway)
		default:
			t.Fatalf("unexpected method %s", r.Method)
		}
	})))

	_, err := client.CreateRecord(context.Background(), &CreateRecordInput{
		HostedZoneID: "zone-1",
		Type:         "A",
		Values:       []RecordValue{{Value: "<ip>"}},
		NoWait:       true,
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if postCalls.Load() != 1 {
		t.Fatalf("POST calls = %d, want 1", postCalls.Load())
	}
	if vngcloud.IsRetryable(err) {
		t.Fatal("IsRetryable(err) = true, want false")
	}
}

func TestCreateRecordLockNotRetried(t *testing.T) {
	var postCalls atomic.Int64
	client := New(testutil.NewRetryConfig(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(zoneBody(StatusActive, "d", []string{"vpc-1"})))
		case http.MethodPost:
			postCalls.Add(1)
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"message":"zone-1 was invalid status. Allowed in [ACTIVE, ERROR]"}`))
		default:
			t.Fatalf("unexpected method %s", r.Method)
		}
	})))

	_, err := client.CreateRecord(context.Background(), &CreateRecordInput{
		HostedZoneID: "zone-1",
		Type:         "A",
		Values:       []RecordValue{{Value: "<ip>"}},
		NoWait:       true,
	})
	var apiErr *core.APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusBadRequest {
		t.Fatalf("unexpected error: %v", err)
	}
	if errors.Is(err, ErrZoneBusy) {
		t.Fatal("err wraps ErrZoneBusy, want the server's own 400 unwrapped")
	}
	if postCalls.Load() != 1 {
		t.Fatalf("POST calls = %d, want 1: a 400 is never retried", postCalls.Load())
	}
}

func TestCreateRecordConflict(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(zoneBody(StatusActive, "d", []string{"vpc-1"})))
		case http.MethodPost:
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"message":"Type must be unique in one subdomain."}`))
		default:
			t.Fatalf("unexpected method %s", r.Method)
		}
	}))

	_, err := client.CreateRecord(context.Background(), &CreateRecordInput{
		HostedZoneID: "zone-1",
		Type:         "A",
		Values:       []RecordValue{{Value: "<ip>"}},
		NoWait:       true,
	})
	var apiErr *vngcloud.APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusConflict {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCreateRecordPathIDRejection(t *testing.T) {
	failIfCalled := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("handler should not be called")
	})
	for _, badID := range []string{"..", ".", "a/b", ""} {
		t.Run(badID, func(t *testing.T) {
			client := newTestClient(t, failIfCalled)
			_, err := client.CreateRecord(context.Background(), &CreateRecordInput{
				HostedZoneID: badID,
				Type:         "A",
				Values:       []RecordValue{{Value: "<ip>"}},
			})
			if !errors.Is(err, vngcloud.ErrInvalidInput) {
				t.Fatalf("err = %v, want ErrInvalidInput", err)
			}
		})
	}
}

// --- UpdateRecord ---

func TestUpdateRecordNoFieldsSet(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("handler should not be called")
	}))

	_, err := client.UpdateRecord(context.Background(), &UpdateRecordInput{HostedZoneID: "zone-1", RecordID: "record-1"})
	if !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
}

func TestUpdateRecordRequestBody(t *testing.T) {
	cases := []struct {
		name string
		in   *UpdateRecordInput
		want map[string]any
	}{
		{
			name: "ttl only sends only ttl",
			in: &UpdateRecordInput{
				HostedZoneID: "zone-1",
				RecordID:     "record-1",
				TTL:          vngcloud.Ptr(120),
				NoWait:       true,
			},
			want: map[string]any{"ttl": float64(120)},
		},
		{
			name: "every field set",
			in: &UpdateRecordInput{
				HostedZoneID:  "zone-1",
				RecordID:      "record-1",
				SubDomain:     vngcloud.Ptr("www"),
				Type:          vngcloud.Ptr("A"),
				TTL:           vngcloud.Ptr(60),
				RoutingPolicy: vngcloud.Ptr("weighted"),
				Values:        vngcloud.Ptr([]RecordValue{{Value: "<ip>", Weight: vngcloud.Ptr(5)}}),
				StickySession: vngcloud.Ptr(false),
				NoWait:        true,
			},
			want: map[string]any{
				"subDomain":           "www",
				"type":                "A",
				"ttl":                 float64(60),
				"routingPolicy":       "weighted",
				"value":               []any{map[string]any{"value": "<ip>", "weight": float64(5)}},
				"enableStickySession": false,
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var putCalls atomic.Int64
			client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.Method {
				case http.MethodGet:
					switch r.URL.Path {
					case "/v1/dns/hosted-zone/zone-1":
						w.Header().Set("Content-Type", "application/json")
						_, _ = w.Write([]byte(zoneBody(StatusActive, "d", []string{"vpc-1"})))
					case "/v1/dns/hosted-zone/zone-1/record/record-1":
						w.Header().Set("Content-Type", "application/json")
						_, _ = w.Write([]byte(recordBody(Record{ID: "record-1", HostedZoneID: "zone-1", Status: StatusActive})))
					default:
						t.Fatalf("unexpected GET path %s", r.URL.Path)
					}
				case http.MethodPut:
					putCalls.Add(1)
					if r.URL.Path != "/v1/dns/hosted-zone/zone-1/record/record-1" {
						t.Fatalf("path = %s", r.URL.Path)
					}
					body := decodeBody(t, r)
					if len(body) != len(tc.want) {
						t.Fatalf("body = %+v, want %+v", body, tc.want)
					}
					for k, v := range tc.want {
						if !deepEqualJSON(body[k], v) {
							t.Fatalf("body[%q] = %#v, want %#v", k, body[k], v)
						}
					}
					w.WriteHeader(http.StatusNoContent)
				default:
					t.Fatalf("unexpected method %s", r.Method)
				}
			}))

			if _, err := client.UpdateRecord(context.Background(), tc.in); err != nil {
				t.Fatalf("UpdateRecord() error = %v", err)
			}
			if putCalls.Load() != 1 {
				t.Fatalf("PUT calls = %d, want 1", putCalls.Load())
			}
		})
	}
}

func TestUpdateRecordNoWaitSendsOneRead(t *testing.T) {
	var recordGetCalls atomic.Int64
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			switch r.URL.Path {
			case "/v1/dns/hosted-zone/zone-1":
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(zoneBody(StatusActive, "d", []string{"vpc-1"})))
			case "/v1/dns/hosted-zone/zone-1/record/record-1":
				recordGetCalls.Add(1)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(recordBody(Record{ID: "record-1", HostedZoneID: "zone-1", Status: StatusActive, TTL: 60})))
			default:
				t.Fatalf("unexpected GET path %s", r.URL.Path)
			}
		case http.MethodPut:
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected method %s", r.Method)
		}
	}))

	out, err := client.UpdateRecord(context.Background(), &UpdateRecordInput{
		HostedZoneID: "zone-1",
		RecordID:     "record-1",
		TTL:          vngcloud.Ptr(60),
		NoWait:       true,
	})
	if err != nil {
		t.Fatalf("UpdateRecord() error = %v", err)
	}
	if out.Record.TTL != 60 {
		t.Fatalf("TTL = %d, want 60", out.Record.TTL)
	}
	if recordGetCalls.Load() != 1 {
		t.Fatalf("record GET calls = %d, want 1: NoWait sends exactly one confirm read", recordGetCalls.Load())
	}
}

// TestUpdateRecordNoWaitConfirmReadFailure checks that a failure of NoWait's
// own single confirm read, after the PUT has already succeeded, wraps
// ErrNotSettled and still returns a non-nil Output built from the fields
// the PUT itself sent, rather than the bare read error over a nil Output.
func TestUpdateRecordNoWaitConfirmReadFailure(t *testing.T) {
	putDone := false
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			switch r.URL.Path {
			case "/v1/dns/hosted-zone/zone-1":
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(zoneBody(StatusActive, "d", []string{"vpc-1"})))
			case "/v1/dns/hosted-zone/zone-1/record/record-1":
				if !putDone {
					t.Fatal("confirm read happened before the PUT")
				}
				w.WriteHeader(http.StatusInternalServerError)
			default:
				t.Fatalf("unexpected GET path %s", r.URL.Path)
			}
		case http.MethodPut:
			putDone = true
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected method %s", r.Method)
		}
	}))

	out, err := client.UpdateRecord(context.Background(), &UpdateRecordInput{
		HostedZoneID: "zone-1",
		RecordID:     "record-1",
		TTL:          vngcloud.Ptr(60),
		NoWait:       true,
	})
	if !errors.Is(err, ErrNotSettled) {
		t.Fatalf("err = %v, want ErrNotSettled", err)
	}
	if out == nil || out.Record.ID != "record-1" || out.Record.TTL != 60 {
		t.Fatalf("out = %+v, want a non-nil Output carrying the record id and the sent TTL", out)
	}
}

func TestUpdateRecordLockNotRetried(t *testing.T) {
	var putCalls atomic.Int64
	client := New(testutil.NewRetryConfig(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(zoneBody(StatusActive, "d", []string{"vpc-1"})))
		case http.MethodPut:
			putCalls.Add(1)
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"message":"zone-1 was invalid status. Allowed in [ACTIVE, ERROR]"}`))
		default:
			t.Fatalf("unexpected method %s", r.Method)
		}
	})))

	_, err := client.UpdateRecord(context.Background(), &UpdateRecordInput{
		HostedZoneID: "zone-1",
		RecordID:     "record-1",
		TTL:          vngcloud.Ptr(60),
		NoWait:       true,
	})
	var apiErr *core.APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusBadRequest {
		t.Fatalf("unexpected error: %v", err)
	}
	if putCalls.Load() != 1 {
		t.Fatalf("PUT calls = %d, want 1: a 400 is never retried", putCalls.Load())
	}
}

func TestUpdateRecordPathIDRejection(t *testing.T) {
	failIfCalled := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("handler should not be called")
	})
	for _, badID := range []string{"..", ".", "a/b", ""} {
		t.Run("HostedZoneID "+badID, func(t *testing.T) {
			client := newTestClient(t, failIfCalled)
			_, err := client.UpdateRecord(context.Background(), &UpdateRecordInput{
				HostedZoneID: badID,
				RecordID:     "record-1",
				TTL:          vngcloud.Ptr(60),
			})
			if !errors.Is(err, vngcloud.ErrInvalidInput) {
				t.Fatalf("err = %v, want ErrInvalidInput", err)
			}
		})
		t.Run("RecordID "+badID, func(t *testing.T) {
			client := newTestClient(t, failIfCalled)
			_, err := client.UpdateRecord(context.Background(), &UpdateRecordInput{
				HostedZoneID: "zone-1",
				RecordID:     badID,
				TTL:          vngcloud.Ptr(60),
			})
			if !errors.Is(err, vngcloud.ErrInvalidInput) {
				t.Fatalf("err = %v, want ErrInvalidInput", err)
			}
		})
	}
}

// --- DeleteRecord ---

func TestDeleteRecordRequestHasNoBody(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(zoneBody(StatusActive, "d", []string{"vpc-1"})))
		case http.MethodDelete:
			if r.URL.Path != "/v1/dns/hosted-zone/zone-1/record/record-1" {
				t.Fatalf("path = %s", r.URL.Path)
			}
			data := decodeBody(t, r)
			if data != nil {
				t.Fatalf("body = %+v, want empty", data)
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected method %s", r.Method)
		}
	}))

	_, err := client.DeleteRecord(context.Background(), &DeleteRecordInput{HostedZoneID: "zone-1", RecordID: "record-1", NoWait: true})
	if err != nil {
		t.Fatalf("DeleteRecord() error = %v", err)
	}
}

func TestDeleteRecordNoWaitReturnsAtOnce(t *testing.T) {
	var recordGetCallsAfterDelete atomic.Int64
	deleted := false
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			if deleted && r.URL.Path != "/v1/dns/hosted-zone/zone-1" {
				recordGetCallsAfterDelete.Add(1)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(zoneBody(StatusActive, "d", []string{"vpc-1"})))
		case http.MethodDelete:
			deleted = true
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected method %s", r.Method)
		}
	}))

	out, err := client.DeleteRecord(context.Background(), &DeleteRecordInput{HostedZoneID: "zone-1", RecordID: "record-1", NoWait: true})
	if err != nil {
		t.Fatalf("DeleteRecord() error = %v", err)
	}
	if out == nil {
		t.Fatal("out is nil")
	}
	if recordGetCallsAfterDelete.Load() != 0 {
		t.Fatalf("record GET calls after delete = %d, want 0: NoWait must return at once", recordGetCallsAfterDelete.Load())
	}
}

func TestDeleteRecordPathIDRejection(t *testing.T) {
	failIfCalled := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("handler should not be called")
	})
	for _, badID := range []string{"..", ".", "a/b", ""} {
		t.Run("HostedZoneID "+badID, func(t *testing.T) {
			client := newTestClient(t, failIfCalled)
			_, err := client.DeleteRecord(context.Background(), &DeleteRecordInput{HostedZoneID: badID, RecordID: "record-1"})
			if !errors.Is(err, vngcloud.ErrInvalidInput) {
				t.Fatalf("err = %v, want ErrInvalidInput", err)
			}
		})
		t.Run("RecordID "+badID, func(t *testing.T) {
			client := newTestClient(t, failIfCalled)
			_, err := client.DeleteRecord(context.Background(), &DeleteRecordInput{HostedZoneID: "zone-1", RecordID: badID})
			if !errors.Is(err, vngcloud.ErrInvalidInput) {
				t.Fatalf("err = %v, want ErrInvalidInput", err)
			}
		})
	}
}
