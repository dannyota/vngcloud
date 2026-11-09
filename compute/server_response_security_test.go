package compute

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"danny.vn/vngcloud"
)

type serverTestCredentials struct{ calls atomic.Int32 }

func (s *serverTestCredentials) Token(context.Context) (vngcloud.Token, error) {
	return vngcloud.Token{AccessToken: fmt.Sprintf("synthetic-token-%03d", s.calls.Add(1))}, nil
}
func (*serverTestCredentials) Invalidate(string) {}

func newServerSecurityClient(t *testing.T, handler http.Handler, retries int) (*Client, *bytes.Buffer, *atomic.Int32) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	var logs bytes.Buffer
	var captures atomic.Int32
	cfg, err := vngcloud.NewConfig(vngcloud.WithRegion("hcm-3"), vngcloud.WithProjectID("project-1"),
		vngcloud.WithCredentialsProvider(&serverTestCredentials{}), vngcloud.WithHTTPClient(srv.Client()),
		vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{VServer: srv.URL, Portal: srv.URL}),
		vngcloud.WithRetry(retries, time.Nanosecond),
		vngcloud.WithLogger(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))),
		vngcloud.WithResponseCapture(func(vngcloud.ResponseCapture) { captures.Add(1) }))
	if err != nil {
		t.Fatal(err)
	}
	return withInstantSleep(New(cfg)), &logs, &captures
}

func assertNoUnsafeServerOutput(t *testing.T, out any, err error) {
	t.Helper()
	if err == nil || (out != nil && !reflect.ValueOf(out).IsNil()) {
		t.Fatalf("unsafe response returned output or no error: output present=%v, error=%v", out != nil && !reflect.ValueOf(out).IsNil(), err)
	}
	if strings.Contains(err.Error(), "synthetic-token-") || strings.Contains(err.Error(), "hostile-marker") {
		t.Fatal("response text leaked through error")
	}
}

func TestServerReadResponseSafety(t *testing.T) {
	for _, list := range []bool{false, true} {
		for _, tc := range []struct {
			name, body string
			fail       bool
		}{
			{"limit", strings.Repeat(" ", 8<<20-len(`{"data":{},"listData":[]}`)) + `{"data":{},"listData":[]}`, false},
			{"limit plus one", strings.Repeat(" ", (8<<20)+1-len(`{"data":{},"listData":[]}`)) + `{"data":{},"listData":[]}`, true},
			{"truncated", `{"data":{"name":"hostile-marker"},"listData":[`, true},
			{"type error", `{"data":{"uuid":"hostile-marker","status":[]},"listData":[{"name":{}}]}`, true},
			{"raw reflection", `{"data":{"name":"synthetic-token-001"},"listData":[{"name":"synthetic-token-001"}]}`, true},
			{"escaped reflection", `{"data":{"name":"\u0073ynthetic-token-001"},"listData":[{"name":"\u0073ynthetic-token-001"}]}`, true},
			{"unknown field reflection", `{"data":{},"listData":[],"ignored":{"synthetic-token-001":"x"}}`, true},
		} {
			t.Run(fmt.Sprintf("list=%v/%s", list, tc.name), func(t *testing.T) {
				c, logs, captures := newServerSecurityClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(tc.body)) }), 0)
				var out any
				var err error
				if list {
					out, err = c.ListServers(context.Background(), nil)
				} else {
					out, err = c.GetServer(context.Background(), &GetServerInput{ServerID: "server-1"})
				}
				if tc.fail {
					assertNoUnsafeServerOutput(t, out, err)
				} else if err != nil {
					t.Fatal(err)
				}
				if strings.Contains(logs.String(), "synthetic-token-") || captures.Load() != 0 {
					t.Fatal("guarded response reached logs or captures")
				}
			})
		}
	}
}

