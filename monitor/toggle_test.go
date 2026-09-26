package monitor

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/endpoints"
	"danny.vn/vngcloud/internal/transport"
)

// noopSleep never really waits: it lets a test that only cares about the
// outcome, not the wait timing, run fast regardless of how many confirm
// reads the case needs.
func noopSleep(context.Context, time.Duration) error { return nil }

// newToggleClient wires a Client at an httptest server through httpClient,
// bypassing New so the test can inject sleep. httpClient defaults to
// server.Client() when nil.
func newToggleClient(t *testing.T, server *httptest.Server, httpClient *http.Client, sleep sleepFunc) *Client {
	t.Helper()
	if httpClient == nil {
		httpClient = server.Client()
	}
	cfg := core.NewTestConfig("hcm-3", "", endpoints.Set{Monitor: server.URL + "/"},
		transport.New(transport.Config{HTTPClient: httpClient}))
	return &Client{c: core.ClientOf(cfg), sleep: sleep}
}

// checkBody is a minimal check JSON body for check "chk-1", the CheckID
// every test in this file uses.
func checkBody(status string) string {
	return fmt.Sprintf(`{"id":"chk-1","status":%q}`, status)
}

func TestPauseCheckAlreadyDisabled(t *testing.T) {
	var getCalls, putCalls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			getCalls.Add(1)
			_, _ = w.Write([]byte(checkBody(StatusDisabled)))
		case http.MethodPut:
			putCalls.Add(1)
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer server.Close()

	client := newToggleClient(t, server, nil, noopSleep)
	out, err := client.PauseCheck(context.Background(), &PauseCheckInput{CheckID: "chk-1"})
	if err != nil {
		t.Fatalf("PauseCheck() error = %v", err)
	}
	if out.Changed {
		t.Fatal("Changed = true, want false: the check was already disabled")
	}
	if putCalls.Load() != 0 {
		t.Fatalf("PUT calls = %d, want 0", putCalls.Load())
	}
	if getCalls.Load() != 1 {
		t.Fatalf("GET calls = %d, want 1", getCalls.Load())
	}
}

func TestResumeCheckAlreadyEnabled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			t.Fatal("unexpected PUT: the check was already enabled")
		}
		_, _ = w.Write([]byte(checkBody(StatusEnabled)))
	}))
	defer server.Close()

	client := newToggleClient(t, server, nil, noopSleep)
	out, err := client.ResumeCheck(context.Background(), &ResumeCheckInput{CheckID: "chk-1"})
	if err != nil {
		t.Fatalf("ResumeCheck() error = %v", err)
	}
	if out.Changed {
		t.Fatal("Changed = true, want false")
	}
}

// TestPauseCheckTogglesAndConfirmsAtOnce covers ENABLED -> DISABLED where
// the very first confirm read (the immediate one, no wait) already shows
// the target, so no sleep is ever called.
func TestPauseCheckTogglesAndConfirmsAtOnce(t *testing.T) {
	var getCalls, putCalls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			n := getCalls.Add(1)
			if n == 1 {
				_, _ = w.Write([]byte(checkBody(StatusEnabled)))
				return
			}
			_, _ = w.Write([]byte(checkBody(StatusDisabled)))
		case http.MethodPut:
			if putCalls.Add(1) > 1 {
				t.Error("PUT sent more than once")
			}
			if r.URL.Path != "/vmonitor-uptime-manager/v1/uptimes/status/chk-1" {
				t.Errorf("PUT path = %s", r.URL.Path)
			}
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer server.Close()

	client := newToggleClient(t, server, nil, func(context.Context, time.Duration) error {
		t.Fatal("sleep called: the first confirm read already showed the target")
		return nil
	})
	out, err := client.PauseCheck(context.Background(), &PauseCheckInput{CheckID: "chk-1"})
	if err != nil {
		t.Fatalf("PauseCheck() error = %v", err)
	}
	if !out.Changed {
		t.Fatal("Changed = false, want true")
	}
	if out.Check.Status != StatusDisabled {
		t.Fatalf("Check.Status = %s, want %s", out.Check.Status, StatusDisabled)
	}
	if putCalls.Load() != 1 {
		t.Fatalf("PUT calls = %d, want 1", putCalls.Load())
	}
	if getCalls.Load() != 2 {
		t.Fatalf("GET calls = %d, want 2 (the read-first and one confirm read)", getCalls.Load())
	}
}

