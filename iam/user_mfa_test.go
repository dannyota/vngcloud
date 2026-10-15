package iam

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/testutil"
)

// Enrolled factor responses are synthetic, not raw probe fixtures.
func mfaUser(verified, enabled bool) string {
	return fmt.Sprintf(`{"id":"user-1","username":"ordinary","from":"iam","twoFactorAuth":{"googleAuthenticator":{"verified":%t,"enabled":%t}}}`, verified, enabled)
}

func callMFA(ctx context.Context, c *Client, enable bool, id, method string) (*DisableUserMFAOutput, error) {
	if enable {
		out, err := c.EnableUserMFA(ctx, &EnableUserMFAInput{UserID: id, Method: method})
		if out == nil {
			return nil, err
		}
		return &DisableUserMFAOutput{Method: out.Method, Enabled: out.Enabled, Changed: out.Changed}, err
	}
	return c.DisableUserMFA(ctx, &DisableUserMFAInput{UserID: id, Method: method})
}

func TestUserMFAStatuses(t *testing.T) {
	for _, enable := range []bool{false, true} {
		for _, status := range []int{204, 200, 202, 301, 302, 303, 307, 308, 400, 401, 403, 404, 409, 429, 500, 502, 503, 504} {
			t.Run(strconv.FormatBool(enable)+"/"+strconv.Itoa(status), func(t *testing.T) {
				posts, targets, after := 0, 0, 0
				c := New(testutil.NewRetryConfig(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method == http.MethodPost {
						posts++
						suffix := "deactivate-google"
						if enable {
							suffix = "activate-google"
						}
						body, err := io.ReadAll(r.Body)
						if err != nil || len(body) != 0 || r.URL.Path != "/accounts-api/v1/iam-users/user-1/"+suffix {
							t.Error("wrong MFA request")
						}
						w.Header().Set("Location", "/redirect")
						w.WriteHeader(status)
						if status != 204 {
							_, _ = io.WriteString(w, `{"code":"hostile-canary","message":"hostile-canary"}`)
						}
						return
					}
					if posts > 0 {
						after++
					}
					body := resetGuardBody(r.URL.Path)
					if r.URL.Path == "/accounts-api/v1/iam-users/user-1" {
						targets++
						body = mfaUser(true, !enable)
						if posts > 0 {
							body = mfaUser(true, enable)
						}
					}
					_, _ = io.WriteString(w, body)
				})))
				out, err := callMFA(context.Background(), c, enable, "user-1", "google")
				if posts != 1 {
					t.Fatalf("posts=%d", posts)
				}
				switch {
				case status == 204:
					if err != nil || out == nil || out.Method != "google" || !out.Changed || out.Enabled == nil || *out.Enabled != enable || after != 1 || targets != 2 {
						t.Fatalf("acceptance: %+v %v, after=%d targets=%d", out, err, after, targets)
					}
				case status >= 400 && status < 500:
					var api *vngcloud.APIError
					if out != nil || !errors.As(err, &api) || api.StatusCode != status || after != 0 || targets != 1 || errors.Is(err, ErrUserWriteUncertain) {
						t.Fatalf("rejection: %+v %v", out, err)
					}
					sentinel := map[int]error{401: vngcloud.ErrAuth, 403: vngcloud.ErrPermission, 404: vngcloud.ErrNotFound, 429: vngcloud.ErrRateLimited}[status]
					if sentinel != nil && !errors.Is(err, sentinel) {
						t.Fatal("lost rejection identity")
					}
					if status == 409 && vngcloud.ErrorCode(err) != "Conflict" {
						t.Fatal("lost conflict")
					}
				default:
					if out != nil || !errors.Is(err, ErrUserWriteUncertain) || after != 0 || targets != 1 {
						t.Fatalf("uncertain: %+v %v", out, err)
					}
				}
				assertUserErrorPrivate(t, err, "hostile-canary")
			})
		}
	}
}

func TestUserMFAValidation(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("validation made request") }))
	for _, enable := range []bool{false, true} {
		for _, tc := range []struct{ id, method string }{{"", "google"}, {"../x", "google"}, {"user-1", ""}, {"user-1", "email"}, {"user-1", "Google"}, {"user-1", " google"}, {"user-1", "google/path"}} {
			out, err := callMFA(context.Background(), c, enable, tc.id, tc.method)
			if out != nil || !errors.Is(err, vngcloud.ErrInvalidInput) {
				t.Fatalf("invalid input: %v", err)
			}
		}
	}
	if out, err := c.EnableUserMFA(context.Background(), nil); out != nil || !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatal("nil enable")
	}
	if out, err := c.DisableUserMFA(context.Background(), nil); out != nil || !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatal("nil disable")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if out, err := callMFA(ctx, c, true, "user-1", "google"); out != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
}

func TestUserMFANoopAndVerification(t *testing.T) {
	for _, enable := range []bool{false, true} {
		for _, verified := range []bool{false, true} {
			posts, reads := 0, 0
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost {
					posts++
					t.Error("no-op sent mutation")
					return
				}
				reads++
				body := resetGuardBody(r.URL.Path)
				if strings.HasSuffix(r.URL.Path, "/iam-users/user-1") {
					body = mfaUser(verified, enable)
				}
				_, _ = io.WriteString(w, body)
			}))
			out, err := callMFA(context.Background(), c, enable, "user-1", "google")
			if posts != 0 || reads != 6 {
				t.Fatalf("posts=%d reads=%d", posts, reads)
			}
			if enable && !verified {
				if out != nil || err == nil || errors.Is(err, vngcloud.ErrInvalidInput) || vngcloud.ErrorCode(err) != "RequestFailed" {
					t.Fatalf("unverified enable: %+v %v", out, err)
				}
			} else if err != nil || out == nil || out.Changed || out.Enabled == nil || *out.Enabled != enable {
				t.Fatalf("no-op: %+v %v", out, err)
			}
		}
	}
}
