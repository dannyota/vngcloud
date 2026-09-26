package monitor

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"testing"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/testutil"
)

func decodeBody(t *testing.T, r *http.Request) map[string]any {
	t.Helper()
	data, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if len(data) == 0 {
		return nil
	}
	var body map[string]any
	if err := json.Unmarshal(data, &body); err != nil {
		t.Fatalf("decode body: %v, raw = %s", err, data)
	}
	return body
}

// TestCreateCheckSendsFieldsAndDefaults covers the request body with every
// field set and with every optional field left zero, per the design's
// discovered console defaults.
func TestCreateCheckSendsFieldsAndDefaults(t *testing.T) {
	t.Run("every field set", func(t *testing.T) {
		client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				t.Fatalf("method = %s", r.Method)
			}
			if r.URL.Path != "/vmonitor-uptime-manager/v1/uptimes" {
				t.Fatalf("path = %s", r.URL.Path)
			}
			body := decodeBody(t, r)
			if body["type"] != "API" || body["subtype"] != "HTTP" || body["name"] != "vngcloud-live-abcd1234" {
				t.Fatalf("unexpected identity fields: %+v", body)
			}
			config, ok := body["config"].(map[string]any)
			if !ok {
				t.Fatalf("config missing or wrong shape: %+v", body)
			}
			request, ok := config["request"].(map[string]any)
			if !ok {
				t.Fatalf("config.request missing or wrong shape: %+v", config)
			}
			wantRequest := map[string]any{
				"url":          "https://example.com/health",
				"method":       "POST",
				"body":         "ping",
				"timeout":      float64(5),
				"verified_ssl": true,
			}
			for k, v := range wantRequest {
				if request[k] != v {
					t.Fatalf("request[%q] = %v, want %v (request = %+v)", k, request[k], v, request)
				}
			}
			headers, _ := request["headers"].(map[string]any)
			if len(headers) != 1 || headers["X-Test"] != "1" {
				t.Fatalf("headers = %+v", headers)
			}
			query, _ := request["query"].(map[string]any)
			if len(query) != 1 || query["q"] != "1" {
				t.Fatalf("query = %+v", query)
			}
			assertions, ok := config["assertions"].([]any)
			if !ok || len(assertions) != 1 {
				t.Fatalf("assertions = %+v", config["assertions"])
			}
			assertion := assertions[0].(map[string]any)
			if assertion["type"] != "body" || assertion["operator"] != "contains" || assertion["target"] != "ok" {
				t.Fatalf("unexpected assertion: %+v", assertion)
			}
			options, ok := body["options"].(map[string]any)
			if !ok {
				t.Fatalf("options missing or wrong shape: %+v", body)
			}
			wantOptions := map[string]any{"test_frequency": float64(15), "tests": float64(3), "failed_locations": float64(2)}
			for k, v := range wantOptions {
				if options[k] != v {
					t.Fatalf("options[%q] = %v, want %v (options = %+v)", k, options[k], v, options)
				}
			}
			locations, ok := body["locations"].([]any)
			if !ok || len(locations) != 2 || locations[0] != "loc-1" || locations[1] != "loc-2" {
				t.Fatalf("locations = %+v", body["locations"])
			}
			notifications, ok := body["notifications"].(map[string]any)
			if !ok {
				t.Fatalf("notifications missing or wrong shape: %+v", body)
			}
			for _, key := range []string{"In-alarm", "Up", "Undetermined"} {
				list, ok := notifications[key].([]any)
				if !ok || len(list) != 0 {
					t.Fatalf("notifications[%q] = %v, want an empty list", key, notifications[key])
				}
			}
			w.WriteHeader(http.StatusCreated)
			testutil.WriteFixture(t, w, "../testdata/monitor/CreateCheck.json")
		}))

		out, err := client.CreateCheck(context.Background(), &CreateCheckInput{
			Name:            "vngcloud-live-abcd1234",
			URL:             "https://example.com/health",
			Locations:       []string{"loc-1", "loc-2"},
			Method:          "POST",
			Headers:         map[string]string{"X-Test": "1"},
			Query:           map[string]string{"q": "1"},
			Body:            "ping",
			Timeout:         5,
			TestFrequency:   15,
			Tests:           3,
			FailedLocations: 2,
			Assertions:      []Assertion{{Type: "body", Operator: "contains", Target: "ok"}},
		})
		if err != nil {
			t.Fatalf("CreateCheck() error = %v", err)
		}
		if out.Check.ID != "chk-2" {
			t.Fatalf("unexpected check: %+v", out.Check)
		}
	})

	t.Run("defaults", func(t *testing.T) {
		client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body := decodeBody(t, r)
			config := body["config"].(map[string]any)
			request := config["request"].(map[string]any)
			if request["method"] != "GET" {
				t.Fatalf("method = %v, want GET", request["method"])
			}
			if request["body"] != "" {
				t.Fatalf("body = %v, want empty", request["body"])
			}
			if request["timeout"] != float64(defaultTimeout) {
				t.Fatalf("timeout = %v, want %d", request["timeout"], defaultTimeout)
			}
			headers, ok := request["headers"].(map[string]any)
			if !ok || len(headers) != 0 {
				t.Fatalf("headers = %+v, want an empty object", request["headers"])
			}
			query, ok := request["query"].(map[string]any)
			if !ok || len(query) != 0 {
				t.Fatalf("query = %+v, want an empty object", request["query"])
			}
			assertions := config["assertions"].([]any)
			if len(assertions) != 1 {
				t.Fatalf("assertions = %+v, want the console default", assertions)
			}
			assertion := assertions[0].(map[string]any)
			if assertion["type"] != defaultAssertion.Type || assertion["operator"] != defaultAssertion.Operator || assertion["target"] != defaultAssertion.Target {
				t.Fatalf("unexpected default assertion: %+v", assertion)
			}
			options := body["options"].(map[string]any)
			wantOptions := map[string]any{
				"test_frequency":   float64(defaultTestFrequency),
				"tests":            float64(defaultTests),
				"failed_locations": float64(1), // len(Locations): one location was passed
			}
			for k, v := range wantOptions {
				if options[k] != v {
					t.Fatalf("options[%q] = %v, want %v", k, options[k], v)
				}
			}
			w.WriteHeader(http.StatusCreated)
			testutil.WriteFixture(t, w, "../testdata/monitor/CreateCheck.json")
		}))

		out, err := client.CreateCheck(context.Background(), &CreateCheckInput{
			Name:      "vngcloud-live-abcd1234",
			URL:       "https://example.com/health",
			Locations: []string{"loc-1"},
		})
		if err != nil {
			t.Fatalf("CreateCheck() error = %v", err)
		}
		// CreateCheck.json encodes these as integral decimals (10.0, 1.0);
		// they must still decode into the response Check's int and bool
		// fields.
		gotOptions := out.Check.Options
		if gotOptions.TestFrequency != 1 || gotOptions.Tests != 1 || gotOptions.FailedLocations != 1 {
			t.Fatalf("unexpected options: %+v", gotOptions)
		}
		if out.Check.Config.Request.Timeout != 10 {
			t.Fatalf("Timeout = %d, want 10", out.Check.Config.Request.Timeout)
		}
		if !out.Check.Config.Request.VerifiedSSL {
			t.Fatal("VerifiedSSL = false, want true")
		}
	})
}