// TestResumeCheckConfirmsAfterOneWait covers DISABLED -> ENABLED where the
// immediate confirm read still shows the old status and the target only
// shows up after one wait, so exactly one sleep of 1 second is requested.
func TestResumeCheckConfirmsAfterOneWait(t *testing.T) {
	var getCalls, putCalls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			n := getCalls.Add(1)
			switch n {
			case 1:
				_, _ = w.Write([]byte(checkBody(StatusDisabled)))
			case 2:
				_, _ = w.Write([]byte(checkBody(StatusDisabled)))
			default:
				_, _ = w.Write([]byte(checkBody(StatusEnabled)))
			}
		case http.MethodPut:
			putCalls.Add(1)
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer server.Close()

	var waits []time.Duration
	client := newToggleClient(t, server, nil, func(_ context.Context, d time.Duration) error {
		waits = append(waits, d)
		return nil
	})
	out, err := client.ResumeCheck(context.Background(), &ResumeCheckInput{CheckID: "chk-1"})
	if err != nil {
		t.Fatalf("ResumeCheck() error = %v", err)
	}
	if !out.Changed {
		t.Fatal("Changed = false, want true")
	}
	if putCalls.Load() != 1 {
		t.Fatalf("PUT calls = %d, want 1", putCalls.Load())
	}
	if len(waits) != 1 || waits[0] != time.Second {
		t.Fatalf("waits = %v, want [1s]", waits)
	}
}

func TestToggleUnexpectedStatusNoPUT(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			t.Fatal("unexpected PUT for an unknown status")
		}
		_, _ = w.Write([]byte(checkBody("PENDING")))
	}))
	defer server.Close()

	client := newToggleClient(t, server, nil, noopSleep)
	_, err := client.PauseCheck(context.Background(), &PauseCheckInput{CheckID: "chk-1"})
	if !errors.Is(err, ErrUnexpectedStatus) {
		t.Fatalf("PauseCheck() error = %v, want ErrUnexpectedStatus", err)
	}
}

func TestToggleInitialReadNotFoundNoPUT(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			t.Fatal("unexpected PUT after a not-found read")
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	client := newToggleClient(t, server, nil, noopSleep)
	_, err := client.PauseCheck(context.Background(), &PauseCheckInput{CheckID: "chk-1"})
	if !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("PauseCheck() error = %v, want ErrNotFound", err)
	}
}

// TestTogglePUTClientErrors covers every 4xx the design names for the toggle
// PUT: the server never acted, so the call returns that error directly
// after exactly one PUT and no confirm read.
func TestTogglePUTClientErrors(t *testing.T) {
	cases := []struct {
		name   string
		status int
		want   error
	}{
		{"401", http.StatusUnauthorized, core.ErrAuth},
		{"403", http.StatusForbidden, core.ErrPermission},
		{"409", http.StatusConflict, nil},
		{"429", http.StatusTooManyRequests, core.ErrRateLimited},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var getCalls, putCalls atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.Method {
				case http.MethodGet:
					getCalls.Add(1)
					_, _ = w.Write([]byte(checkBody(StatusEnabled)))
				case http.MethodPut:
					if putCalls.Add(1) > 1 {
						t.Error("PUT sent more than once")
					}
					w.WriteHeader(tc.status)
				}
			}))
			defer server.Close()

			client := newToggleClient(t, server, nil, func(context.Context, time.Duration) error {
				t.Fatal("sleep called: a 4xx on the PUT must return at once, with no confirm read")
				return nil
			})
			_, err := client.PauseCheck(context.Background(), &PauseCheckInput{CheckID: "chk-1"})
			if err == nil {
				t.Fatal("expected error")
			}
			if tc.want != nil && !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want to match %v", err, tc.want)
			}
			var apiErr *core.APIError
			if !errors.As(err, &apiErr) {
				t.Fatalf("error = %T, want *core.APIError", err)
			}
			if apiErr.StatusCode != tc.status {
				t.Fatalf("StatusCode = %d, want %d", apiErr.StatusCode, tc.status)
			}
			if putCalls.Load() != 1 {
				t.Fatalf("PUT calls = %d, want 1", putCalls.Load())
			}
			if getCalls.Load() != 1 {
				t.Fatalf("GET calls = %d, want 1 (no confirm read)", getCalls.Load())
			}
		})
	}
}