func TestServerReadEarlierCredentialAfterRefresh(t *testing.T) {
	for _, list := range []bool{false, true} {
		t.Run(strconv.FormatBool(list), func(t *testing.T) {
			var sends atomic.Int32
			c, _, _ := newServerSecurityClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if sends.Add(1) == 1 {
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
				_, _ = w.Write([]byte(`{"data":{"name":"synthetic-token-001"},"listData":[{"name":"synthetic-token-001"}]}`))
			}), 0)
			var out any
			var err error
			if list {
				out, err = c.ListServers(context.Background(), nil)
			} else {
				out, err = c.GetServer(context.Background(), &GetServerInput{ServerID: "server-1"})
			}
			assertNoUnsafeServerOutput(t, out, err)
			if sends.Load() != 2 {
				t.Fatalf("sends=%d, want 2", sends.Load())
			}
		})
	}
}

func TestServerRetainedOutputRecheckedAgainstLaterCredentials(t *testing.T) {
	for _, action := range []string{"start", "stop", "reboot", "resize"} {
		for _, noWait := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/noWait=%v", action, noWait), func(t *testing.T) {
				reads := 0
				c, logs, captures := newServerSecurityClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					switch {
					case strings.HasSuffix(r.URL.Path, "/price"):
						_, _ = w.Write([]byte(quoteServerFixture))
					case r.Method == http.MethodGet:
						reads++
						if reads > 1 {
							_, _ = w.Write([]byte(`{"data":`))
							return
						}
						status := "ACTIVE"
						if action == "start" {
							status = "STOPPED"
						}
						_, _ = fmt.Fprintf(w, `{"data":{"uuid":"server-1","name":"synthetic-token-002","status":%q,"flavor":{"flavorId":"flavor-1"}}}`, status)
					default:
						w.WriteHeader(http.StatusAccepted)
					}
				}), 0)
				var out any
				var err error
				switch action {
				case "start":
					out, err = c.StartServer(context.Background(), &StartServerInput{ServerID: "server-1", NoWait: noWait})
				case "stop":
					out, err = c.StopServer(context.Background(), &StopServerInput{ServerID: "server-1", NoWait: noWait})
				case "reboot":
					out, err = c.RebootServer(context.Background(), &RebootServerInput{ServerID: "server-1", NoWait: noWait})
				case "resize":
					out, err = c.ResizeServer(context.Background(), &ResizeServerInput{ServerID: "server-1", FlavorID: "flavor-2", MaxPrice: 400000, NoWait: noWait})
				}
				assertNoUnsafeServerOutput(t, out, err)
				if strings.Contains(logs.String(), "synthetic-token-") || captures.Load() != 0 {
					t.Fatal("operation leaked credential")
				}
			})
		}
	}
}