func TestCreateCheckRequiredFields(t *testing.T) {
	failIfCalled := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called")
	})

	cases := []struct {
		name string
		in   *CreateCheckInput
	}{
		{"nil input", nil},
		{"missing name", &CreateCheckInput{URL: "https://example.com", Locations: []string{"loc-1"}}},
		{"missing url", &CreateCheckInput{Name: "vngcloud-live-x", Locations: []string{"loc-1"}}},
		{"missing locations", &CreateCheckInput{Name: "vngcloud-live-x", URL: "https://example.com"}},
		{"empty locations", &CreateCheckInput{Name: "vngcloud-live-x", URL: "https://example.com", Locations: []string{}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := newTestClient(t, failIfCalled)
			_, err := client.CreateCheck(context.Background(), tc.in)
			if !errors.Is(err, core.ErrInvalidInput) {
				t.Fatalf("err = %v, want ErrInvalidInput", err)
			}
		})
	}
}

// TestCreateCheckMissingID checks that a 201 response without an id is an
// *core.APIError: the SDK never finds a new check by listing names.
func TestCreateCheckMissingID(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"no id field", `{"name":"x","status":"ENABLED"}`},
		{"empty body", ``},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusCreated)
				_, _ = w.Write([]byte(tc.body))
			}))

			_, err := client.CreateCheck(context.Background(), &CreateCheckInput{
				Name:      "vngcloud-live-x",
				URL:       "https://example.com",
				Locations: []string{"loc-1"},
			})
			var apiErr *core.APIError
			if !errors.As(err, &apiErr) {
				t.Fatalf("expected *core.APIError, got %v", err)
			}
			if apiErr.Message != "create response had no id" {
				t.Fatalf("Message = %q", apiErr.Message)
			}
			if apiErr.Operation != "monitor.CreateCheck" {
				t.Fatalf("Operation = %q", apiErr.Operation)
			}
		})
	}
}