// failMethodTransport fails every request whose method is method with
// failErr, and sends every other request through inner. It lets a test
// simulate a dial failure or dropped connection for the toggle PUT alone,
// while the pre-toggle and confirm reads still reach the real server.
type failMethodTransport struct {
	inner   http.RoundTripper
	method  string
	failErr error
	calls   atomic.Int64
}

func (f *failMethodTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method == f.method {
		f.calls.Add(1)
		return nil, f.failErr
	}
	return f.inner.RoundTrip(req)
}

func TestTogglePUTFailedDialNoConfirm(t *testing.T) {
	var getCalls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		getCalls.Add(1)
		_, _ = w.Write([]byte(checkBody(StatusEnabled)))
	}))
	defer server.Close()

	rt := &failMethodTransport{
		inner:   server.Client().Transport,
		method:  http.MethodPut,
		failErr: &net.OpError{Op: "dial", Err: errors.New("refused")},
	}
	client := newToggleClient(t, server, &http.Client{Transport: rt}, func(context.Context, time.Duration) error {
		t.Fatal("sleep called: a failed dial must return at once, with no confirm read")
		return nil
	})
	_, err := client.PauseCheck(context.Background(), &PauseCheckInput{CheckID: "chk-1"})
	if err == nil {
		t.Fatal("expected error")
	}
	var apiErr *core.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error = %T, want *core.APIError", err)
	}
	if !apiErr.Retryable {
		t.Fatal("Retryable = false, want true for a failed dial")
	}
	if rt.calls.Load() != 1 {
		t.Fatalf("PUT attempts = %d, want 1", rt.calls.Load())
	}
	if getCalls.Load() != 1 {
		t.Fatalf("GET calls = %d, want 1 (no confirm read)", getCalls.Load())
	}
}

// TestTogglePUTServerErrorThenConfirmed and
// TestTogglePUTDroppedConnectionThenConfirmed cover the design's "2xx, 5xx,
// or failed after connecting" rule: neither a 502 nor a dropped connection
// on the PUT is returned directly; both fall through to a confirm read,
// which here shows the target at once.
func TestTogglePUTServerErrorThenConfirmed(t *testing.T) {
	testToggleAmbiguousPUTThenConfirmed(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	})
}

func TestTogglePUTDroppedConnectionThenConfirmed(t *testing.T) {
	testToggleAmbiguousPUTThenConfirmed(t, func(w http.ResponseWriter, r *http.Request) {
		hj, ok := w.(http.Hijacker)
		if !ok {
			panic("ResponseWriter does not support hijacking")
		}
		conn, _, err := hj.Hijack()
		if err != nil {
			panic(err)
		}
		_ = conn.Close()
	})
}

func testToggleAmbiguousPUTThenConfirmed(t *testing.T, putHandler http.HandlerFunc) {
	t.Helper()
	var getCalls, putCalls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			n := getCalls.Add(1)
			if n == 1 {
				_, _ = w.Write([]byte(checkBody(StatusEnabled)))
				return
			}
			_, _ = w.Write([]byte(checkBody(StatusDisabled)))
		case http.MethodPut:
			if putCalls.Add(1) > 1 {
				t.Error("PUT sent more than once")
			}
			putHandler(w, r)
		}
	}))
	defer server.Close()

	client := newToggleClient(t, server, nil, noopSleep)
	out, err := client.PauseCheck(context.Background(), &PauseCheckInput{CheckID: "chk-1"})
	if err != nil {
		t.Fatalf("PauseCheck() error = %v", err)
	}
	if !out.Changed {
		t.Fatal("Changed = false, want true: the confirm read showed the target")
	}
	if putCalls.Load() != 1 {
		t.Fatalf("PUT calls = %d, want 1", putCalls.Load())
	}
}