func TestServerWaitCredentialExhaustionWithholdsRetainedOutput(t *testing.T) {
	var sends atomic.Int32
	c, _, _ := newServerSecurityClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if sends.Add(1)%2 == 1 {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"data":{"uuid":"server-1","status":"CREATING"}}`))
	}), 0)
	out, err := c.waitServerActive(context.Background(), "compute.CreateServer", "server-1")
	assertNoUnsafeServerOutput(t, out, err)
	if sends.Load() != 256 {
		t.Fatalf("sends=%d, want 256", sends.Load())
	}
}

func TestServerPublicIPWriteAndWaitPaths(t *testing.T) {
	for _, action := range []string{"create", "start", "stop", "reboot", "rename", "resize"} {
		t.Run(action, func(t *testing.T) {
			changed := false
			c := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case strings.HasSuffix(r.URL.Path, "/price"):
					_, _ = w.Write([]byte(quoteServerFixture))
					return
				case r.URL.Path == "/v2/project-1/servers" && r.Method == http.MethodGet:
					_, _ = w.Write([]byte(emptyListServersPage))
					return
				case r.Method != http.MethodGet && action != "rename":
					changed = true
					w.WriteHeader(http.StatusAccepted)
					_, _ = w.Write([]byte(`{"data":{"uuid":"server-1"}}`))
					return
				}
				status := "ACTIVE"
				if action == "start" && !changed {
					status = "STOPPED"
				}
				if action == "stop" && changed {
					status = "STOPPED"
				}
				flavor := "flavor-1"
				if changed {
					flavor = "flavor-2"
				}
				_, _ = fmt.Fprintf(w, `{"data":{"uuid":"server-1","name":"web-2","status":%q,"flavor":{"flavorId":%q},"internalInterfaces":[{"floatingIp":"::ffff:192.0.2.1"}]}}`, status, flavor)
			})))
			var server Server
			var err error
			switch action {
			case "create":
				in := validCreateServerInput()
				in.MaxPrice = 400000
				var out *CreateServerOutput
				out, err = c.CreateServer(context.Background(), in)
				if out != nil {
					server = out.Server
				}
			case "start":
				var out *StartServerOutput
				out, err = c.StartServer(context.Background(), &StartServerInput{ServerID: "server-1"})
				if out != nil {
					server = out.Server
				}
			case "stop":
				var out *StopServerOutput
				out, err = c.StopServer(context.Background(), &StopServerInput{ServerID: "server-1"})
				if out != nil {
					server = out.Server
				}
			case "reboot":
				var out *RebootServerOutput
				out, err = c.RebootServer(context.Background(), &RebootServerInput{ServerID: "server-1"})
				if out != nil {
					server = out.Server
				}
			case "rename":
				var out *RenameServerOutput
				out, err = c.RenameServer(context.Background(), &RenameServerInput{ServerID: "server-1", Name: "web-2"})
				if out != nil {
					server = out.Server
				}
			case "resize":
				var out *ResizeServerOutput
				out, err = c.ResizeServer(context.Background(), &ResizeServerInput{ServerID: "server-1", FlavorID: "flavor-2", MaxPrice: 400000})
				if out != nil {
					server = out.Server
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			if publicIPValue(t, server) != "192.0.2.1" {
				t.Fatal("write or wait lost derived address")
			}
		})
	}
}

func TestServerReadErrorIdentityAndCancellation(t *testing.T) {
	for _, status := range []int{401, 403, 404, 429, 500} {
		c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(status) }))
		out, err := c.GetServer(context.Background(), &GetServerInput{ServerID: "server-1"})
		var api *vngcloud.APIError
		if out != nil || !errors.As(err, &api) || api.StatusCode != status {
			t.Fatalf("status=%d, error=%v", status, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("canceled request sent") }))
	if out, err := c.GetServer(ctx, &GetServerInput{ServerID: "server-1"}); out != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled error=%v", err)
	}
}

func TestServerPollDeadlineIncludesRequests(t *testing.T) {
	called := false
	err := poll(context.Background(), time.Now, contextSleep, 5*time.Minute, func(ctx context.Context) (bool, error) {
		called = true
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 5*time.Minute {
			t.Error("poll request has no phase deadline")
		}
		return true, nil
	}, func() error { return errors.New("unexpected timeout") })
	if err != nil || !called {
		t.Fatalf("poll error=%v", err)
	}
}

type serverPhaseTransport func(*http.Request) (*http.Response, error)

// RoundTrip inspects request contexts without making a network call.
func (f serverPhaseTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestServerPreflightAndQuotePhaseDeadlines(t *testing.T) {
	for _, operation := range []string{"create", "resize", "quote create", "quote resize"} {
		t.Run(operation, func(t *testing.T) {
			var firstPageDeadline time.Time
			transport := serverPhaseTransport(func(r *http.Request) (*http.Response, error) {
				body := quoteServerFixture
				status := http.StatusOK
				switch {
				case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/servers"):
					deadline, ok := r.Context().Deadline()
					if !ok {
						t.Error("preflight has no deadline")
					}
					if firstPageDeadline.IsZero() {
						firstPageDeadline = deadline
					} else if deadline.After(firstPageDeadline) {
						t.Error("preflight deadline restarted on next page")
					}
					if r.URL.Query().Get("page") == "1" {
						body = `{"listData":[{"uuid":"other-1","name":"other-1"}],"page":1,"pageSize":1,"totalPage":2,"totalItem":2}`
					} else {
						body = `{"listData":[{"uuid":"other-2","name":"other-2"}],"page":2,"pageSize":1,"totalPage":2,"totalItem":2}`
					}
				case strings.HasSuffix(r.URL.Path, "/price"):
					deadline, ok := r.Context().Deadline()
					if !ok || time.Until(deadline) > 30*time.Second {
						t.Error("quote has no 30-second phase deadline")
					}
				case r.Method == http.MethodGet:
					body = serverBodyWithFlavor("ACTIVE", "flavor-1")
				default:
					status = http.StatusAccepted
					body = `{"data":{"uuid":"server-1"}}`
				}
				return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
			})
			cfg, err := vngcloud.NewConfig(vngcloud.WithRegion("hcm-3"), vngcloud.WithProjectID("project-1"), vngcloud.WithStaticToken("synthetic-token"), vngcloud.WithHTTPClient(&http.Client{Transport: transport}), vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{VServer: "http://synthetic.invalid", Portal: "http://synthetic.invalid"}))
			if err != nil {
				t.Fatal(err)
			}
			c := New(cfg)
			create := validCreateServerInput()
			create.MaxPrice = 400000
			create.NoWait = true
			resize := &ResizeServerInput{ServerID: "server-1", FlavorID: "flavor-2", MaxPrice: 400000, NoWait: true}
			switch operation {
			case "create":
				_, err = c.CreateServer(context.Background(), create)
			case "resize":
				_, err = c.ResizeServer(context.Background(), resize)
			case "quote create":
				_, err = c.QuoteCreateServer(context.Background(), create)
			case "quote resize":
				_, err = c.QuoteResizeServer(context.Background(), resize)
			}
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestServerWithheldOutputPreservesOriginalAPIError(t *testing.T) {
	c, _, _ := newServerSecurityClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`{"data":{"uuid":"server-1","name":"synthetic-token-002","status":"STOPPED"}}`))
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"message":"synthetic-token-002"}`))
	}), 0)
	out, err := c.StartServer(context.Background(), &StartServerInput{ServerID: "server-1"})
	assertNoUnsafeServerOutput(t, out, err)
	var api *vngcloud.APIError
	if !errors.Is(err, ErrNotSettled) || !errors.As(err, &api) || api.StatusCode != 500 {
		t.Fatalf("original API/settlement identity lost: %v", err)
	}
}

