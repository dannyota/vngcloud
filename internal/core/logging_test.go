package core

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"danny.vn/vngcloud/internal/transport"
)

// capturedRecord is one slog record's message and attributes, flattened to a
// map so a test can check exactly which keys were logged.
type capturedRecord struct {
	msg   string
	attrs map[string]any
}

// recordingHandler is a slog.Handler that stores every record it receives,
// including at Debug level, so a test can inspect exactly what the SDK
// logged through WithLogger without depending on text formatting.
type recordingHandler struct {
	mu      sync.Mutex
	records *[]capturedRecord
}

func newRecordingLogger() (*slog.Logger, *[]capturedRecord) {
	records := &[]capturedRecord{}
	h := &recordingHandler{records: records}
	return slog.New(h), records
}

func (h *recordingHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *recordingHandler) Handle(_ context.Context, r slog.Record) error {
	attrs := map[string]any{}
	r.Attrs(func(a slog.Attr) bool {
		attrs[a.Key] = a.Value.Any()
		return true
	})
	h.mu.Lock()
	defer h.mu.Unlock()
	*h.records = append(*h.records, capturedRecord{msg: r.Message, attrs: attrs})
	return nil
}

func (h *recordingHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *recordingHandler) WithGroup(string) slog.Handler      { return h }

func recordsNamed(records []capturedRecord, msg string) []capturedRecord {
	var out []capturedRecord
	for _, r := range records {
		if r.msg == msg {
			out = append(out, r)
		}
	}
	return out
}

func keys(m map[string]any) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	return ks
}

// TestDebugLoggingLoginAndRequest drives a full login through a query-string
// API call and checks that WithLogger sees exactly: "login started" (no
// attrs), "login finished" with only "ok", and one "request" record per HTTP
// attempt with only "method", "path" (no query string), "status", and
// "duration". It also checks that login's own GET/POST calls never produce a
// "request" record.
func TestDebugLoggingLoginAndRequest(t *testing.T) {
	var tokenURLRef string
	loginGETs, loginPOSTs := 0, 0
	signin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			loginGETs++
			_, _ = w.Write([]byte(`<html><input name="_csrf" value="csrf-login"></html>`))
		case http.MethodPost:
			loginPOSTs++
			http.Redirect(w, r, tokenURLRef+"/callback?code=auth-code", http.StatusFound)
		}
	}))
	defer signin.Close()

	token := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"accessToken":"tok-1","expiresIn":3600}`))
	}))
	defer token.Close()
	tokenURLRef = token.URL

	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer api.Close()

	logger, records := newRecordingLogger()
	c, err := newClient(
		WithRegion("hcm-3"),
		WithLogger(logger),
		WithIAMUser(&IAMUserAuth{RootEmail: "root@example.test", Username: "user", Password: "pass"}),
		WithEndpointOverrides(EndpointOverrides{Signin: signin.URL, Dashboard: token.URL + "/"}),
	)
	if err != nil {
		t.Fatalf("newClient() error = %v", err)
	}

	status, err := c.DoJSONStatus(context.Background(), transport.Request{
		Operation: "Test.Op",
		Method:    http.MethodGet,
		URL:       api.URL + "/servers?secret=do-not-log",
		OK:        []int{http.StatusOK},
	}, nil)
	if err != nil {
		t.Fatalf("DoJSONStatus() error = %v", err)
	}
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}

	started := recordsNamed(*records, "login started")
	if len(started) != 1 {
		t.Fatalf("login started records = %d, want 1: %+v", len(started), *records)
	}
	if len(started[0].attrs) != 0 {
		t.Fatalf("login started attrs = %v, want none", started[0].attrs)
	}

	finished := recordsNamed(*records, "login finished")
	if len(finished) != 1 {
		t.Fatalf("login finished records = %d, want 1: %+v", len(finished), *records)
	}
	if ok, has := finished[0].attrs["ok"]; !has || ok != true {
		t.Fatalf("login finished attrs = %v, want {ok: true}", finished[0].attrs)
	}
	if len(finished[0].attrs) != 1 {
		t.Fatalf("login finished attrs = %v, want only ok", finished[0].attrs)
	}

	requests := recordsNamed(*records, "request")
	if len(requests) != 1 {
		t.Fatalf("request records = %d, want 1: %+v", len(requests), *records)
	}
	req := requests[0]
	wantKeys := map[string]bool{"method": true, "path": true, "status": true, "duration": true}
	if len(req.attrs) != len(wantKeys) {
		t.Fatalf("request attrs = %v, want exactly %v", keys(req.attrs), wantKeys)
	}
	for k := range wantKeys {
		if _, ok := req.attrs[k]; !ok {
			t.Fatalf("request attrs = %v, missing %q", req.attrs, k)
		}
	}
	if req.attrs["method"] != http.MethodGet {
		t.Fatalf("method = %v, want GET", req.attrs["method"])
	}
	if req.attrs["path"] != "/servers" {
		t.Fatalf("path = %v, want /servers (no query string)", req.attrs["path"])
	}
	if req.attrs["status"] != int64(http.StatusOK) && req.attrs["status"] != http.StatusOK {
		t.Fatalf("status = %v, want 200", req.attrs["status"])
	}

	if loginGETs == 0 || loginPOSTs == 0 {
		t.Fatal("login flow did not run")
	}
	// Login's own GET/POST calls (loginGETs + loginPOSTs, plus the token
	// exchange) must never appear as "request" records: only the one real
	// API call above does.
	if len(requests) != 1 {
		t.Fatalf("request records = %d, want exactly 1 (login's own HTTP calls must not be logged)", len(requests))
	}
}

// TestDebugLoggingNilLoggerLogsNothing checks that without WithLogger, no
// logger is ever consulted: a login and a request succeed exactly the same,
// and there is nothing to assert on because no records exist. This test
// exists mainly to document the "nil logger" contract explicitly and to
// exercise the same path without a logger, to catch a panic from an
// unguarded nil dereference.
func TestDebugLoggingNilLoggerLogsNothing(t *testing.T) {
	var tokenURLRef string
	signin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			_, _ = w.Write([]byte(`<html><input name="_csrf" value="csrf-login"></html>`))
		case http.MethodPost:
			http.Redirect(w, r, tokenURLRef+"/callback?code=auth-code", http.StatusFound)
		}
	}))
	defer signin.Close()
	token := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"accessToken":"tok-1","expiresIn":3600}`))
	}))
	defer token.Close()
	tokenURLRef = token.URL

	c, err := newClient(
		WithRegion("hcm-3"),
		WithIAMUser(&IAMUserAuth{RootEmail: "root@example.test", Username: "user", Password: "pass"}),
		WithEndpointOverrides(EndpointOverrides{Signin: signin.URL, Dashboard: token.URL + "/"}),
	)
	if err != nil {
		t.Fatalf("newClient() error = %v", err)
	}
	if err := c.Authenticate(context.Background()); err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
}
