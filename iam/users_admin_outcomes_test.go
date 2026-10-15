package iam

import (
	"context"
	"crypto/rand"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/testutil"
)

func TestResetUserPasswordBodyOutcomes(t *testing.T) {
	for _, status := range []int{204, 400, 401, 403, 404, 409, 429, 502} {
		for _, failure := range []string{"read", "close", "overflow", "ignored"} {
			t.Run(strconv.Itoa(status)+"/"+failure, func(t *testing.T) {
				var posts, after atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method == http.MethodPost {
						posts.Add(1)
						w.WriteHeader(status)
						return
					}
					if posts.Load() > 0 {
						after.Add(1)
					}
					_, _ = io.WriteString(w, resetGuardBody(r.URL.Path))
				}))
				defer server.Close()
				rt := userWriteRoundTrip(func(r *http.Request) (*http.Response, error) {
					resp, err := server.Client().Transport.RoundTrip(r)
					if err == nil && r.Method == http.MethodPost {
						_ = resp.Body.Close()
						body := io.NopCloser(strings.NewReader(`{"code":"hostile-body","message":"hostile-body"}`))
						switch failure {
						case "read", "close":
							resp.Body = userWriteFailBody{ReadCloser: body, closeFailure: failure == "close"}
						case "overflow":
							resp.Body = io.NopCloser(strings.NewReader(strings.Repeat("x", userWriteMaxBody+1)))
						default:
							resp.Body = body
						}
					}
					return resp, err
				})
				cfg := resetUserConfig(t, server, rt)
				// The production configuration uses the same origin for both guard APIs.
				c := New(cfg)
				out, err := c.ResetUserPassword(context.Background(), &ResetUserPasswordInput{UserID: "user-1"})
				if posts.Load() != 1 || after.Load() != 0 {
					t.Fatal("replayed or followed reset")
				}
				switch {
				case status >= 400 && status < 500:
					var api *vngcloud.APIError
					if out != nil || !errors.As(err, &api) || api.StatusCode != status {
						t.Fatalf("rejection lost: %v", err)
					}
				case status == 204 && failure == "ignored":
					if err != nil || out == nil || out.Password == "" {
						t.Fatalf("ignored body: %v", err)
					}
				default:
					want := ErrUserWriteUncertain
					if status == 204 {
						want = ErrNotSettled
					}
					if out == nil || out.Password == "" || !errors.Is(err, want) {
						t.Fatalf("outcome lost: %v", err)
					}
				}
				if (failure == "read" || failure == "close") && !errors.Is(err, context.Canceled) {
					t.Fatal("body context identity lost")
				}
				assertUserErrorPrivate(t, err, "body-canary", "hostile-body")
			})
		}
	}
}

func TestResetUserPasswordDroppedResponse(t *testing.T) {
	for _, partial := range []bool{false, true} {
		var posts atomic.Int32
		c := New(testutil.NewRetryConfig(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				_, _ = io.WriteString(w, resetGuardBody(r.URL.Path))
				return
			}
			posts.Add(1)
			conn, buf, err := http.NewResponseController(w).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			defer func() { _ = conn.Close() }()
			if partial {
				_, _ = buf.WriteString("HTTP/1.1 502 Bad Gateway\r\nContent-Length: 100\r\n\r\nx")
				_ = buf.Flush()
			}
		})))
		out, err := c.ResetUserPassword(context.Background(), &ResetUserPasswordInput{UserID: "user-1"})
		if posts.Load() != 1 || out == nil || out.Password == "" || !errors.Is(err, ErrUserWriteUncertain) {
			t.Fatalf("drop: %v", err)
		}
	}
}

func TestResetUserPasswordCancellationAfterSend(t *testing.T) {
	var posts atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	release := make(chan struct{})
	c := New(testutil.NewRetryConfig(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			_, _ = io.WriteString(w, resetGuardBody(r.URL.Path))
			return
		}
		posts.Add(1)
		cancel()
		<-release
	})))
	out, err := c.ResetUserPassword(ctx, &ResetUserPasswordInput{UserID: "user-1"})
	close(release)
	if posts.Load() != 1 || out == nil || out.Password == "" || !errors.Is(err, ErrUserWriteUncertain) || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
}

