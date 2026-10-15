package iam

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

func assertUserErrorPrivate(t *testing.T, err error, canaries ...string) {
	t.Helper()
	if err == nil {
		return
	}
	values := []string{fmt.Sprint(err), fmt.Sprintf("%+v", err), fmt.Sprintf("%#v", err)}
	var api *vngcloud.APIError
	if errors.As(err, &api) {
		values = append(values, api.Operation, api.Code, api.Message)
	}
	for _, value := range values {
		for _, canary := range canaries {
			if strings.Contains(value, canary) {
				t.Fatal("credential reached error node")
			}
		}
	}
	switch e := err.(type) { //nolint:errorlint // Traverse each direct error node before inspecting its children.
	case interface{ Unwrap() error }:
		assertUserErrorPrivate(t, e.Unwrap(), canaries...)
	case interface{ Unwrap() []error }:
		for _, cause := range e.Unwrap() {
			assertUserErrorPrivate(t, cause, canaries...)
		}
	}
}

func TestCreateUserCredentialEchoes(t *testing.T) {
	for _, where := range []string{"create-id", "confirmation-id", "confirmation-name", "confirmation-origin", "error", "nested-error", "lookup-id", "lookup-name"} {
		for _, canary := range []string{"password-canary", "bearer-canary"} {
			t.Run(where+"/"+strconv.Itoa(len(canary)), func(t *testing.T) {
				var calls atomic.Int32
				captures := 0
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					if r.Method == http.MethodPost {
						w.WriteHeader(201)
						switch where {
						case "create-id":
							_ = json.NewEncoder(w).Encode(map[string]string{"id": canary})
						case "lookup-id", "lookup-name":
							_, _ = w.Write([]byte(`{}`))
						default:
							_, _ = w.Write([]byte(`{"id":"user-1"}`))
						}
						return
					}
					if where == "error" || where == "nested-error" {
						w.WriteHeader(403)
						msg := canary
						if where == "nested-error" {
							msg = `{"password":"` + canary + `"}`
						}
						_ = json.NewEncoder(w).Encode(map[string]string{"code": msg, "message": msg})
						return
					}
					id, name, origin := "user-1", "new-user", "iam"
					switch where {
					case "confirmation-id", "lookup-id":
						id = canary
					case "confirmation-name", "lookup-name":
						name = canary
					case "confirmation-origin":
						origin = canary
					}
					if strings.Contains(where, "lookup") {
						_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{{"id": id, "username": name}}, "pageSize": 1000, "pageNumber": 0, "totalItems": 1, "totalPages": 1})
						return
					}
					_ = json.NewEncoder(w).Encode(map[string]string{"id": id, "username": name, "from": origin})
				}))
				defer server.Close()
				var logs bytes.Buffer
				cfg, err := vngcloud.NewConfig(vngcloud.WithRegion("hcm-3"), vngcloud.WithStaticToken("bearer-canary"), vngcloud.WithHTTPClient(server.Client()), vngcloud.WithRetry(3, 0), vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{Dashboard: server.URL + "/"}), vngcloud.WithLogger(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))))
				if err != nil {
					t.Fatal(err)
				}
				// Capture and debug are tested independently because config exposes no capture option.
				c := New(cfg)
				out, err := c.CreateUser(context.Background(), &CreateUserInput{Username: "new-user", Password: vngcloud.Secret("password-canary")})
				if out == nil || err == nil || out.Password != "" || out.CandidateID != "" || calls.Load() > 2 {
					t.Fatalf("calls=%d err=%v", calls.Load(), err)
				}
				assertUserErrorPrivate(t, err, "password-canary", "bearer-canary")
				raw, _ := json.Marshal(out) //nolint:gosec // Verify Secret redaction in the serialized output.
				for _, value := range []string{string(raw), logs.String(), fmt.Sprintf("%+v", out), fmt.Sprintf("%#v", out)} {
					if strings.Contains(value, "password-canary") || strings.Contains(value, "bearer-canary") {
						t.Fatal("credential leaked")
					}
				}
				captureClient := New(testutil.NewConfigWithCapture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method == http.MethodPost {
						w.WriteHeader(201)
						_, _ = w.Write([]byte(`{}`))
						return
					}
					_, _ = w.Write([]byte(`{"data":[],"pageNumber":0,"pageSize":1000,"totalItems":0,"totalPages":0}`))
				}), func(transport.Capture) { captures++ }))
				_, _ = captureClient.CreateUser(context.Background(), &CreateUserInput{Username: "new-user", Password: vngcloud.Secret("password-canary")})
				if captures != 0 {
					t.Fatal("create or lookup captured")
				}
			})
		}
	}
}