// TestToggleNeverConfirms covers a 502 on the PUT followed by four confirm
// reads that never show the target: the call ends in ErrStatusUnconfirmed
// after waits of 1, 2, and 4 seconds, and the PUT is still sent only once.
func TestToggleNeverConfirms(t *testing.T) {
	var getCalls, putCalls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			getCalls.Add(1)
			_, _ = w.Write([]byte(checkBody(StatusEnabled)))
		case http.MethodPut:
			if putCalls.Add(1) > 1 {
				t.Error("PUT sent more than once")
			}
			w.WriteHeader(http.StatusBadGateway)
		}
	}))
	defer server.Close()

	var waits []time.Duration
	client := newToggleClient(t, server, nil, func(_ context.Context, d time.Duration) error {
		waits = append(waits, d)
		return nil
	})
	_, err := client.PauseCheck(context.Background(), &PauseCheckInput{CheckID: "chk-1"})
	if !errors.Is(err, ErrStatusUnconfirmed) {
		t.Fatalf("PauseCheck() error = %v, want ErrStatusUnconfirmed", err)
	}
	if putCalls.Load() != 1 {
		t.Fatalf("PUT calls = %d, want 1", putCalls.Load())
	}
	// getCalls: 1 pre-toggle read + 4 confirm reads.
	if getCalls.Load() != 5 {
		t.Fatalf("GET calls = %d, want 5", getCalls.Load())
	}
	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second}
	if len(waits) != len(want) {
		t.Fatalf("waits = %v, want %v", waits, want)
	}
	for i, w := range want {
		if waits[i] != w {
			t.Fatalf("waits = %v, want %v", waits, want)
		}
	}
	var apiErr *core.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error does not unwrap to *core.APIError: %v", err)
	}
	if apiErr.StatusCode != http.StatusBadGateway {
		t.Fatalf("unwrapped StatusCode = %d, want %d: ErrStatusUnconfirmed must still expose the PUT's own error", apiErr.StatusCode, http.StatusBadGateway)
	}
	// A pause's pre-toggle read already proved the check was StatusEnabled,
	// so the recovery text treats the pause as done rather than pointing at
	// a person or telling the caller to rerun.
	if !strings.Contains(err.Error(), "treat the pause as done and resume later") {
		t.Fatalf("error = %q, want it to tell the caller to treat the pause as done", err.Error())
	}
}

// TestResumeCheckNeverConfirms is TestToggleNeverConfirms' counterpart for
// ResumeCheck: a 502 on the PUT followed by four confirm reads that never
// show ENABLED still sends the PUT only once and ends in
// ErrStatusUnconfirmed, whose message points at a person instead of telling
// the caller to rerun, since a resume's pre-toggle read gives no proof the
// check was ever DISABLED to begin with.
func TestResumeCheckNeverConfirms(t *testing.T) {
	var getCalls, putCalls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			getCalls.Add(1)
			_, _ = w.Write([]byte(checkBody(StatusDisabled)))
		case http.MethodPut:
			if putCalls.Add(1) > 1 {
				t.Error("PUT sent more than once")
			}
			w.WriteHeader(http.StatusBadGateway)
		}
	}))
	defer server.Close()

	var waits []time.Duration
	client := newToggleClient(t, server, nil, func(_ context.Context, d time.Duration) error {
		waits = append(waits, d)
		return nil
	})
	_, err := client.ResumeCheck(context.Background(), &ResumeCheckInput{CheckID: "chk-1"})
	if !errors.Is(err, ErrStatusUnconfirmed) {
		t.Fatalf("ResumeCheck() error = %v, want ErrStatusUnconfirmed", err)
	}
	if putCalls.Load() != 1 {
		t.Fatalf("PUT calls = %d, want 1", putCalls.Load())
	}
	// getCalls: 1 pre-toggle read + 4 confirm reads.
	if getCalls.Load() != 5 {
		t.Fatalf("GET calls = %d, want 5", getCalls.Load())
	}
	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second}
	if len(waits) != len(want) {
		t.Fatalf("waits = %v, want %v", waits, want)
	}
	if !strings.Contains(err.Error(), "ask a person to check it") {
		t.Fatalf("error = %q, want it to point at a person, not a rerun", err.Error())
	}
}

