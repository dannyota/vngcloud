package monitor

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/testutil"
)

// currentCheckJSON is a full check GetCheck's read inside UpdateCheck can
// return: every field non-empty and non-default, so a test that changes one
// field can tell the merge kept every other one apart from a coincidental
// match with a default. It mirrors the shape checks_test.go's fixtures
// decode, not a specific account's check.
const currentCheckJSON = `{
	"id": "chk-1",
	"name": "old-name",
	"type": "API",
	"subtype": "HTTP",
	"status": "%s",
	"config": {
		"request": {
			"url": "https://example.com/old",
			"method": "POST",
			"headers": {"X-Old": "1"},
			"query": {"q": "old"},
			"body": "old-body",
			"timeout": 30.0,
			"verified_ssl": true
		},
		"assertions": [
			{"type": "body", "operator": "contains", "target": "old-ok"}
		]
	},
	"options": {"test_frequency": 60.0, "tests": 2.0, "failed_locations": 1.0},
	"locations": ["loc-1", "loc-2"],
	"notifications": {"In-alarm": ["chan-1"], "Up": [], "Undetermined": ["chan-2"]},
	"created_at": "Jan 1, 2026, 12:00:00 AM",
	"updated_at": "Jan 2, 2026, 1:00:00 PM"
}`