func TestCreateUserResponseCaps(t *testing.T) {
	for _, status := range []int{201, 400, 502} {
		var calls atomic.Int32
		c := New(testutil.NewRetryConfig(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			calls.Add(1)
			w.WriteHeader(status)
			_, _ = io.WriteString(w, strings.Repeat("x", (64<<10)+1))
		})))
		out, err := c.CreateUser(context.Background(), &CreateUserInput{Username: "new-user"})
		if calls.Load() != 1 || err == nil || status == 400 && out != nil || status != 400 && (out == nil || !errors.Is(err, ErrUserCreateUncertain)) {
			t.Fatalf("status=%d calls=%d err=%v", status, calls.Load(), err)
		}
	}
}

func TestCreateUserDroppedAndPartialBody(t *testing.T) {
	for _, partial := range []bool{false, true} {
		var calls atomic.Int32
		c := New(testutil.NewRetryConfig(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			calls.Add(1)
			conn, buf, err := http.NewResponseController(w).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			defer func() { _ = conn.Close() }()
			if partial {
				_, _ = buf.WriteString("HTTP/1.1 201 Created\r\nContent-Length: 100\r\n\r\n{\"id\":")
				_ = buf.Flush()
			}
		})))
		out, err := c.CreateUser(context.Background(), &CreateUserInput{Username: "new-user"})
		if calls.Load() != 1 || out == nil || out.Password == "" || !errors.Is(err, ErrUserCreateUncertain) {
			t.Fatalf("calls=%d err=%v", calls.Load(), err)
		}
	}
}

type userWriteRoundTrip func(*http.Request) (*http.Response, error)

func (f userWriteRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type userWriteFailBody struct {
	io.ReadCloser
	closeFailure bool
}

func (b userWriteFailBody) Read(p []byte) (int, error) {
	if b.closeFailure {
		return b.ReadCloser.Read(p)
	}
	return 0, fmt.Errorf("body-canary: %w", context.Canceled)
}
func (b userWriteFailBody) Close() error {
	_ = b.ReadCloser.Close()
	if b.closeFailure {
		return fmt.Errorf("body-canary: %w", context.Canceled)
	}
	return nil
}

func userWriteConfig(t *testing.T, server *httptest.Server, rt http.RoundTripper) vngcloud.Config {
	t.Helper()
	cfg, err := vngcloud.NewConfig(vngcloud.WithRegion("hcm-3"), vngcloud.WithStaticToken("bearer-canary"), vngcloud.WithHTTPClient(&http.Client{Transport: rt}), vngcloud.WithRetry(3, 0), vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{Dashboard: server.URL + "/"}))
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestCreateUserObservedBodyFailures(t *testing.T) {
	for _, status := range []int{201, 400, 401, 403, 404, 429, 502} {
		for _, closeFailure := range []bool{false, true} {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.WriteHeader(status)
				_, _ = io.WriteString(w, `{"id":"user-1"}`)
			}))
			rt := userWriteRoundTrip(func(r *http.Request) (*http.Response, error) {
				resp, err := server.Client().Transport.RoundTrip(r)
				if err == nil {
					resp.Body = userWriteFailBody{ReadCloser: resp.Body, closeFailure: closeFailure}
				}
				return resp, err
			})
			c := New(userWriteConfig(t, server, rt))
			out, err := c.CreateUser(context.Background(), &CreateUserInput{Username: "new-user"})
			if calls.Load() != 1 || !errors.Is(err, context.Canceled) {
				t.Fatalf("status=%d calls=%d err=%v", status, calls.Load(), err)
			}
			if status >= 400 && status < 500 {
				var api *vngcloud.APIError
				if out != nil || !errors.As(err, &api) || api.StatusCode != status {
					t.Fatal("rejection lost")
				}
			} else if out == nil || !errors.Is(err, ErrUserCreateUncertain) {
				t.Fatal("uncertain outcome lost")
			}
			assertUserErrorPrivate(t, err, "body-canary")
			server.Close()
		}
	}
}

