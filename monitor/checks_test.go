package monitor

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/testutil"
)

func newTestClient(t *testing.T, handler http.Handler) *Client {
	t.Helper()
	return New(testutil.NewConfig(t, handler))
}

func TestMonitorZeroConfig(t *testing.T) {
	c := New(vngcloud.Config{})
	if _, err := c.ListChecks(context.Background(), nil); !errors.Is(err, vngcloud.ErrInvalidConfig) {
		t.Fatalf("ListChecks() err = %v, want ErrInvalidConfig", err)
	}
}

// assertExampleCheck checks every field ListChecks.json and GetCheck.json
// share, decoded from the sanitized live capture behind this design: a real
// check named "vngcloud-live-toggle" with the console's default request,
// options, and assertion, renamed and re-IDed for the fixture.
func assertExampleCheck(t *testing.T, got Check) {
	t.Helper()
	if got.ID != "chk-1" || got.Name != "example-check" || got.Type != "API" || got.Subtype != "HTTP" {
		t.Fatalf("unexpected check identity: %+v", got)
	}
	if got.Status != StatusEnabled {
		t.Fatalf("Status = %s, want %s", got.Status, StatusEnabled)
	}
	if len(got.Locations) != 1 || got.Locations[0] != "loc-1" {
		t.Fatalf("Locations = %v", got.Locations)
	}
	if got.CreatedAt != "Jan 1, 2026, 12:00:00 AM" || got.UpdatedAt != "Jan 2, 2026, 1:00:00 PM" {
		t.Fatalf("unexpected timestamps: %+v", got)
	}
	n := got.Notifications
	if len(n.InAlarm) != 0 || len(n.Up) != 0 || len(n.Undetermined) != 0 {
		t.Fatalf("unexpected notifications: %+v", n)
	}

	req := got.Config.Request
	if req.URL != "https://example.com" || req.Method != "GET" || req.Body != "" {
		t.Fatalf("unexpected request: %+v", req)
	}
	if len(req.Headers) != 0 || len(req.Query) != 0 {
		t.Fatalf("unexpected headers/query: %+v / %+v", req.Headers, req.Query)
	}
	// The API sends timeout as an integral decimal (30.0); it must still
	// decode into the int field.
	if req.Timeout != 30 {
		t.Fatalf("Timeout = %d, want 30", req.Timeout)
	}
	if !req.VerifiedSSL {
		t.Fatal("VerifiedSSL = false, want true")
	}

	if len(got.Config.Assertions) != 1 {
		t.Fatalf("unexpected assertions: %+v", got.Config.Assertions)
	}
	a := got.Config.Assertions[0]
	if a.Type != "status_code" || a.Operator != "does_not_match_regex" || a.Target != "[4-5][0-9][0-9]" {
		t.Fatalf("unexpected assertion: %+v", a)
	}

	// options.test_frequency, tests, and failed_locations are also integral
	// decimals (60.0, 1.0, 1.0) on the wire.
	if got.Options.TestFrequency != 60 || got.Options.Tests != 1 || got.Options.FailedLocations != 1 {
		t.Fatalf("unexpected options: %+v", got.Options)
	}
}

func TestListChecksDecodesFixture(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("method = %s", r.Method)
		}
		if r.URL.Path != "/vmonitor-uptime-manager/v1/uptimes" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		testutil.WriteFixture(t, w, "../testdata/monitor/ListChecks.json")
	}))

	out, err := client.ListChecks(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListChecks() error = %v", err)
	}
	if len(out.Items) != 1 {
		t.Fatalf("unexpected checks: %+v", out.Items)
	}
	assertExampleCheck(t, out.Items[0])
}

func TestGetCheckDecodesFixture(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("method = %s", r.Method)
		}
		if r.URL.Path != "/vmonitor-uptime-manager/v1/uptimes/chk-1" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		testutil.WriteFixture(t, w, "../testdata/monitor/GetCheck.json")
	}))

	out, err := client.GetCheck(context.Background(), &GetCheckInput{CheckID: "chk-1"})
	if err != nil {
		t.Fatalf("GetCheck() error = %v", err)
	}
	assertExampleCheck(t, out.Check)
}

func TestGetCheckNotFound(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"not found"}`))
	}))

	_, err := client.GetCheck(context.Background(), &GetCheckInput{CheckID: "chk-1"})
	if !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("GetCheck() error = %v, want ErrNotFound", err)
	}
}

// TestGetCheckMissingCheckID and TestGetCheckPathIDRejection cover the
// design's required path ID checks: a missing or shape-invalid CheckID
// fails before any request, on every operation that takes one.
func TestGetCheckMissingCheckID(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("unexpected request for a missing CheckID")
	}))
	if _, err := client.GetCheck(context.Background(), &GetCheckInput{}); !errors.Is(err, core.ErrInvalidInput) {
		t.Fatalf("GetCheck() error = %v, want ErrInvalidInput", err)
	}
	if _, err := client.GetCheck(context.Background(), nil); !errors.Is(err, core.ErrInvalidInput) {
		t.Fatalf("GetCheck(nil) error = %v, want ErrInvalidInput", err)
	}
}

func TestGetCheckPathIDRejection(t *testing.T) {
	for _, id := range []string{"..", ".", "/", ""} {
		t.Run(fmt.Sprintf("%q", id), func(t *testing.T) {
			client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				t.Fatal("unexpected request for a rejected CheckID")
			}))
			_, err := client.GetCheck(context.Background(), &GetCheckInput{CheckID: id})
			if !errors.Is(err, core.ErrInvalidInput) {
				t.Fatalf("GetCheck(%q) error = %v, want ErrInvalidInput", id, err)
			}
		})
	}
}