// TestUpdateCheckReadsMergesAndSendsFullBody covers the design's read-merge
// shape: an update that sets only one field resends every other field
// exactly as GetCheck read it, and the PUT body carries no status field.
func TestUpdateCheckReadsMergesAndSendsFullBody(t *testing.T) {
	t.Run("only Name set keeps every other field", func(t *testing.T) {
		var getCalls, putCalls int
		client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodGet:
				getCalls++
				if r.URL.Path != "/vmonitor-uptime-manager/v1/uptimes/chk-1" {
					t.Fatalf("GET path = %s", r.URL.Path)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = fmt.Fprintf(w, currentCheckJSON, StatusEnabled)
			case http.MethodPut:
				putCalls++
				if r.URL.Path != "/vmonitor-uptime-manager/v1/uptimes/chk-1" {
					t.Fatalf("PUT path = %s", r.URL.Path)
				}
				body := decodeBody(t, r)
				if _, ok := body["status"]; ok {
					t.Fatalf("body carries a status field: %+v", body)
				}
				if body["type"] != "API" || body["subtype"] != "HTTP" || body["name"] != "new-name" {
					t.Fatalf("unexpected identity fields: %+v", body)
				}
				config := body["config"].(map[string]any)
				request := config["request"].(map[string]any)
				wantRequest := map[string]any{
					"url": "https://example.com/old", "method": "POST", "body": "old-body",
					"timeout": float64(30), "verified_ssl": true,
				}
				for k, v := range wantRequest {
					if request[k] != v {
						t.Fatalf("request[%q] = %v, want %v (unchanged)", k, request[k], v)
					}
				}
				headers, _ := request["headers"].(map[string]any)
				if len(headers) != 1 || headers["X-Old"] != "1" {
					t.Fatalf("headers = %+v, want unchanged", headers)
				}
				query, _ := request["query"].(map[string]any)
				if len(query) != 1 || query["q"] != "old" {
					t.Fatalf("query = %+v, want unchanged", query)
				}
				assertions := config["assertions"].([]any)
				if len(assertions) != 1 {
					t.Fatalf("assertions = %+v, want unchanged", assertions)
				}
				assertion := assertions[0].(map[string]any)
				if assertion["type"] != "body" || assertion["operator"] != "contains" || assertion["target"] != "old-ok" {
					t.Fatalf("assertion = %+v, want unchanged", assertion)
				}
				options := body["options"].(map[string]any)
				wantOptions := map[string]any{"test_frequency": float64(60), "tests": float64(2), "failed_locations": float64(1)}
				for k, v := range wantOptions {
					if options[k] != v {
						t.Fatalf("options[%q] = %v, want %v (unchanged)", k, options[k], v)
					}
				}
				locations := body["locations"].([]any)
				if len(locations) != 2 || locations[0] != "loc-1" || locations[1] != "loc-2" {
					t.Fatalf("locations = %+v, want unchanged", locations)
				}
				notifications := body["notifications"].(map[string]any)
				wantNotifications := map[string][]any{"In-alarm": {"chan-1"}, "Up": {}, "Undetermined": {"chan-2"}}
				for key, want := range wantNotifications {
					got, _ := notifications[key].([]any)
					if len(got) != len(want) {
						t.Fatalf("notifications[%q] = %v, want %v (unchanged)", key, notifications[key], want)
					}
					for i, v := range want {
						if got[i] != v {
							t.Fatalf("notifications[%q][%d] = %v, want %v", key, i, got[i], v)
						}
					}
				}
				w.WriteHeader(http.StatusOK)
				testutil.WriteFixture(t, w, "../testdata/monitor/UpdateCheck.json")
			default:
				t.Fatalf("unexpected method %s", r.Method)
			}
		}))

		newName := "new-name"
		out, err := client.UpdateCheck(context.Background(), &UpdateCheckInput{CheckID: "chk-1", Name: &newName})
		if err != nil {
			t.Fatalf("UpdateCheck() error = %v", err)
		}
		if getCalls != 1 || putCalls != 1 {
			t.Fatalf("getCalls = %d, putCalls = %d, want 1 each", getCalls, putCalls)
		}
		if out.Check.ID != "chk-1" {
			t.Fatalf("unexpected check: %+v", out.Check)
		}
	})

	t.Run("every field set", func(t *testing.T) {
		client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodGet:
				w.Header().Set("Content-Type", "application/json")
				_, _ = fmt.Fprintf(w, currentCheckJSON, StatusEnabled)
			case http.MethodPut:
				body := decodeBody(t, r)
				if body["name"] != "new-name" {
					t.Fatalf("name = %v, want new-name", body["name"])
				}
				config := body["config"].(map[string]any)
				request := config["request"].(map[string]any)
				if request["url"] != "https://example.com/new" || request["method"] != "GET" || request["body"] != "new-body" {
					t.Fatalf("request = %+v", request)
				}
				if request["timeout"] != float64(15) {
					t.Fatalf("timeout = %v, want 15", request["timeout"])
				}
				headers, _ := request["headers"].(map[string]any)
				if len(headers) != 1 || headers["X-New"] != "1" {
					t.Fatalf("headers = %+v", headers)
				}
				query, _ := request["query"].(map[string]any)
				if len(query) != 1 || query["q"] != "new" {
					t.Fatalf("query = %+v", query)
				}
				assertions := config["assertions"].([]any)
				if len(assertions) != 1 {
					t.Fatalf("assertions = %+v", assertions)
				}
				assertion := assertions[0].(map[string]any)
				if assertion["type"] != "status_code" || assertion["target"] != "[2-3][0-9][0-9]" {
					t.Fatalf("assertion = %+v", assertion)
				}
				options := body["options"].(map[string]any)
				wantOptions := map[string]any{"test_frequency": float64(5), "tests": float64(3), "failed_locations": float64(2)}
				for k, v := range wantOptions {
					if options[k] != v {
						t.Fatalf("options[%q] = %v, want %v", k, options[k], v)
					}
				}
				locations := body["locations"].([]any)
				if len(locations) != 1 || locations[0] != "loc-3" {
					t.Fatalf("locations = %+v", locations)
				}
				notifications := body["notifications"].(map[string]any)
				wantNotifications := map[string][]any{"In-alarm": {"chan-9"}, "Up": {}, "Undetermined": {}}
				for key, want := range wantNotifications {
					got, _ := notifications[key].([]any)
					if len(got) != len(want) {
						t.Fatalf("notifications[%q] = %v, want %v", key, notifications[key], want)
					}
				}
				w.WriteHeader(http.StatusOK)
				testutil.WriteFixture(t, w, "../testdata/monitor/UpdateCheck.json")
			default:
				t.Fatalf("unexpected method %s", r.Method)
			}
		}))

		newName, newURL, newMethod, newBody := "new-name", "https://example.com/new", "GET", "new-body"
		newTimeout, newFreq, newTests, newFailed := 15, 5, 3, 2
		newHeaders := map[string]string{"X-New": "1"}
		newQuery := map[string]string{"q": "new"}
		newLocations := []string{"loc-3"}
		newAssertions := []Assertion{{Type: "status_code", Operator: "does_not_match_regex", Target: "[2-3][0-9][0-9]"}}
		newNotifications := CheckNotifications{InAlarm: []string{"chan-9"}}

		_, err := client.UpdateCheck(context.Background(), &UpdateCheckInput{
			CheckID:         "chk-1",
			Name:            &newName,
			URL:             &newURL,
			Method:          &newMethod,
			Headers:         &newHeaders,
			Query:           &newQuery,
			Body:            &newBody,
			Timeout:         &newTimeout,
			TestFrequency:   &newFreq,
			Tests:           &newTests,
			FailedLocations: &newFailed,
			Locations:       &newLocations,
			Assertions:      &newAssertions,
			Notifications:   &newNotifications,
		})
		if err != nil {
			t.Fatalf("UpdateCheck() error = %v", err)
		}
	})
}