// TestResumeCheckPUTClientErrors is TestTogglePUTClientErrors' counterpart
// for ResumeCheck: the pre-toggle read shows DISABLED, so every case still
// sends the PUT and returns that 4xx directly with no confirm read.
func TestResumeCheckPUTClientErrors(t *testing.T) {
	cases := []struct {
		name   string
		status int
		want   error
	}{
		{"401", http.StatusUnauthorized, core.ErrAuth},
		{"403", http.StatusForbidden, core.ErrPermission},
		{"409", http.StatusConflict, nil},
		{"429", http.StatusTooManyRequests, core.ErrRateLimited},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var getCalls, putCalls atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.Method {
				case http.MethodGet:
					getCalls.Add(1)
					_, _ = w.Write([]byte(checkBody(StatusDisabled)))
				case http.MethodPut:
					if putCalls.Add(1) > 1 {
						t.Error("PUT sent more than once")
					}
					w.WriteHeader(tc.status)
				}
			}))
			defer server.Close()

			client := newToggleClient(t, server, nil, func(context.Context, time.Duration) error {
				t.Fatal("sleep called: a 4xx on the PUT must return at once, with no confirm read")
				return nil
			})
			_, err := client.ResumeCheck(context.Background(), &ResumeCheckInput{CheckID: "chk-1"})
			if err == nil {
				t.Fatal("expected error")
			}
			if tc.want != nil && !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want to match %v", err, tc.want)
			}
			var apiErr *core.APIError
			if !errors.As(err, &apiErr) {
				t.Fatalf("error = %T, want *core.APIError", err)
			}
			if apiErr.StatusCode != tc.status {
				t.Fatalf("StatusCode = %d, want %d", apiErr.StatusCode, tc.status)
			}
			if putCalls.Load() != 1 {
				t.Fatalf("PUT calls = %d, want 1", putCalls.Load())
			}
			if getCalls.Load() != 1 {
				t.Fatalf("GET calls = %d, want 1 (no confirm read)", getCalls.Load())
			}
		})
	}
}

// TestResumeCheckPUTCancelledContext is TestTogglePUTCancelledContext's
// counterpart for ResumeCheck.
func TestResumeCheckPUTCancelledContext(t *testing.T) {
	var putCalls atomic.Int64
	ctx, cancel := context.WithCancel(context.Background())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			_, _ = w.Write([]byte(checkBody(StatusDisabled)))
		case http.MethodPut:
			if putCalls.Add(1) > 1 {
				t.Error("PUT sent more than once")
			}
			cancel()
		}
	}))
	defer server.Close()

	client := newToggleClient(t, server, nil, noopSleep)
	_, err := client.ResumeCheck(ctx, &ResumeCheckInput{CheckID: "chk-1"})
	if !errors.Is(err, ErrStatusUnconfirmed) {
		t.Fatalf("ResumeCheck() error = %v, want ErrStatusUnconfirmed", err)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ResumeCheck() error = %v, want it to wrap context.Canceled", err)
	}
	if putCalls.Load() != 1 {
		t.Fatalf("PUT calls = %d, want 1", putCalls.Load())
	}
}

// TestTogglePUTDroppedConnectionNeverConfirms covers a dropped connection on
// the PUT followed by confirm reads that never show the target: unlike
// TestTogglePUTDroppedConnectionThenConfirmed, every confirm read here still
// shows the pre-toggle status, so the call ends in ErrStatusUnconfirmed
// rather than Changed: true, and the PUT is still sent only once.
func TestTogglePUTDroppedConnectionNeverConfirms(t *testing.T) {
	var getCalls, putCalls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			getCalls.Add(1)
			_, _ = w.Write([]byte(checkBody(StatusEnabled)))
		case http.MethodPut:
			if putCalls.Add(1) > 1 {
				t.Error("PUT sent more than once")
			}
			hj, ok := w.(http.Hijacker)
			if !ok {
				panic("ResponseWriter does not support hijacking")
			}
			conn, _, err := hj.Hijack()
			if err != nil {
				panic(err)
			}
			_ = conn.Close()
		}
	}))
	defer server.Close()

	client := newToggleClient(t, server, nil, noopSleep)
	_, err := client.PauseCheck(context.Background(), &PauseCheckInput{CheckID: "chk-1"})
	if !errors.Is(err, ErrStatusUnconfirmed) {
		t.Fatalf("PauseCheck() error = %v, want ErrStatusUnconfirmed", err)
	}
	if putCalls.Load() != 1 {
		t.Fatalf("PUT calls = %d, want 1", putCalls.Load())
	}
	// getCalls: 1 pre-toggle read + 4 confirm reads, every one still ENABLED.
	if getCalls.Load() != 5 {
		t.Fatalf("GET calls = %d, want 5", getCalls.Load())
	}
}

