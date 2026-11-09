package billing

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/endpoints"
	"danny.vn/vngcloud/internal/testutil"
	"danny.vn/vngcloud/internal/transport"
)

func TestHistorySentCredentials(t *testing.T) {
	historyKinds(t, func(t *testing.T, payments bool, _ string) {
		for _, reverse := range []bool{false, true} {
			first, second := "synthetic-shared-token", "synthetic-shared-token-SECRET-TAIL"
			if reverse {
				first, second = second, first
			}
			var calls atomic.Int32
			var logs bytes.Buffer
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if calls.Add(1) == 1 {
					w.WriteHeader(401)
					_, _ = w.Write([]byte(`{"message":"synthetic-account"}`))
					return
				}
				page := 1
				if r.URL.Query().Get("pageNumber") == "2" {
					page = 2
				}
				obj := historyObject(historyBody(payments, page, 1, 2, page))
				row := obj["data"].(map[string]any)["content"].([]any)[0].(map[string]any)
				row["description"] = first + " " + second
				if payments {
					row["transactionID"] = first
					row["payGate"] = second
					row["paymentStatus"] = first
				} else {
					row["sourceReference"] = first
					row["userServiceReference"] = second
				}
				_, _ = w.Write([]byte(historyJSON(obj)))
			}))
			defer server.Close()
			tc := transport.New(transport.Config{HTTPClient: server.Client(), TokenSource: &credentialTestTokens{values: []string{first, second}}, Logger: slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))})
			c := New(core.NewTestConfig("hcm-3", "", endpoints.Set{Billing: server.URL + "/"}, tc))
			out, err := historyRead(t, context.Background(), c, payments, &historyOptions{start: "2024-01-01", end: "2024-01-01", size: 1, all: true})
			if err != nil || out == nil || calls.Load() != 3 {
				t.Fatal("401 recovery walk failed")
			}
			encoded := historyJSON(out)
			for _, secret := range []string{first, second, "SECRET-TAIL", "synthetic-account"} {
				if strings.Contains(encoded+logs.String(), secret) {
					t.Fatal("credential or account text escaped")
				}
			}
		}
	})
}

func TestHistoryCanceledAfterResponse(t *testing.T) {
	historyKinds(t, func(t *testing.T, payments bool, _ string) {
		for _, rows := range []int{0, 1} {
			ctx, cancel := context.WithCancel(context.Background())
			ids := []int{}
			if rows == 1 {
				ids = append(ids, 1)
			}
			cfg := testutil.NewConfigWithCapture(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(historyBody(payments, 1, 50, rows, ids...)))
			}), func(transport.Capture) { cancel() })
			out, err := historyRead(t, ctx, New(cfg), payments, &historyOptions{start: "2024-01-01", end: "2024-01-01", all: true})
			cancel()
			if out != nil || !errors.Is(err, context.Canceled) || errors.Is(err, ErrHistoryIncomplete) {
				t.Fatal("canceled row processing or empty aggregation returned output")
			}
		}
	})
}

func TestHistoryCanceledRetry(t *testing.T) {
	historyKinds(t, func(t *testing.T, payments bool, _ string) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		var calls atomic.Int32
		cfg := testutil.NewRetryConfig(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			calls.Add(1)
			w.Header().Set("Retry-After", "30")
			w.WriteHeader(429)
			w.(http.Flusher).Flush()
			cancel()
		}))
		out, err := historyRead(t, ctx, New(cfg), payments, &historyOptions{start: "2024-01-01", end: "2024-01-01"})
		if out != nil || !errors.Is(err, context.Canceled) || calls.Load() != 1 {
			t.Fatal("canceled retry sent another request or lost context identity")
		}
	})
}

