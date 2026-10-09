package transport

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

const testAPIKey = "<secret>-api-key-value"

// failTokens fails the test on any call, proving an APIKey request never
// touches the token flow.
type failTokens struct{ t *testing.T }

func (f failTokens) Token(context.Context) (Token, error) {
	f.t.Helper()
	f.t.Error("token source called for an APIKey request")
	return Token{AccessToken: "iam-token"}, nil
}

func (f failTokens) Invalidate(string) {
	f.t.Helper()
	f.t.Error("token source invalidated for an APIKey request")
}

func TestAPIKeySendsBearerWithoutToken(t *testing.T) {
	var got string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()
	client := New(Config{HTTPClient: server.Client(), TokenSource: failTokens{t}})
	req := Request{Operation: "t", URL: server.URL, APIKey: testAPIKey, Headers: map[string]string{"Authorization": "Bearer other"}}
	if err := client.DoJSON(context.Background(), req, &struct{}{}); err != nil {
		t.Fatal(err)
	}
	if got != "Bearer "+testAPIKey {
		t.Fatalf("Authorization = %q", got)
	}
}

func TestAPIKey401IsNotRetriedOrInvalidated(t *testing.T) {
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()
	client := New(Config{HTTPClient: server.Client(), TokenSource: failTokens{t}, RetryCount: 3})
	err := client.DoJSON(context.Background(), Request{Operation: "t", URL: server.URL, APIKey: testAPIKey}, &struct{}{})
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusUnauthorized {
		t.Fatalf("err = %v, want 401 APIError", err)
	}
	if n := requests.Load(); n != 1 {
		t.Fatalf("requests = %d, want 1", n)
	}
}

func TestAPIKey401WithOnceIsNotInvalidated(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()
	client := New(Config{HTTPClient: server.Client(), TokenSource: failTokens{t}})
	_ = client.DoJSON(context.Background(), Request{Operation: "t", URL: server.URL, APIKey: testAPIKey, Once: true}, &struct{}{})
}

func TestAPIKeyWithSkipAuthFailsBeforeRequest(t *testing.T) {
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
	}))
	defer server.Close()
	client := New(Config{HTTPClient: server.Client()})
	err := client.DoJSON(context.Background(), Request{Operation: "t", URL: server.URL, APIKey: testAPIKey, SkipAuth: true}, nil)
	if err == nil || strings.Contains(err.Error(), testAPIKey) {
		t.Fatalf("err = %v, want an error without the key", err)
	}
	if requests.Load() != 0 {
		t.Fatal("a request was sent")
	}
	if _, _, _, rawErr := client.DoRaw(context.Background(), Request{Operation: "t", URL: server.URL, APIKey: testAPIKey, SkipAuth: true}); rawErr == nil {
		t.Fatal("DoRaw accepted APIKey with SkipAuth")
	}
}

func TestAPIKeyRedactedFromErrorsCaptureAndLog(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"title":"Bad Request","detail":"bad token ` + testAPIKey + ` given","code":"` + testAPIKey + `"}`))
	}))
	defer server.Close()

	var logs bytes.Buffer
	var captured []byte
	client := New(Config{
		HTTPClient: server.Client(),
		Logger:     slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
		Capture:    func(c Capture) { captured = c.Body },
	})
	err := client.DoJSON(context.Background(), Request{Operation: "t", URL: server.URL + "/x?k=1", APIKey: testAPIKey}, nil)
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v", err)
	}
	for name, text := range map[string]string{
		"message": apiErr.Message, "code": apiErr.Code, "error": err.Error(), "log": logs.String(), "capture": string(captured),
	} {
		if strings.Contains(text, testAPIKey) {
			t.Errorf("%s holds the key: %q", name, text)
		}
	}
	if !strings.Contains(apiErr.Message, "bad token") {
		t.Errorf("message = %q, want the rest of the detail kept", apiErr.Message)
	}
}

func TestDecodeErrorTitleIsLastFallback(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{"title only", `{"title":"Method Not Allowed","status":405}`, "Method Not Allowed"},
		{"detail wins", `{"title":"Not Found","detail":"No static resource x."}`, "No static resource x."},
		{"message wins", `{"title":"t","message":"m","detail":"d"}`, "m"},
		{"error wins over title", `{"title":"t","error":"e"}`, "e"},
		{"no body", ``, "Method Not Allowed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := decodeError(Request{Operation: "t"}, http.StatusMethodNotAllowed, []byte(tc.body))
			var apiErr *APIError
			if !errors.As(err, &apiErr) || apiErr.Message != tc.want {
				t.Fatalf("Message = %v, want %q", err, tc.want)
			}
		})
	}
}
