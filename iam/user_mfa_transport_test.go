package iam

import (
	"context"
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

func TestUserMFABodyOutcomes(t *testing.T) {
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
					body := resetGuardBody(r.URL.Path)
					if r.URL.Path == "/accounts-api/v1/iam-users/user-1" {
						body = mfaUser(true, posts.Load() > 0)
					}
					_, _ = io.WriteString(w, body)
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
				out, err := callMFA(context.Background(), c, true, "user-1", "google")
				wantAfter := int32(0)
				if status == 204 {
					wantAfter = 1
				}
				if posts.Load() != 1 || after.Load() != wantAfter {
					t.Fatal("replayed or followed reset")
				}
				switch {
				case status >= 400 && status < 500:
					var api *vngcloud.APIError
					if out != nil || !errors.As(err, &api) || api.StatusCode != status {
						t.Fatalf("rejection lost: %v", err)
					}
				case status == 204 && failure == "ignored":
					if err != nil || out == nil || !out.Changed || out.Enabled == nil || !*out.Enabled {
						t.Fatalf("ignored body: %v", err)
					}
				default:
					want := ErrUserWriteUncertain
					if status == 204 {
						want = ErrNotSettled
					}
					if !errors.Is(err, want) || (status == 204 && (out == nil || !out.Changed || out.Enabled == nil || !*out.Enabled)) || (status != 204 && out != nil) {
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

func TestUserMFADroppedResponse(t *testing.T) {
	for _, partial := range []bool{false, true} {
		var posts atomic.Int32
		c := New(testutil.NewRetryConfig(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				body := resetGuardBody(r.URL.Path)
				if r.URL.Path == "/accounts-api/v1/iam-users/user-1" {
					body = mfaUser(true, posts.Load() > 0)
				}
				_, _ = io.WriteString(w, body)
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
		out, err := callMFA(context.Background(), c, true, "user-1", "google")
		if posts.Load() != 1 || out != nil || !errors.Is(err, ErrUserWriteUncertain) {
			t.Fatalf("drop: %v", err)
		}
	}
}

func TestUserMFACancellationAfterSend(t *testing.T) {
	var posts atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	release := make(chan struct{})
	c := New(testutil.NewRetryConfig(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			body := resetGuardBody(r.URL.Path)
			if r.URL.Path == "/accounts-api/v1/iam-users/user-1" {
				body = mfaUser(true, posts.Load() > 0)
			}
			_, _ = io.WriteString(w, body)
			return
		}
		posts.Add(1)
		cancel()
		<-release
	})))
	out, err := callMFA(ctx, c, true, "user-1", "google")
	close(release)
	if posts.Load() != 1 || out != nil || !errors.Is(err, ErrUserWriteUncertain) || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
}

func TestUserMFACustomDialError(t *testing.T) {
	var posts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := resetGuardBody(r.URL.Path)
		if r.URL.Path == "/accounts-api/v1/iam-users/user-1" {
			body = mfaUser(true, posts.Load() > 0)
		}
		_, _ = io.WriteString(w, body)
	}))
	defer server.Close()
	rt := userWriteRoundTrip(func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodPost {
			posts.Add(1)
			return nil, &net.OpError{Op: "dial", Err: errors.New("dial-canary")}
		}
		return server.Client().Transport.RoundTrip(r)
	})
	c := New(resetUserConfig(t, server, rt))
	out, err := callMFA(context.Background(), c, true, "user-1", "google")
	if posts.Load() != 1 || out != nil || !errors.Is(err, ErrUserWriteUncertain) {
		t.Fatalf("custom dial: %v", err)
	}
	assertUserErrorPrivate(t, err, "dial-canary")
}

func TestUserMFAProvenNotSent(t *testing.T) {
	var refuse atomic.Bool
	var dials atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			t.Error("MFA reached server")
		}
		if strings.HasSuffix(r.URL.Path, "/user-1/groups") {
			refuse.Store(true)
		}
		body := resetGuardBody(r.URL.Path)
		if r.URL.Path == "/accounts-api/v1/iam-users/user-1" {
			body = mfaUser(true, false)
		}
		_, _ = io.WriteString(w, body)
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
	out, err := callMFA(context.Background(), New(resetUserConfig(t, server, tr)), true, "user-1", "google")
	if dials.Load() != 1 || out != nil || err == nil || errors.Is(err, ErrUserWriteUncertain) {
		t.Fatalf("not-sent identity: %v", err)
	}
	assertUserErrorPrivate(t, err, "dial-canary")
}

func TestUserMFAConfirmationBoundsAndRedirects(t *testing.T) {
	for _, mode := range []string{"overflow", "redirect"} {
		var posts, foreign, captures atomic.Int32
		var logs lockedGuardLog
		destination := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { foreign.Add(1) }))
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPost {
				posts.Add(1)
				w.WriteHeader(204)
				return
			}
			if posts.Load() != 0 {
				if mode == "redirect" {
					w.Header().Set("Location", destination.URL+"/foreign")
					w.WriteHeader(302)
				} else {
					_, _ = io.WriteString(w, strings.Repeat("x", userObjectMaxBody+1))
				}
				return
			}
			body := resetGuardBody(r.URL.Path)
			if r.URL.Path == "/accounts-api/v1/iam-users/user-1" {
				body = mfaUser(true, false)
			}
			_, _ = io.WriteString(w, body)
		}))
		out, err := callMFA(context.Background(), privateGuardClient(server, &logs, &captures), true, "user-1", "google")
		server.Close()
		destination.Close()
		if out == nil || !out.Changed || out.Enabled != nil || !errors.Is(err, ErrNotSettled) || posts.Load() != 1 || foreign.Load() != 0 || captures.Load() != 0 {
			t.Fatalf("confirm bounds: %+v %v", out, err)
		}
	}
}