//nolint:errorlint // Inspect every direct cause; errors.As can skip branches of a joined tree.
func assertSafeServerCauseTree(t *testing.T, err error, credential string) {
	t.Helper()
	if err == nil {
		return
	}
	if strings.Contains(err.Error(), credential) {
		t.Error("credential remains in an error cause")
	}
	var api *vngcloud.APIError
	if errors.As(err, &api) {
		for _, text := range []string{api.Operation, api.Code, api.Message} {
			if strings.Contains(text, credential) {
				t.Error("credential remains in API metadata")
			}
		}
	}
	switch cause := err.(type) {
	case interface{ Unwrap() []error }:
		for _, child := range cause.Unwrap() {
			assertSafeServerCauseTree(t, child, credential)
		}
	case interface{ Unwrap() error }:
		assertSafeServerCauseTree(t, cause.Unwrap(), credential)
	}
}

func TestCreateServerLaterCredentialRemovedFromEveryCause(t *testing.T) {
	const credential = "synthetic-token-004"
	c, _, _ := newServerSecurityClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/price"):
			_, _ = w.Write([]byte(quoteServerFixture))
		case r.Method == http.MethodGet && r.URL.Path == "/v2/project-1/servers":
			_, _ = w.Write([]byte(emptyListServersPage))
		case r.Method == http.MethodPost:
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"data":{"uuid":"synthetic-token-004"}}`))
		default:
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"code":"SafeCode","message":"confirmation failed"}`))
		}
	}), 0)
	in := validCreateServerInput()
	in.MaxPrice = 400000
	out, err := c.CreateServer(context.Background(), in)
	assertNoUnsafeServerOutput(t, out, err)
	assertSafeServerCauseTree(t, err, credential)
	var api *vngcloud.APIError
	if !errors.Is(err, ErrNotSettled) || !errors.As(err, &api) || api.StatusCode != 503 || api.Code != "SafeCode" || !api.Retryable {
		t.Fatalf("safe API metadata or settlement cause lost: %v", err)
	}
}

type hiddenServerTestError struct{ cause error }

// Error deliberately omits its child's message.
func (*hiddenServerTestError) Error() string { return "safe outer message" }

// Unwrap exposes the nested error for cause-tree protection tests.
func (e *hiddenServerTestError) Unwrap() error { return e.cause }