// TestUpdateCheckDoesNotToggleStatus checks that updating a DISABLED check
// sends no status field and that the returned check stays DISABLED, per the
// live-confirmed behavior that a PUT never changes a check's status.
func TestUpdateCheckDoesNotToggleStatus(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, currentCheckJSON, StatusDisabled)
		case http.MethodPut:
			body := decodeBody(t, r)
			if _, ok := body["status"]; ok {
				t.Fatalf("body carries a status field: %+v", body)
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			testutil.WriteFixture(t, w, "../testdata/monitor/UpdateCheck.json")
		default:
			t.Fatalf("unexpected method %s", r.Method)
		}
	}))

	newName := "new-name"
	out, err := client.UpdateCheck(context.Background(), &UpdateCheckInput{CheckID: "chk-1", Name: &newName})
	if err != nil {
		t.Fatalf("UpdateCheck() error = %v", err)
	}
	if out.Check.Status != StatusDisabled {
		t.Fatalf("Status = %s, want %s", out.Check.Status, StatusDisabled)
	}
}

// TestUpdateCheckUsesReadWhenResponseHasNoCheck checks that a PUT response
// carrying no check id is not treated as a failure the way CreateCheck's
// missing id is: UpdateCheck instead reads the check once and returns that.
func TestUpdateCheckUsesReadWhenResponseHasNoCheck(t *testing.T) {
	var getCalls int
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			getCalls++
			w.Header().Set("Content-Type", "application/json")
			if getCalls == 1 {
				_, _ = fmt.Fprintf(w, currentCheckJSON, StatusEnabled)
				return
			}
			testutil.WriteFixture(t, w, "../testdata/monitor/UpdateCheck.json")
		case http.MethodPut:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
		default:
			t.Fatalf("unexpected method %s", r.Method)
		}
	}))

	newName := "new-name"
	out, err := client.UpdateCheck(context.Background(), &UpdateCheckInput{CheckID: "chk-1", Name: &newName})
	if err != nil {
		t.Fatalf("UpdateCheck() error = %v", err)
	}
	if getCalls != 2 {
		t.Fatalf("getCalls = %d, want 2 (the pre-merge read and the fallback read)", getCalls)
	}
	if out.Check.ID != "chk-1" {
		t.Fatalf("unexpected check: %+v", out.Check)
	}
}

// TestUpdateCheckRequiresAtLeastOneField checks that an Input with only
// CheckID set is refused before any request, GetCheck's own read included.
func TestUpdateCheckRequiresAtLeastOneField(t *testing.T) {
	failIfCalled := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called")
	})
	client := newTestClient(t, failIfCalled)

	_, err := client.UpdateCheck(context.Background(), &UpdateCheckInput{CheckID: "chk-1"})
	if !errors.Is(err, core.ErrInvalidInput) {
		t.Fatalf("UpdateCheck() error = %v, want ErrInvalidInput", err)
	}
}