func TestCreateUserCustomDialShapedError(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer server.Close()
	rt := userWriteRoundTrip(func(*http.Request) (*http.Response, error) {
		calls++
		return nil, &net.OpError{Op: "dial", Err: errors.New("dial-canary")}
	})
	out, err := New(userWriteConfig(t, server, rt)).CreateUser(context.Background(), &CreateUserInput{Username: "new-user"})
	if calls != 1 || out == nil || !errors.Is(err, ErrUserCreateUncertain) {
		t.Fatalf("calls=%d err=%v", calls, err)
	}
	assertUserErrorPrivate(t, err, "dial-canary")
}

func TestCreateUserCancellationAfterSend(t *testing.T) {
	var calls atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	release := make(chan struct{})
	c := New(testutil.NewRetryConfig(t, http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) { calls.Add(1); cancel(); <-release })))
	out, err := c.CreateUser(ctx, &CreateUserInput{Username: "new-user"})
	close(release)
	if calls.Load() != 1 || out == nil || out.Password == "" || !errors.Is(err, context.Canceled) || !errors.Is(err, ErrUserCreateUncertain) {
		t.Fatalf("calls=%d err=%v", calls.Load(), err)
	}
}

type userWriteTokens struct{ calls int }

func (s *userWriteTokens) Token(context.Context) (transport.Token, error) {
	s.calls++
	if s.calls == 1 {
		return transport.Token{AccessToken: "first-bearer", ExpiresAt: time.Unix(1<<40, 0)}, nil
	}
	return transport.Token{AccessToken: "refreshed-bearer", ExpiresAt: time.Unix(1<<40, 0)}, nil
}
func (*userWriteTokens) Invalidate(string) {}

func TestCreateUserRefreshCredentialIdentity(t *testing.T) {
	for _, where := range []string{"list", "get"} {
		var calls atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			if r.Method == http.MethodPost {
				w.WriteHeader(201)
				if where == "list" {
					_, _ = io.WriteString(w, `{}`)
				} else {
					_, _ = io.WriteString(w, `{"id":"user-1"}`)
				}
				return
			}
			if r.Header.Get("Authorization") == "Bearer first-bearer" {
				w.WriteHeader(401)
				return
			}
			if where == "list" {
				_, _ = io.WriteString(w, `{"data":[{"id":"refreshed-bearer","username":"new-user"}],"pageNumber":0,"pageSize":1000,"totalItems":1,"totalPages":1}`)
			} else {
				_, _ = io.WriteString(w, `{"id":"refreshed-bearer","username":"new-user","from":"iam"}`)
			}
		}))
		defer server.Close()
		source := &userWriteTokens{}
		cfg := core.NewTestConfig("hcm-3", "", endpoints.Set{Dashboard: server.URL + "/"}, transport.New(transport.Config{HTTPClient: server.Client(), TokenSource: source, RetryCount: 3}))
		out, err := New(cfg).CreateUser(context.Background(), &CreateUserInput{Username: "new-user"})
		if calls.Load() != 3 || out == nil || err == nil || out.CandidateID != "" {
			t.Fatalf("calls=%d err=%v", calls.Load(), err)
		}
		assertUserErrorPrivate(t, err, "first-bearer", "refreshed-bearer")
		if raw, _ := json.Marshal(out); bytes.Contains(raw, []byte("bearer")) { //nolint:gosec // Verify Secret redaction in the serialized output.
			t.Fatal("credential in output")
		}
	}
}

func TestCreateUserProvenNotSent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("unexpected request") }))
	defer server.Close()
	tr := server.Client().Transport.(*http.Transport).Clone()
	var calls atomic.Int32
	tr.DialContext = func(context.Context, string, string) (net.Conn, error) {
		calls.Add(1)
		return nil, &net.OpError{Op: "dial", Err: errors.New("dial-canary")}
	}
	defer tr.CloseIdleConnections()
	out, err := New(userWriteConfig(t, server, tr)).CreateUser(context.Background(), &CreateUserInput{Username: "new-user"})
	if calls.Load() != 1 || out != nil || err == nil || errors.Is(err, ErrUserCreateUncertain) {
		t.Fatalf("calls=%d err=%v", calls.Load(), err)
	}
	assertUserErrorPrivate(t, err, "dial-canary")
}