func TestServerSanitizesHiddenJoinedCausesAndAPIMetadata(t *testing.T) {
	const credential = "synthetic-hidden-token"
	for _, metadata := range []bool{false, true} {
		t.Run(strconv.FormatBool(metadata), func(t *testing.T) {
			_, scope := serverOperationContext(context.Background())
			if _, err := scope.observe(credential); err != nil {
				t.Fatal(err)
			}
			api := &vngcloud.APIError{Operation: "compute.GetServer", StatusCode: 503, Code: "SafeCode", Message: "safe API message", Retryable: true, Err: fmt.Errorf("%s: %w", credential, context.DeadlineExceeded)}
			if metadata {
				api.Operation += credential
				api.Code += credential
				api.Message += credential
			}
			targets := []error{ErrNotSettled, ErrFailed, vngcloud.ErrAuth, vngcloud.ErrPermission, vngcloud.ErrNotFound, vngcloud.ErrRateLimited, vngcloud.ErrInvalidInput, context.Canceled, context.DeadlineExceeded, io.EOF}
			causes := append([]error{fmt.Errorf("%s: %w", credential, api)}, targets...)
			var err error = &hiddenServerTestError{cause: errors.Join(causes...)}
			var out *GetServerOutput
			finishServerOperation(scope, "compute.GetServer", &out, &err)
			assertSafeServerCauseTree(t, err, credential)
			for _, target := range targets {
				if !errors.Is(err, target) {
					t.Fatalf("safe sentinel lost: %v", target)
				}
			}
			var safe *vngcloud.APIError
			if !errors.As(err, &safe) || safe.StatusCode != 503 || !safe.Retryable {
				t.Fatal("safe API metadata lost")
			}
			if !metadata && (safe.Operation != "compute.GetServer" || safe.Code != "SafeCode" || safe.Message != "safe API message") {
				t.Fatal("safe API text changed")
			}
		})
	}
}

func TestServerReadsRejectDerivedPublicIPCredential(t *testing.T) {
	const body = `{"data":{"internalInterfaces":[{"floatingIp":"2001:0DB8:0:0:0:0:0:1"}]},"listData":[{"internalInterfaces":[{"floatingIp":"2001:0DB8:0:0:0:0:0:1"}]}]}`
	for _, method := range []string{"get", "list", "rename"} {
		t.Run(method, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) }))
			defer srv.Close()
			cfg, err := vngcloud.NewConfig(vngcloud.WithRegion("hcm-3"), vngcloud.WithProjectID("project-1"), vngcloud.WithStaticToken("2001:db8::1"), vngcloud.WithHTTPClient(srv.Client()), vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{VServer: srv.URL}), vngcloud.WithRetry(0, 0))
			if err != nil {
				t.Fatal(err)
			}
			c := New(cfg)
			var out any
			switch method {
			case "get":
				out, err = c.GetServer(context.Background(), &GetServerInput{ServerID: "server-1"})
			case "list":
				out, err = c.ListServers(context.Background(), nil)
			case "rename":
				out, err = c.RenameServer(context.Background(), &RenameServerInput{ServerID: "server-1", Name: "web-2"})
			}
			assertNoUnsafeServerOutput(t, out, err)
		})
	}
}

type derivedServerTestCredentials struct{ calls atomic.Int32 }

func (s *derivedServerTestCredentials) Token(context.Context) (vngcloud.Token, error) {
	value := "synthetic-earlier-token"
	if s.calls.Add(1) > 1 {
		value = "2001:db8::1"
	}
	return vngcloud.Token{AccessToken: value}, nil
}
func (*derivedServerTestCredentials) Invalidate(string) {}