func TestUpdateCheckRequiredFields(t *testing.T) {
	failIfCalled := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called")
	})
	newName := "n"

	cases := []struct {
		name string
		in   *UpdateCheckInput
	}{
		{"nil input", nil},
		{"missing check id", &UpdateCheckInput{Name: &newName}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := newTestClient(t, failIfCalled)
			_, err := client.UpdateCheck(context.Background(), tc.in)
			if !errors.Is(err, core.ErrInvalidInput) {
				t.Fatalf("err = %v, want ErrInvalidInput", err)
			}
		})
	}
}

// TestUpdateCheckPathIDRejection covers the design's required path ID
// check: a shape-invalid CheckID fails before any request.
func TestUpdateCheckPathIDRejection(t *testing.T) {
	failIfCalled := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called")
	})
	newName := "n"

	for _, id := range []string{"..", ".", "/", ""} {
		t.Run(idLabel(id), func(t *testing.T) {
			client := newTestClient(t, failIfCalled)
			_, err := client.UpdateCheck(context.Background(), &UpdateCheckInput{CheckID: id, Name: &newName})
			if !errors.Is(err, core.ErrInvalidInput) {
				t.Fatalf("UpdateCheck(%q) error = %v, want ErrInvalidInput", id, err)
			}
		})
	}
}

// TestUpdateCheckDecodesFixture checks the PUT response decodes through the
// same Check model GetCheck and CreateCheck use, from a sanitized fixture
// shaped like the live-verified response: a renamed check that stayed
// DISABLED and kept a channel ID in its In-alarm notifications.
func TestUpdateCheckDecodesFixture(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, currentCheckJSON, StatusDisabled)
		case http.MethodPut:
			testutil.WriteFixture(t, w, "../testdata/monitor/UpdateCheck.json")
		default:
			t.Fatalf("unexpected method %s", r.Method)
		}
	}))

	newName := "example-check-renamed"
	out, err := client.UpdateCheck(context.Background(), &UpdateCheckInput{CheckID: "chk-1", Name: &newName})
	if err != nil {
		t.Fatalf("UpdateCheck() error = %v", err)
	}
	got := out.Check
	if got.ID != "chk-1" || got.Name != "example-check-renamed" || got.Status != StatusDisabled {
		t.Fatalf("unexpected check: %+v", got)
	}
	if len(got.Notifications.InAlarm) != 1 || got.Notifications.InAlarm[0] != "0123456789abcdef0123456789abcdef" {
		t.Fatalf("Notifications.InAlarm = %v", got.Notifications.InAlarm)
	}
}

// TestUpdateCheckPropagatesNotFound checks UpdateCheck returns GetCheck's
// not-found sentinel, and sends no PUT, when the check does not exist.
func TestUpdateCheckPropagatesNotFound(t *testing.T) {
	var putCalls int
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			putCalls++
		}
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"not found"}`))
	}))

	newName := "n"
	_, err := client.UpdateCheck(context.Background(), &UpdateCheckInput{CheckID: "chk-missing", Name: &newName})
	if !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("UpdateCheck() error = %v, want ErrNotFound", err)
	}
	if putCalls != 0 {
		t.Fatalf("putCalls = %d, want 0", putCalls)
	}
}

// TestUpdateCheckRefusesWhenPreReadHasNoUsableCheck checks that a pre-update
// GET returning no usable check, such as a 200 that decodes to {}, is never
// merged into the full-replace PUT: doing so would send the check's
// zero-valued fields and wipe it. UpdateCheck refuses before any PUT.
func TestUpdateCheckRefusesWhenPreReadHasNoUsableCheck(t *testing.T) {
	var putCalls int
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{}`))
		case http.MethodPut:
			putCalls++
			w.WriteHeader(http.StatusOK)
		default:
			t.Fatalf("unexpected method %s", r.Method)
		}
	}))

	newName := "new-name"
	_, err := client.UpdateCheck(context.Background(), &UpdateCheckInput{CheckID: "chk-1", Name: &newName})
	if err == nil {
		t.Fatal("UpdateCheck() error = nil, want an error for an unusable pre-read")
	}
	var apiErr *core.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error = %T, want *core.APIError", err)
	}
	if putCalls != 0 {
		t.Fatalf("putCalls = %d, want 0", putCalls)
	}
}