// TestTogglePUTCancelledContext covers a context canceled mid-PUT: Once
// means the PUT is still sent only once, and the same canceled context then
// fails every confirm read, ending in ErrStatusUnconfirmed.
func TestTogglePUTCancelledContext(t *testing.T) {
	var getCalls, putCalls atomic.Int64
	ctx, cancel := context.WithCancel(context.Background())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			getCalls.Add(1)
			_, _ = w.Write([]byte(checkBody(StatusEnabled)))
		case http.MethodPut:
			if putCalls.Add(1) > 1 {
				t.Error("PUT sent more than once")
			}
			cancel()
			// The client's context is now canceled; whether this response
			// ever reaches it is irrelevant, so nothing more is written.
		}
	}))
	defer server.Close()

	client := newToggleClient(t, server, nil, noopSleep)
	_, err := client.PauseCheck(ctx, &PauseCheckInput{CheckID: "chk-1"})
	if !errors.Is(err, ErrStatusUnconfirmed) {
		t.Fatalf("PauseCheck() error = %v, want ErrStatusUnconfirmed", err)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("PauseCheck() error = %v, want it to wrap context.Canceled", err)
	}
	if putCalls.Load() != 1 {
		t.Fatalf("PUT calls = %d, want 1", putCalls.Load())
	}
}

// TestToggleSerializesUnderMutex checks that two concurrent PauseCheck calls
// on one Client never interleave their read-toggle-confirm sequences: the
// server only ever sees one in-flight request at a time from this Client.
// The check starts ENABLED, so whichever goroutine's pre-toggle read wins
// the mutex first actually sends a PUT and a confirm read, not just a GET
// that short-circuits on an already-matching status; every later goroutine
// then reads the now-DISABLED status and returns without a PUT of its own.
// If toggleMu ever let two goroutines's requests overlap, this flips to
// ENABLED under a concurrent PUT would race maxInFlight past 1.
func TestToggleSerializesUnderMutex(t *testing.T) {
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
		switch r.Method {
		case http.MethodGet:
			mu.Lock()
			current := status
			mu.Unlock()
			_, _ = w.Write([]byte(checkBody(current)))
		case http.MethodPut:
			mu.Lock()
			status = StatusDisabled
			mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer server.Close()

	client := newToggleClient(t, server, nil, noopSleep)
	done := make(chan struct{})
	for range 5 {
		go func() {
			defer func() { done <- struct{}{} }()
			if _, err := client.PauseCheck(context.Background(), &PauseCheckInput{CheckID: "chk-1"}); err != nil {
				t.Errorf("PauseCheck() error = %v", err)
			}
		}()
	}
	for range 5 {
		<-done
	}
	if maxInFlight.Load() > 1 {
		t.Fatalf("max in-flight requests = %d, want 1: toggleMu must serialize PauseCheck calls", maxInFlight.Load())
	}

	out, err := client.GetCheck(context.Background(), &GetCheckInput{CheckID: "chk-1"})
	if err != nil {
		t.Fatalf("GetCheck() error = %v", err)
	}
	if out.Check.Status != StatusDisabled {
		t.Fatalf("final status = %s, want %s: exactly one PauseCheck call must have sent the PUT", out.Check.Status, StatusDisabled)
	}
}

func TestPauseCheckMissingCheckID(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("unexpected request for a missing CheckID")
	}))
	if _, err := client.PauseCheck(context.Background(), &PauseCheckInput{}); !errors.Is(err, core.ErrInvalidInput) {
		t.Fatalf("PauseCheck() error = %v, want ErrInvalidInput", err)
	}
	if _, err := client.ResumeCheck(context.Background(), &ResumeCheckInput{}); !errors.Is(err, core.ErrInvalidInput) {
		t.Fatalf("ResumeCheck() error = %v, want ErrInvalidInput", err)
	}
}