func TestHistoryConcurrentCredentialRotation(t *testing.T) {
	historyKinds(t, func(t *testing.T, payments bool, _ string) {
		const first = "synthetic-first-token"
		const later = "synthetic-later-token"
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		blocked, release := make(chan struct{}), make(chan struct{})
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			q := r.URL.Query()
			if q.Get("dateStart") == "02/01/2024" {
				if r.Header.Get("Authorization") == "Bearer "+first {
					w.WriteHeader(401)
					return
				}
				_, _ = w.Write([]byte(historyBody(payments, 1, 50, 0)))
				return
			}
			page := 1
			if q.Get("pageNumber") == "1" {
				close(blocked)
				select {
				case <-release:
				case <-r.Context().Done():
					return
				}
			} else {
				page = 2
			}
			obj := historyObject(historyBody(payments, page, 1, 2, page))
			obj["data"].(map[string]any)["content"].([]any)[0].(map[string]any)["description"] = first + " " + later
			_, _ = w.Write([]byte(historyJSON(obj)))
		}))
		defer server.Close()
		tc := transport.New(transport.Config{HTTPClient: server.Client(), TokenSource: &credentialTestTokens{values: []string{first, later}}})
		c := New(core.NewTestConfig("hcm-3", "", endpoints.Set{Billing: server.URL + "/"}, tc))
		type result struct {
			out any
			err error
		}
		done := make(chan result, 1)
		go func() {
			if payments {
				out, err := c.ListPayments(ctx, &ListPaymentsInput{StartDate: "2024-01-01", EndDate: "2024-01-01", Size: 1, All: true})
				done <- result{out, err}
			} else {
				out, err := c.ListTransactions(ctx, &ListTransactionsInput{StartDate: "2024-01-01", EndDate: "2024-01-01", Size: 1, All: true})
				done <- result{out, err}
			}
		}()
		select {
		case <-blocked:
		case <-ctx.Done():
			t.Fatal("first history request did not start")
		}
		_, err := historyRead(t, ctx, c, payments, &historyOptions{start: "2024-01-02", end: "2024-01-02"})
		close(release)
		if err != nil {
			t.Fatal("concurrent credential refresh failed")
		}
		select {
		case got := <-done:
			if got.err != nil {
				t.Fatal("concurrent history walk failed")
			}
			encoded := historyJSON(got.out)
			if strings.Contains(encoded, first) || strings.Contains(encoded, later) {
				t.Fatal("concurrent credential escaped history output")
			}
		case <-ctx.Done():
			t.Fatal("history walk did not finish")
		}
	})
}

func TestHistoryLaterCredentialRedactsEarlierRows(t *testing.T) {
	historyKinds(t, func(t *testing.T, payments bool, _ string) {
		const first = "synthetic-first-token"
		const later = "synthetic-later-token"
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			page := 1
			if r.URL.Query().Get("pageNumber") == "2" {
				page = 2
			}
			if page == 2 && r.Header.Get("Authorization") == "Bearer "+first {
				w.WriteHeader(401)
				return
			}
			obj := historyObject(historyBody(payments, page, 1, 2, page))
			row := obj["data"].(map[string]any)["content"].([]any)[0].(map[string]any)
			row["description"] = later
			_, _ = w.Write([]byte(historyJSON(obj)))
		}))
		defer server.Close()
		tc := transport.New(transport.Config{HTTPClient: server.Client(), TokenSource: &credentialTestTokens{values: []string{first, later}}})
		c := New(core.NewTestConfig("hcm-3", "", endpoints.Set{Billing: server.URL + "/"}, tc))
		out, err := historyRead(t, context.Background(), c, payments, &historyOptions{start: "2024-01-01", end: "2024-01-01", size: 1, all: true})
		if err != nil || out == nil || strings.Contains(historyJSON(out), later) {
			t.Fatal("later credential escaped in an earlier row")
		}
	})
}

func TestHistorySafeDecodeCauses(t *testing.T) {
	historyKinds(t, func(t *testing.T, payments bool, _ string) {
		for _, syntax := range []bool{false, true} {
			body := `{"code":200,"data":{"content":[],"pageable":{"synthetic-account-marker":{"size":"peer-value"},"number":1,"size":"peer-value","totalPages":0,"totalElements":0}}}`
			if syntax {
				body = `{"synthetic-account-marker": invalid}`
			}
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) }))
			out, err := historyRead(t, context.Background(), c, payments, &historyOptions{start: "2024-01-01", end: "2024-01-01"})
			var api *vngcloud.APIError
			var typeErr *json.UnmarshalTypeError
			var syntaxErr *json.SyntaxError
			if out != nil || !errors.As(err, &api) || api.Code != "InvalidResponse" || errors.Is(err, ErrHistoryIncomplete) {
				t.Fatal("decode failure lost API identity")
			}
			if syntax {
				if !errors.As(err, &syntaxErr) {
					t.Fatal("safe syntax identity lost")
				}
			} else if !errors.As(err, &typeErr) || strings.Contains(typeErr.Field+typeErr.Struct+typeErr.Value, "peer-value") {
				t.Fatal("safe type identity lost")
			}
			for cause := err; cause != nil; cause = errors.Unwrap(cause) {
				if strings.Contains(cause.Error(), "synthetic-account-marker") || strings.Contains(cause.Error(), "peer-value") {
					t.Fatal("decode chain retained peer values")
				}
			}
		}
	})
}

type historyContextTokens struct {
	t      *testing.T
	cancel context.CancelFunc
	calls  int
}

func TestHistoryTruncatedBodyKeepsIdentity(t *testing.T) {
	historyKinds(t, func(t *testing.T, payments bool, _ string) {
		c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Length", "100")
			_, _ = w.Write([]byte("{"))
		}))
		out, err := historyRead(t, context.Background(), c, payments, &historyOptions{start: "2024-01-01", end: "2024-01-01"})
		var api *vngcloud.APIError
		if out != nil || !errors.As(err, &api) || api.StatusCode != 200 || !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Fatal("truncated response lost status or safe cause")
		}
	})
}

