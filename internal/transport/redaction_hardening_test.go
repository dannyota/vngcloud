package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCaptureMalformedEscapesAndNumericCredentials(t *testing.T) {
	for _, status := range []int{200, 400} {
		for _, body := range []string{`{"value":"\u0073ynthetic-token-1"}garbage`, `{"value":1234567890123456}`} {
			var captured []byte
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(status); _, _ = w.Write([]byte(body)) }))
			c := New(Config{HTTPClient: server.Client(), TokenSource: &longTokens{}, Capture: func(c Capture) { captured = c.Body }})
			req := Request{URL: server.URL}
			if strings.Contains(body, "1234567890123456") {
				req.APIKey = "1234567890123456"
			}
			_ = c.DoJSON(context.Background(), req, nil)
			server.Close()
			if strings.Contains(string(captured), `\u0073ynthetic-token-1`) || strings.Contains(string(captured), "1234567890123456") {
				t.Errorf("capture retained recoverable credential: %s", captured)
			}
			if strings.Contains(body, "garbage") && string(captured) != redactedText {
				t.Errorf("malformed capture = %q", captured)
			}
		}
	}
}

func TestRejectStructuredErrorCodes(t *testing.T) {
	for _, code := range []string{`{"value":"\u0073ynthetic-token-1"}`, `["\u0073ynthetic-token-1"]`, `true`} {
		err := decodeError(Request{}, 400, []byte(`{"code":`+code+`,"message":"bad"}`))
		var ae *APIError
		if !errors.As(err, &ae) {
			t.Fatal(err)
		}
		if ae.Code != "" {
			t.Errorf("unsupported code = %q", ae.Code)
		}
	}
}

func TestRedirectRedactsRawHostBeforeQuoting(t *testing.T) {
	secret := "synthetic\u200bcredential"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://"+secret+".invalid/private?secret="+secret, http.StatusFound)
	}))
	defer server.Close()
	c := New(Config{HTTPClient: server.Client()})
	err := c.DoJSON(context.Background(), Request{URL: server.URL, APIKey: secret}, nil)
	if err == nil {
		t.Fatal("expected cross-host refusal")
	}
	if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), `synthetic\u200bcredential`) {
		t.Fatalf("escaped credential leaked: %s", err)
	}
	if !strings.Contains(err.Error(), ".invalid") {
		t.Fatal("hostname lost")
	}
}

func TestDebugPathRedactsAttemptValues(t *testing.T) {
	var logs bytes.Buffer
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{}`)) }))
	defer server.Close()
	c := New(Config{HTTPClient: server.Client(), TokenSource: &longTokens{}, Logger: slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))})
	if err := c.DoJSON(context.Background(), Request{URL: server.URL + "/synthetic-token-1/synthetic-explicit-secret", Redact: []string{"synthetic-explicit-secret"}}, new(json.RawMessage)); err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"synthetic-token-1", "synthetic-explicit-secret"} {
		if strings.Contains(logs.String(), secret) {
			t.Errorf("debug path leaked %s", secret)
		}
	}
}

func TestCaptureWithoutSecretsKeepsMalformedUnicode(t *testing.T) {
	body := []byte(`{"text":"\u0061"}garbage`)
	if got := (Request{Redact: []string{""}}).redactBody(body); !bytes.Equal(got, body) {
		t.Fatalf("capture without secrets changed: %s", got)
	}
}