func TestServerRetainedDerivedPublicIPRecheckedAfterLaterCredential(t *testing.T) {
	for _, noWait := range []bool{true, false} {
		t.Run(strconv.FormatBool(noWait), func(t *testing.T) {
			reads := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPut {
					w.WriteHeader(http.StatusAccepted)
					return
				}
				reads++
				if reads > 1 {
					w.WriteHeader(http.StatusServiceUnavailable)
					return
				}
				_, _ = w.Write([]byte(`{"data":{"uuid":"server-1","status":"STOPPED","internalInterfaces":[{"floatingIp":"2001:0DB8:0:0:0:0:0:1"}]}}`))
			}))
			defer srv.Close()
			cfg, err := vngcloud.NewConfig(vngcloud.WithRegion("hcm-3"), vngcloud.WithProjectID("project-1"), vngcloud.WithCredentialsProvider(&derivedServerTestCredentials{}), vngcloud.WithHTTPClient(srv.Client()), vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{VServer: srv.URL}), vngcloud.WithRetry(0, 0))
			if err != nil {
				t.Fatal(err)
			}
			out, err := New(cfg).StartServer(context.Background(), &StartServerInput{ServerID: "server-1", NoWait: noWait})
			assertNoUnsafeServerOutput(t, out, err)
			assertSafeServerCauseTree(t, err, "2001:db8::1")
		})
	}
}

// Synthetic numeric credentials must remain protected after JSON normalization.
func TestServerReadsRejectNormalizedNumericCredential(t *testing.T) {
	const body = `{"data":{"gpuMemory":1.23456789e8},"listData":[{"gpuMemory":1.23456789e8}]}`
	for _, method := range []string{"get", "list", "rename"} {
		t.Run(method, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) }))
			defer srv.Close()
			cfg, err := vngcloud.NewConfig(vngcloud.WithRegion("hcm-3"), vngcloud.WithProjectID("project-1"), vngcloud.WithStaticToken("123456789"), vngcloud.WithHTTPClient(srv.Client()), vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{VServer: srv.URL}), vngcloud.WithRetry(0, 0))
			if err != nil {
				t.Fatal(err)
			}
			c := New(cfg)
			var out any
			switch method {
			case "get":
				out, err = c.GetServer(context.Background(), &GetServerInput{ServerID: "server-1"})
			case "list":
				out, err = c.ListServers(context.Background(), nil)
			case "rename":
				out, err = c.RenameServer(context.Background(), &RenameServerInput{ServerID: "server-1", Name: "web-2"})
			}
			assertNoUnsafeServerOutput(t, out, err)
		})
	}
}

func TestServerCancellationIdentityWithMatchingCredential(t *testing.T) {
	for _, tc := range []struct {
		name, credential string
		cause            error
	}{
		{"canceled", "canceled", context.Canceled},
		{"deadline", "deadline", context.DeadlineExceeded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			httpClient := &http.Client{Transport: serverPhaseTransport(func(*http.Request) (*http.Response, error) { return nil, tc.cause })}
			cfg, err := vngcloud.NewConfig(vngcloud.WithRegion("hcm-3"), vngcloud.WithProjectID("project-1"), vngcloud.WithStaticToken(tc.credential), vngcloud.WithHTTPClient(httpClient), vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{VServer: "http://synthetic.invalid"}), vngcloud.WithRetry(0, 0))
			if err != nil {
				t.Fatal(err)
			}
			out, err := New(cfg).GetServer(context.Background(), &GetServerInput{ServerID: "server-1"})
			assertNoUnsafeServerOutput(t, out, err)
			assertSafeServerCauseTree(t, err, tc.credential)
			if !errors.Is(err, tc.cause) {
				t.Fatal("request lost cancellation identity after redaction")
			}
			var api *vngcloud.APIError
			if !errors.As(err, &api) || api.Operation != "compute.GetServer" {
				t.Fatal("request lost safe API metadata")
			}
			if strings.Contains(api.Error(), tc.credential) {
				t.Fatal("API metadata regenerated credential-bearing cause text")
			}
		})
	}
}

