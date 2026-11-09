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
	"strconv"
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

func TestHistoryRejectsNumericCredentials(t *testing.T) {
	historyKinds(t, func(t *testing.T, payments bool, _ string) {
		for index, credential := range []string{"123456789012345", "+1235", "-1235", "1235.", ".5", "-.5", "1e3", "-1E-3", "1e99999", "01", "0", "  +1.25e+03  "} {
			for _, mode := range []string{"envelope", "amount", "empty", "http-error", "malformed"} {
				t.Run(strconv.Itoa(index)+"/"+mode, func(t *testing.T) {
					var calls atomic.Int32
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
						calls.Add(1)
						switch mode {
						case "envelope":
							_, _ = w.Write([]byte(`{"code":123456789012345,"data":[]}`))
						case "http-error":
							w.WriteHeader(http.StatusNotFound)
						case "malformed":
							_, _ = w.Write([]byte(`{"code":`))
						case "empty":
							_, _ = w.Write([]byte(historyBody(payments, 1, 50, 0)))
						default:
							body := historyObject(historyBody(payments, 1, 50, 1, 1))
							field := "transactionValue"
							if payments {
								field = "paymentAmount"
							}
							body["data"].(map[string]any)["content"].([]any)[0].(map[string]any)[field] = json.Number("123456789012345")
							_, _ = w.Write([]byte(historyJSON(body)))
						}
					}))
					defer server.Close()
					tc := transport.New(transport.Config{HTTPClient: server.Client(), TokenSource: &credentialTestTokens{values: []string{credential}}})
					c := New(core.NewTestConfig("hcm-3", "", endpoints.Set{Billing: server.URL + "/"}, tc))
					out, err := historyRead(t, context.Background(), c, payments, &historyOptions{start: "2024-01-01", end: "2024-01-01", all: true})
					assertHistoryNumericRefusal(t, out, err)
					if calls.Load() != 1 {
						t.Fatal("numeric refusal sent extra requests")
					}
				})
			}
		}
	})
}

func assertHistoryNumericRefusal(t *testing.T, out *historyResult, err error) {
	t.Helper()
	var api *vngcloud.APIError
	if out != nil || !errors.As(err, &api) || api.Code != "InvalidResponse" || api.Message != historyWithheld || api.Err != nil || api.Retryable || api.Operation == "" {
		t.Fatal("numeric credential did not fail closed with a fixed response")
	}
	if strings.Contains(historyJSON(api), "123456789012345") {
		t.Fatal("numeric credential reached error fields")
	}
}

func TestHistoryRejectsNumericCredentialsAcrossRefresh(t *testing.T) {
	historyKinds(t, func(t *testing.T, payments bool, _ string) {
		for _, credentials := range [][]string{{"123456789012345", "SYNTHETIC-HISTORY-TOKEN"}, {"SYNTHETIC-HISTORY-TOKEN", "123456789012345"}} {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if calls.Add(1) == 1 {
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
				_, _ = w.Write([]byte(historyBody(payments, 1, 50, 0)))
			}))
			defer server.Close()
			tc := transport.New(transport.Config{HTTPClient: server.Client(), TokenSource: &credentialTestTokens{values: credentials}})
			c := New(core.NewTestConfig("hcm-3", "", endpoints.Set{Billing: server.URL + "/"}, tc))
			out, err := historyRead(t, context.Background(), c, payments, &historyOptions{start: "2024-01-01", end: "2024-01-01", all: true})
			assertHistoryNumericRefusal(t, out, err)
			if calls.Load() != 2 {
				t.Fatal("numeric refresh refusal sent extra requests")
			}
		}
	})
}

func TestHistoryCaseInsensitiveCredentials(t *testing.T) {
	historyKinds(t, func(t *testing.T, payments bool, _ string) {
		for index, credential := range []string{"SYNTHETIC-HISTORY-TOKEN", `SYNTHETIC-"HISTORY\TOKEN`, "SYNTHETIC-K-Σ-TOKEN"} {
			for _, escaped := range []bool{false, true} {
				t.Run(strconv.Itoa(index)+"/"+strconv.FormatBool(escaped), func(t *testing.T) {
					reflected := credential
					if escaped {
						encoded := historyJSON(credential)
						reflected = encoded[1 : len(encoded)-1]
					}
					reflected = strings.ToLower(reflected)
					body := historyObject(historyBody(payments, 1, 50, 1, 1))
					row := body["data"].(map[string]any)["content"].([]any)[0].(map[string]any)
					row["description"] = "ordinary prefix " + reflected + " ordinary suffix"
					if payments {
						row["transactionID"], row["payGate"], row["paymentStatus"] = reflected, reflected, reflected
					} else {
						row["sourceReference"], row["userServiceReference"] = reflected, reflected
					}
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(historyJSON(body))) }))
					defer server.Close()
					tc := transport.New(transport.Config{HTTPClient: server.Client(), TokenSource: &credentialTestTokens{values: []string{credential}}})
					c := New(core.NewTestConfig("hcm-3", "", endpoints.Set{Billing: server.URL + "/"}, tc))
					out, err := historyRead(t, context.Background(), c, payments, &historyOptions{start: "2024-01-01", end: "2024-01-01"})
					if err != nil || out == nil || out.Items[0]["Description"] != "ordinary prefix [redacted] ordinary suffix" {
						t.Fatal("case-insensitive reflection escaped or account text changed")
					}
					if strings.Contains(strings.ToLower(historyJSON(out)), reflected) {
						t.Fatal("case-insensitive credential reached returned fields")
					}
					if out.Items[0]["CreatedAt"] != "2024-11-11T18:00:00.123+00:00" {
						t.Fatal("redaction changed an unrelated timestamp")
					}
				})
			}
		}
	})
}