// TestUpdateCheckRefusesNonHTTPCheck checks that UpdateCheck refuses to
// merge and resend a check whose current Type or Subtype is not API/HTTP:
// checkWriteBody only carries an HTTP request, so resending it would
// silently convert the check to a type it never was.
func TestUpdateCheckRefusesNonHTTPCheck(t *testing.T) {
	var putCalls int
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"id":"chk-1","name":"old","type":"PING","subtype":"ICMP","status":%q}`, StatusEnabled)
		case http.MethodPut:
			putCalls++
			w.WriteHeader(http.StatusOK)
		default:
			t.Fatalf("unexpected method %s", r.Method)
		}
	}))

	newName := "new-name"
	_, err := client.UpdateCheck(context.Background(), &UpdateCheckInput{CheckID: "chk-1", Name: &newName})
	if !errors.Is(err, core.ErrInvalidInput) {
		t.Fatalf("UpdateCheck() error = %v, want ErrInvalidInput", err)
	}
	if putCalls != 0 {
		t.Fatalf("putCalls = %d, want 0", putCalls)
	}
}

// TestUpdateCheckRefusesEmptyLocations checks that a Locations set to a
// non-nil empty slice is refused before any request, the same as
// CreateCheck refuses an empty Locations: sending it would clear every
// location the check runs from.
func TestUpdateCheckRefusesEmptyLocations(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called")
	}))

	emptyLocations := []string{}
	_, err := client.UpdateCheck(context.Background(), &UpdateCheckInput{CheckID: "chk-1", Locations: &emptyLocations})
	if !errors.Is(err, core.ErrInvalidInput) {
		t.Fatalf("UpdateCheck() error = %v, want ErrInvalidInput", err)
	}
}

// TestUpdateCheckAndPauseCheckSerializeUnderMutex checks that UpdateCheck and
// PauseCheck, run concurrently on one Client, never let their own
// read-then-write sequences overlap: toggleMu backs UpdateCheck the same way
// it backs PauseCheck and ResumeCheck, so the server here only ever sees one
// in-flight request at a time from this Client.
func TestUpdateCheckAndPauseCheckSerializeUnderMutex(t *testing.T) {
	var inFlight atomic.Int64
	var maxInFlight atomic.Int64
	var mu sync.Mutex
	status := StatusEnabled
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := inFlight.Add(1)
		for {
			if m := maxInFlight.Load(); n > m {
				if maxInFlight.CompareAndSwap(m, n) {
					break
				}
				continue
			}
			break
		}
		time.Sleep(5 * time.Millisecond)
		defer inFlight.Add(-1)

		switch {
		case r.Method == http.MethodGet:
			mu.Lock()
			current := status
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, currentCheckJSON, current)
		case r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/status/"):
			mu.Lock()
			status = StatusDisabled
			mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodPut:
			mu.Lock()
			current := status
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = fmt.Fprintf(w, currentCheckJSON, current)
		default:
			t.Errorf("unexpected method %s", r.Method)
		}
	}))
	defer server.Close()

	client := newToggleClient(t, server, nil, noopSleep)
	newName := "concurrent-name"
	const rounds = 4
	errs := make(chan error, rounds*2)
	for range rounds {
		go func() {
			_, err := client.PauseCheck(context.Background(), &PauseCheckInput{CheckID: "chk-1"})
			errs <- err
		}()
		go func() {
			_, err := client.UpdateCheck(context.Background(), &UpdateCheckInput{CheckID: "chk-1", Name: &newName})
			errs <- err
		}()
	}
	for range rounds * 2 {
		if err := <-errs; err != nil {
			t.Errorf("call error = %v", err)
		}
	}
	if maxInFlight.Load() > 1 {
		t.Fatalf("max in-flight requests = %d, want 1: toggleMu must serialize UpdateCheck against PauseCheck", maxInFlight.Load())
	}
}