// Synthetic credentials intentionally match public sentinel messages.
func TestServerRedactedSentinelIdentities(t *testing.T) {
	for _, target := range []error{
		context.Canceled, context.DeadlineExceeded,
		vngcloud.ErrTokenCache, vngcloud.ErrAuth, vngcloud.ErrNotFound,
		vngcloud.ErrPermission, vngcloud.ErrRateLimited, vngcloud.ErrProjectNotFound,
		vngcloud.ErrProjectAmbiguous, vngcloud.ErrMissingProjectID,
		vngcloud.ErrInvalidConfig, vngcloud.ErrInvalidInput, vngcloud.ErrPriceAboveMax,
		vngcloud.ErrUnpriced, vngcloud.ErrNoCredentials, vngcloud.ErrCredentialsFile,
		ErrNotSettled, ErrFailed, ErrUnexpectedStatus, ErrServerGroupInUse,
	} {
		t.Run(target.Error(), func(t *testing.T) {
			credential := target.Error()
			_, scope := serverOperationContext(context.Background())
			if _, err := scope.observe(credential); err != nil {
				t.Fatal(err)
			}
			err := target
			var out *GetServerOutput
			finishServerOperation(scope, "compute.GetServer", &out, &err)
			assertSafeServerCauseTree(t, err, credential)
			if !errors.Is(err, target) {
				t.Fatal("redaction lost SDK or context sentinel identity")
			}
			if errors.Is(err, errors.New(target.Error())) {
				t.Fatal("redaction compares sentinel text instead of identity")
			}
		})
	}
}

// Short synthetic credentials must not erase transport or context identities.
func TestServerNetworkIdentityWithMatchingCredential(t *testing.T) {
	for _, tc := range []struct {
		name, credential string
		target, cause    error
	}{
		{"refused", "refused", syscall.ECONNREFUSED, &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}},
		{"reset", "reset", syscall.ECONNRESET, &net.OpError{Op: "read", Net: "tcp", Err: syscall.ECONNRESET}},
		{"closed", "closed", net.ErrClosed, net.ErrClosed},
		{"canceled", "canceled", context.Canceled, context.Canceled},
		{"deadline", "deadline", context.DeadlineExceeded, context.DeadlineExceeded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			httpClient := &http.Client{Transport: serverPhaseTransport(func(*http.Request) (*http.Response, error) { return nil, tc.cause })}
			cfg, err := vngcloud.NewConfig(vngcloud.WithRegion("hcm-3"), vngcloud.WithProjectID("project-1"), vngcloud.WithStaticToken(tc.credential), vngcloud.WithHTTPClient(httpClient), vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{VServer: "http://synthetic.invalid"}), vngcloud.WithRetry(0, 0))
			if err != nil {
				t.Fatal(err)
			}
			out, err := New(cfg).GetServer(context.Background(), &GetServerInput{ServerID: "server-1"})
			assertNoUnsafeServerOutput(t, out, err)
			assertSafeServerCauseTree(t, err, tc.credential)
			if !errors.Is(err, tc.target) {
				t.Fatal("redaction lost original network or context identity")
			}
		})
	}
}

func TestServerRedactedIdentityWithoutSentinelList(t *testing.T) {
	for _, shape := range []string{"leaf", "wrapped", "joined"} {
		t.Run(shape, func(t *testing.T) {
			const credential = "secret"
			target := errors.New("synthetic secret identity")
			err := target
			switch shape {
			case "wrapped":
				err = fmt.Errorf("diagnostic: %w", target)
			case "joined":
				err = errors.Join(target, context.Canceled, vngcloud.ErrAuth)
			}
			_, scope := serverOperationContext(context.Background())
			if _, observeErr := scope.observe(credential); observeErr != nil {
				t.Fatal(observeErr)
			}
			var out *GetServerOutput
			finishServerOperation(scope, "compute.GetServer", &out, &err)
			assertSafeServerCauseTree(t, err, credential)
			if !errors.Is(err, target) {
				t.Fatal("redaction lost an unlisted error identity")
			}
			if errors.Is(err, errors.New(target.Error())) {
				t.Fatal("identity matched error text")
			}
			if shape == "joined" && (!errors.Is(err, context.Canceled) || !errors.Is(err, vngcloud.ErrAuth)) {
				t.Fatal("joined identities were lost")
			}
		})
	}
}