func TestResetUserPasswordCustomDialError(t *testing.T) {
	var posts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, resetGuardBody(r.URL.Path)) }))
	defer server.Close()
	rt := userWriteRoundTrip(func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodPost {
			posts.Add(1)
			return nil, &net.OpError{Op: "dial", Err: errors.New("dial-canary")}
		}
		return server.Client().Transport.RoundTrip(r)
	})
	c := New(resetUserConfig(t, server, rt))
	out, err := c.ResetUserPassword(context.Background(), &ResetUserPasswordInput{UserID: "user-1"})
	if posts.Load() != 1 || out == nil || !errors.Is(err, ErrUserWriteUncertain) {
		t.Fatalf("custom dial: %v", err)
	}
	assertUserErrorPrivate(t, err, "dial-canary")
}

type resetEntropy struct{ calls int }

func (e *resetEntropy) Read([]byte) (int, error) { e.calls++; return 0, errors.New("entropy-canary") }

func TestResetUserPasswordGenerationAfterGuards(t *testing.T) {
	entropy := new(resetEntropy)
	old := rand.Reader
	rand.Reader = entropy
	defer func() { rand.Reader = old }()
	for _, refuse := range []bool{true, false} {
		posts := 0
		c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPost {
				posts++
				w.WriteHeader(204)
				return
			}
			if refuse {
				_, _ = io.WriteString(w, `{"userId":"user-1","userType":"iam-user"}`)
				return
			}
			_, _ = io.WriteString(w, resetGuardBody(r.URL.Path))
		}))
		out, err := c.ResetUserPassword(context.Background(), &ResetUserPasswordInput{UserID: "user-1"})
		if out != nil || err == nil || posts != 0 {
			t.Fatal("entropy failure sent reset")
		}
		if refuse && (!errors.Is(err, ErrSelfChange) || entropy.calls != 0) {
			t.Fatal("generated before guards")
		}
		if !refuse && entropy.calls != 1 {
			t.Fatal("generation was not attempted after guards")
		}
		assertUserErrorPrivate(t, err, "entropy-canary")
	}
}

func resetUserConfig(t *testing.T, server *httptest.Server, rt http.RoundTripper) vngcloud.Config {
	t.Helper()
	cfg, err := vngcloud.NewConfig(vngcloud.WithRegion("hcm-3"), vngcloud.WithStaticToken("bearer-canary"), vngcloud.WithHTTPClient(&http.Client{Transport: rt}), vngcloud.WithRetry(3, 0), vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{Dashboard: server.URL + "/", IAM: server.URL + "/"}))
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestResetUserPasswordProvenNotSent(t *testing.T) {
	var refuse atomic.Bool
	var dials atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			t.Error("reset reached server")
		}
		if strings.HasSuffix(r.URL.Path, "/user-1/groups") {
			refuse.Store(true)
		}
		_, _ = io.WriteString(w, resetGuardBody(r.URL.Path))
	}))
	defer server.Close()
	tr := server.Client().Transport.(*http.Transport).Clone()
	tr.DisableKeepAlives = true
	tr.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		if refuse.Load() {
			dials.Add(1)
			return nil, &net.OpError{Op: "dial", Err: errors.New("dial-canary")}
		}
		return (&net.Dialer{}).DialContext(ctx, network, address)
	}
	defer tr.CloseIdleConnections()
	out, err := New(resetUserConfig(t, server, tr)).ResetUserPassword(context.Background(), &ResetUserPasswordInput{UserID: "user-1"})
	if dials.Load() != 1 || out != nil || err == nil || errors.Is(err, ErrUserWriteUncertain) {
		t.Fatalf("not-sent identity: %v", err)
	}
	assertUserErrorPrivate(t, err, "dial-canary")
}