func TestPauseCheckPathIDRejection(t *testing.T) {
	for _, id := range []string{"..", ".", "/", ""} {
		t.Run(fmt.Sprintf("pause %q", id), func(t *testing.T) {
			client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				t.Fatal("unexpected request for a rejected CheckID")
			}))
			_, err := client.PauseCheck(context.Background(), &PauseCheckInput{CheckID: id})
			if !errors.Is(err, core.ErrInvalidInput) {
				t.Fatalf("PauseCheck(%q) error = %v, want ErrInvalidInput", id, err)
			}
		})
		t.Run(fmt.Sprintf("resume %q", id), func(t *testing.T) {
			client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				t.Fatal("unexpected request for a rejected CheckID")
			}))
			_, err := client.ResumeCheck(context.Background(), &ResumeCheckInput{CheckID: id})
			if !errors.Is(err, core.ErrInvalidInput) {
				t.Fatalf("ResumeCheck(%q) error = %v, want ErrInvalidInput", id, err)
			}
		})
	}
}

// fakeTokenSource issues sequential tokens and records every value
// Invalidate is called with. Every other test in this file wires its
// Client through newToggleClient or newTestClient, whose transport has no
// TokenSource at all, so transport.doAuthenticated's 401 branch, and
// invalidateOnce inside it, never run: EnsureToken is a no-op and the
// synthetic-401 check on an empty token is skipped for a nil source. This
// type lets a test build a monitor Client with a real one instead, so a
// 401 on the toggle PUT actually reaches invalidateOnce's Once branch.
type fakeTokenSource struct {
	count       atomic.Int64
	invalidated []string
}

func (s *fakeTokenSource) Token(context.Context) (transport.Token, error) {
	n := s.count.Add(1)
	return transport.Token{AccessToken: fmt.Sprintf("token-%d", n), ExpiresAt: time.Now().Add(time.Hour)}, nil
}

func (s *fakeTokenSource) Invalidate(token string) {
	s.invalidated = append(s.invalidated, token)
}

// TestTogglePUT401InvalidatesTokenOnce checks the monitor path with a real
// TokenSource: a 401 on the toggle PUT invalidates the token it sent, the
// same as an ordinary request would, but the Once request is never resent
// with a refreshed one.
func TestTogglePUT401InvalidatesTokenOnce(t *testing.T) {
	var getCalls, putCalls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			getCalls.Add(1)
			_, _ = w.Write([]byte(checkBody(StatusEnabled)))
		case http.MethodPut:
			if putCalls.Add(1) > 1 {
				t.Error("PUT sent more than once")
			}
			w.WriteHeader(http.StatusUnauthorized)
		}
	}))
	defer server.Close()

	source := &fakeTokenSource{}
	tc := transport.New(transport.Config{HTTPClient: server.Client(), TokenSource: source})
	cfg := core.NewTestConfig("hcm-3", "", endpoints.Set{Monitor: server.URL + "/"}, tc)
	client := &Client{c: core.ClientOf(cfg), sleep: noopSleep}

	_, err := client.PauseCheck(context.Background(), &PauseCheckInput{CheckID: "chk-1"})
	if !errors.Is(err, core.ErrAuth) {
		t.Fatalf("PauseCheck() error = %v, want core.ErrAuth", err)
	}
	if putCalls.Load() != 1 {
		t.Fatalf("PUT calls = %d, want 1: a Once request must never resend after a 401", putCalls.Load())
	}
	if len(source.invalidated) != 1 || source.invalidated[0] != "token-1" {
		t.Fatalf("invalidated = %v, want [\"token-1\"]", source.invalidated)
	}
}