func TestServerAPIMetadataRejectsRenderedCredential(t *testing.T) {
	for _, tc := range []struct {
		credential string
		expose     bool
	}{
		{"503", false},
		{"unrelated-synthetic-token", true},
	} {
		t.Run(tc.credential, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = w.Write([]byte(`{"code":"SafeCode","message":"retry later"}`))
			}))
			defer srv.Close()
			cfg, err := vngcloud.NewConfig(vngcloud.WithRegion("hcm-3"), vngcloud.WithProjectID("project-1"), vngcloud.WithStaticToken(tc.credential), vngcloud.WithHTTPClient(srv.Client()), vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{VServer: srv.URL}), vngcloud.WithRetry(0, 0))
			if err != nil {
				t.Fatal(err)
			}
			out, err := New(cfg).GetServer(context.Background(), &GetServerInput{ServerID: "server-1"})
			assertNoUnsafeServerOutput(t, out, err)
			assertSafeServerCauseTree(t, err, tc.credential)
			var api *vngcloud.APIError
			if got := errors.As(err, &api); got != tc.expose {
				t.Fatalf("API metadata exposed=%v, want %v", got, tc.expose)
			}
			if api != nil && (api.StatusCode != 503 || api.Code != "SafeCode" || strings.Contains(api.Error(), tc.credential)) {
				t.Fatal("safe API metadata changed or exposed a credential")
			}
		})
	}
}

func TestServerAPIMetadataAsChecksEveryStringField(t *testing.T) {
	for _, field := range []string{"Operation", "Code", "Message"} {
		t.Run(field, func(t *testing.T) {
			const credential = "canceled"
			_, scope := serverOperationContext(context.Background())
			if _, err := scope.observe(credential); err != nil {
				t.Fatal(err)
			}
			var err error = &vngcloud.APIError{Operation: "compute.GetServer", Err: context.Canceled}
			var out *GetServerOutput
			finishServerOperation(scope, "compute.GetServer", &out, &err)
			var api *vngcloud.APIError
			if !errors.As(err, &api) {
				t.Fatal("initial safe metadata was withheld")
			}
			switch field {
			case "Operation":
				api.Operation = credential
			case "Code":
				api.Code = credential
			case "Message":
				api.Message = credential
			}
			var next *vngcloud.APIError
			if errors.As(err, &next) {
				t.Fatal("As exposed metadata containing a credential")
			}
			assertSafeServerCauseTree(t, err, credential)
			if !errors.Is(err, context.Canceled) {
				t.Fatal("withholding metadata lost cancellation identity")
			}
		})
	}
}

func TestServerWithheldAPIMetadataKeepsIdentities(t *testing.T) {
	_, scope := serverOperationContext(context.Background())
	if _, err := scope.observe("503"); err != nil {
		t.Fatal(err)
	}
	target := errors.New("synthetic underlying identity")
	var err error = &vngcloud.APIError{Operation: "compute.GetServer", StatusCode: 503, Err: errors.Join(target, context.Canceled, vngcloud.ErrAuth)}
	var out *GetServerOutput
	finishServerOperation(scope, "compute.GetServer", &out, &err)
	assertSafeServerCauseTree(t, err, "503")
	var api *vngcloud.APIError
	if errors.As(err, &api) {
		t.Fatal("numeric credential escaped through API metadata")
	}
	for _, identity := range []error{target, context.Canceled, vngcloud.ErrAuth} {
		if !errors.Is(err, identity) {
			t.Fatal("withholding metadata lost an underlying identity")
		}
	}
}

func TestServerAPIMetadataAsTracksLaterCredentials(t *testing.T) {
	_, scope := serverOperationContext(context.Background())
	if _, err := scope.observe("canceled"); err != nil {
		t.Fatal(err)
	}
	var err error = &vngcloud.APIError{Operation: "compute.GetServer", Code: "synthetic-later-token", Err: context.Canceled}
	var out *GetServerOutput
	finishServerOperation(scope, "compute.GetServer", &out, &err)
	var api *vngcloud.APIError
	if !errors.As(err, &api) {
		t.Fatal("initial safe metadata was withheld")
	}
	if _, observeErr := scope.observe("synthetic-later-token"); observeErr != nil {
		t.Fatal(observeErr)
	}
	var next *vngcloud.APIError
	if errors.As(err, &next) {
		t.Fatal("As ignored a credential observed later in the operation")
	}
	assertSafeServerCauseTree(t, err, "synthetic-later-token")
	if !errors.Is(err, context.Canceled) {
		t.Fatal("metadata refusal lost cancellation identity")
	}
}