func TestHistoryCaseInsensitiveOverlappingCredentials(t *testing.T) {
	historyKinds(t, func(t *testing.T, payments bool, _ string) {
		credentials := []string{"SYNTHETIC-HISTORY-TOKEN", "SYNTHETIC-HISTORY-TOKEN-SECRET-TAIL"}
		var calls atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			if calls.Add(1) == 1 {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			body := historyObject(historyBody(payments, 1, 50, 1, 1))
			body["data"].(map[string]any)["content"].([]any)[0].(map[string]any)["description"] = strings.ToLower(credentials[1])
			_, _ = w.Write([]byte(historyJSON(body)))
		}))
		defer server.Close()
		tc := transport.New(transport.Config{HTTPClient: server.Client(), TokenSource: &credentialTestTokens{values: credentials}})
		c := New(core.NewTestConfig("hcm-3", "", endpoints.Set{Billing: server.URL + "/"}, tc))
		out, err := historyRead(t, context.Background(), c, payments, &historyOptions{start: "2024-01-01", end: "2024-01-01"})
		if err != nil || out == nil || out.Items[0]["Description"] != "[redacted]" {
			t.Fatal("overlapping case-insensitive credential was partly exposed")
		}
	})
}

func TestHistoryCaseInsensitiveRedactionBounds(t *testing.T) {
	historyKinds(t, func(t *testing.T, payments bool, _ string) {
		for _, largeCredential := range []bool{false, true} {
			credential := strings.Repeat("SYNTHETIC-HISTORY-TOKEN", 256)
			if largeCredential {
				credential = strings.Repeat("SYNTHETIC-HISTORY-TOKEN", 40000)
			}
			body := historyObject(historyBody(payments, 1, 50, 1, 1))
			body["data"].(map[string]any)["content"].([]any)[0].(map[string]any)["description"] = strings.Repeat("ordinary account text ", 1<<18) + strings.ToLower(credential)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(historyJSON(body))) }))
			defer server.Close()
			tc := transport.New(transport.Config{HTTPClient: server.Client(), TokenSource: &credentialTestTokens{values: []string{credential}}})
			c := New(core.NewTestConfig("hcm-3", "", endpoints.Set{Billing: server.URL + "/"}, tc))
			out, err := historyRead(t, context.Background(), c, payments, &historyOptions{start: "2024-01-01", end: "2024-01-01"})
			if err != nil || out == nil || out.Items[0]["Description"] != "[redacted]" {
				t.Fatal("redaction work exhaustion did not withhold the field")
			}
		}
	})
}

func TestHistoryCaseInsensitivePreservesLongAccountText(t *testing.T) {
	historyKinds(t, func(t *testing.T, payments bool, _ string) {
		credential := "SYNTHETIC-" + strings.Repeat("A", 2048) + "-TOKEN"
		description := strings.Repeat("ordinary account text ", 1024)
		body := historyObject(historyBody(payments, 1, 50, 1, 1))
		body["data"].(map[string]any)["content"].([]any)[0].(map[string]any)["description"] = description
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(historyJSON(body))) }))
		defer server.Close()
		tc := transport.New(transport.Config{HTTPClient: server.Client(), TokenSource: &credentialTestTokens{values: []string{credential}}})
		c := New(core.NewTestConfig("hcm-3", "", endpoints.Set{Billing: server.URL + "/"}, tc))
		out, err := historyRead(t, context.Background(), c, payments, &historyOptions{start: "2024-01-01", end: "2024-01-01"})
		if err != nil || out == nil || out.Items[0]["Description"] != description {
			t.Fatal("redaction changed unrelated account text")
		}
	})
}

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