func (s *historyContextTokens) Token(ctx context.Context) (transport.Token, error) {
	s.calls++
	deadline, ok := ctx.Deadline()
	if !ok || time.Until(deadline) > 2*time.Minute {
		s.t.Error("authentication lacked whole-invocation deadline")
	}
	s.cancel()
	return transport.Token{}, ctx.Err()
}
func (*historyContextTokens) Invalidate(string) {}

func TestHistoryCanceledAuthentication(t *testing.T) {
	historyKinds(t, func(t *testing.T, payments bool, _ string) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		source := &historyContextTokens{t: t, cancel: cancel}
		tc := transport.New(transport.Config{TokenSource: source})
		c := New(core.NewTestConfig("hcm-3", "", endpoints.Set{Billing: "http://127.0.0.1/"}, tc))
		out, err := historyRead(t, ctx, c, payments, &historyOptions{start: "2024-01-01", end: "2024-01-01"})
		if out != nil || !errors.Is(err, context.Canceled) || source.calls != 1 {
			t.Fatal("authentication cancellation lost identity")
		}
	})
}

type historyFailingTokens struct{ err error }

func (s historyFailingTokens) Token(context.Context) (transport.Token, error) {
	return transport.Token{}, s.err
}
func (historyFailingTokens) Invalidate(string) {}

func TestHistoryAuthenticationErrorTypes(t *testing.T) {
	historyKinds(t, func(t *testing.T, payments bool, _ string) {
		for _, serviceAccount := range []bool{false, true} {
			var failure error = &core.LoginError{Status: 401, Reason: "synthetic-account-marker", Err: core.ErrAuth}
			if serviceAccount {
				failure = &core.ServiceAccountLoginError{Status: 401, Reason: "synthetic-account-marker", Err: core.ErrAuth}
			}
			tc := transport.New(transport.Config{TokenSource: historyFailingTokens{err: failure}})
			c := New(core.NewTestConfig("hcm-3", "", endpoints.Set{Billing: "http://127.0.0.1/"}, tc))
			out, err := historyRead(t, context.Background(), c, payments, &historyOptions{start: "2024-01-01", end: "2024-01-01"})
			var login *vngcloud.LoginError
			var exchange *vngcloud.ServiceAccountLoginError
			if out != nil || !errors.Is(err, vngcloud.ErrAuth) || (!serviceAccount && !errors.As(err, &login)) || (serviceAccount && !errors.As(err, &exchange)) {
				t.Fatal("public authentication error identity lost")
			}
			if strings.Contains(err.Error(), "synthetic-account-marker") {
				t.Fatal("authentication peer text escaped")
			}
		}
	})
}

func TestHistorySafeNetworkTimeout(t *testing.T) {
	historyKinds(t, func(t *testing.T, payments bool, _ string) {
		failure := &net.DNSError{Name: "synthetic-account-marker", Err: "synthetic-peer-marker", IsTimeout: true}
		tc := transport.New(transport.Config{TokenSource: historyFailingTokens{err: failure}})
		c := New(core.NewTestConfig("hcm-3", "", endpoints.Set{Billing: "http://127.0.0.1/"}, tc))
		out, err := historyRead(t, context.Background(), c, payments, &historyOptions{start: "2024-01-01", end: "2024-01-01"})
		var network net.Error
		if out != nil || !errors.As(err, &network) || !network.Timeout() || strings.Contains(err.Error(), "synthetic-") {
			t.Fatal("safe network timeout identity lost")
		}
	})
}

func TestHistoryRedirectAndTLSRefused(t *testing.T) {
	historyKinds(t, func(t *testing.T, payments bool, _ string) {
		var foreign atomic.Int32
		target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { foreign.Add(1) }))
		defer target.Close()
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, http.StatusFound) }))
		defer server.Close()
		client := server.Client()
		client.CheckRedirect = func(*http.Request, []*http.Request) error {
			t.Error("redirect hook ran despite NoRedirect")
			return nil
		}
		for _, tls := range []bool{false, true} {
			url := server.URL
			if tls {
				secure := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("untrusted TLS request reached handler") }))
				defer secure.Close()
				url = secure.URL
			}
			tc := transport.New(transport.Config{HTTPClient: client})
			c := New(core.NewTestConfig("hcm-3", "", endpoints.Set{Billing: url + "/"}, tc))
			out, err := historyRead(t, context.Background(), c, payments, &historyOptions{start: "2024-01-01", end: "2024-01-01"})
			if out != nil || err == nil || foreign.Load() != 0 {
				t.Fatal("redirect or TLS protection failed")
			}
		}
	})
}