// TestCreateCheckNotRetriedAfter502 checks a POST create is never retried
// after an ambiguous failure, since a retried create could create a second
// check.
func TestCreateCheckNotRetriedAfter502(t *testing.T) {
	calls := 0
	client := New(testutil.NewRetryConfig(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusBadGateway)
	})))

	_, err := client.CreateCheck(context.Background(), &CreateCheckInput{
		Name:      "vngcloud-live-x",
		URL:       "https://example.com",
		Locations: []string{"loc-1"},
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}
	if core.IsRetryable(err) {
		t.Fatal("IsRetryable(err) = true, want false")
	}
}

// TestCreateCheckAPIErrorNotRetried checks a create the server rejects with
// a 4xx returns that status as an *core.APIError, sent exactly once: a 4xx
// is never retryable, so a POST is not retried after a client error
// either.
func TestCreateCheckAPIErrorNotRetried(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusNotFound} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			calls := 0
			client := New(testutil.NewRetryConfig(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.WriteHeader(status)
				_, _ = w.Write([]byte(`{"message":"rejected"}`))
			})))

			_, err := client.CreateCheck(context.Background(), &CreateCheckInput{
				Name:      "vngcloud-live-x",
				URL:       "https://example.com",
				Locations: []string{"loc-1"},
			})
			var apiErr *core.APIError
			if !errors.As(err, &apiErr) {
				t.Fatalf("expected *core.APIError, got %v", err)
			}
			if apiErr.StatusCode != status {
				t.Fatalf("StatusCode = %d, want %d", apiErr.StatusCode, status)
			}
			if calls != 1 {
				t.Fatalf("calls = %d, want 1", calls)
			}
		})
	}
}

func TestDeleteCheckSendsNoBody(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Fatalf("method = %s", r.Method)
		}
		if r.URL.Path != "/vmonitor-uptime-manager/v1/uptimes/chk-1" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		if len(data) != 0 {
			t.Fatalf("body = %s, want empty", data)
		}
		w.WriteHeader(http.StatusNoContent)
	}))

	_, err := client.DeleteCheck(context.Background(), &DeleteCheckInput{CheckID: "chk-1"})
	if err != nil {
		t.Fatalf("DeleteCheck() error = %v", err)
	}
}

func TestDeleteCheckNotFound(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"not found"}`))
	}))

	_, err := client.DeleteCheck(context.Background(), &DeleteCheckInput{CheckID: "chk-1"})
	if !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("DeleteCheck() error = %v, want ErrNotFound", err)
	}
}

func TestDeleteCheckMissingCheckID(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("unexpected request for a missing CheckID")
	}))
	if _, err := client.DeleteCheck(context.Background(), &DeleteCheckInput{}); !errors.Is(err, core.ErrInvalidInput) {
		t.Fatalf("DeleteCheck() error = %v, want ErrInvalidInput", err)
	}
	if _, err := client.DeleteCheck(context.Background(), nil); !errors.Is(err, core.ErrInvalidInput) {
		t.Fatalf("DeleteCheck(nil) error = %v, want ErrInvalidInput", err)
	}
}

// TestWritePathIDs covers the design's required path ID checks for every
// new path operation that takes a CheckID: a shape-invalid value fails
// before any request.
func TestWritePathIDs(t *testing.T) {
	failIfCalled := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called")
	})

	for _, id := range []string{"..", ".", "/", ""} {
		t.Run("DeleteCheck rejects "+idLabel(id), func(t *testing.T) {
			client := newTestClient(t, failIfCalled)
			_, err := client.DeleteCheck(context.Background(), &DeleteCheckInput{CheckID: id})
			if !errors.Is(err, core.ErrInvalidInput) {
				t.Fatalf("DeleteCheck(%q) error = %v, want ErrInvalidInput", id, err)
			}
		})
	}
}

func idLabel(id string) string {
	if id == "" {
		return "empty"
	}
	return id
}
