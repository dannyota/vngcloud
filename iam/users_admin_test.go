package iam

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/testutil"
)

// resetGuardBody models the evidenced empty attachments and a harmless policy.
func resetGuardBody(path string) string {
	if strings.HasSuffix(path, "/user-1/policies") {
		return `{"data":[{"id":"policy-1"}],"totalItems":1}`
	}
	if strings.HasSuffix(path, "/user-1/groups") {
		return `[{"id":"group-1","policies":["policy-1"]}]`
	}
	return privateGuardBody(path)
}

func TestResetUserPasswordStatuses(t *testing.T) {
	for _, generated := range []bool{false, true} {
		for _, status := range []int{204, 200, 202, 301, 302, 303, 307, 308, 400, 401, 403, 404, 409, 429, 500, 502, 503, 504} {
			t.Run(strconv.Itoa(status)+"/generated="+strconv.FormatBool(generated), func(t *testing.T) {
				posts, reads, after := 0, 0, 0
				var submitted string
				c := New(testutil.NewRetryConfig(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method == http.MethodPost {
						posts++
						if r.URL.Path != "/accounts-api/v1/iam-users/user-1/reset-password" {
							t.Error("wrong reset path")
						}
						var body map[string]string
						if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
							t.Error(err)
						}
						if len(body) != 1 {
							t.Error("unexpected body fields")
						}
						submitted = body["password"]
						w.Header().Set("Location", "/redirect")
						w.WriteHeader(status)
						if status != 204 {
							_, _ = w.Write([]byte(`{"code":"password-canary","message":"password-canary"}`))
						}
						return
					}
					reads++
					if posts > 0 {
						after++
					}
					_, _ = w.Write([]byte(resetGuardBody(r.URL.Path)))
				})))
				in := &ResetUserPasswordInput{UserID: "user-1", Password: vngcloud.Secret("password-canary")}
				if generated {
					in.Password = ""
				}
				original := *in
				out, err := c.ResetUserPassword(context.Background(), in)
				if posts != 1 || reads != 6 || after != 0 || *in != original {
					t.Fatalf("posts=%d reads=%d after=%d", posts, reads, after)
				}
				switch {
				case status == 204:
					if err != nil || out == nil {
						t.Fatalf("acceptance: %v", err)
					}
				case status >= 400 && status < 500:
					var api *vngcloud.APIError
					if out != nil || !errors.As(err, &api) || api.StatusCode != status {
						t.Fatalf("rejection: %v", err)
					}
					for code, sentinel := range map[int]error{401: vngcloud.ErrAuth, 403: vngcloud.ErrPermission, 404: vngcloud.ErrNotFound, 429: vngcloud.ErrRateLimited} {
						if status == code && !errors.Is(err, sentinel) {
							t.Fatal("status identity lost")
						}
					}
					if status == 409 && vngcloud.ErrorCode(err) != "Conflict" {
						t.Fatal("conflict identity lost")
					}
				default:
					if out == nil || !errors.Is(err, ErrUserWriteUncertain) {
						t.Fatalf("uncertain: %v", err)
					}
				}
				if out != nil {
					if generated {
						if out.Password.Reveal() != submitted || len(submitted) != 32 || !passwordClasses([]byte(submitted)) {
							t.Fatal("generated password lost")
						}
					} else if out.Password != "" || submitted != "password-canary" {
						t.Fatal("supplied password returned or changed")
					}
				}
				assertUserErrorPrivate(t, err, "password-canary")
			})
		}
	}
}

func TestResetUserPasswordValidation(t *testing.T) {
	calls := 0
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls++ }))
	for _, in := range []*ResetUserPasswordInput{nil, {}, {UserID: "../x"}, {UserID: "user-1", PasswordLength: 7}, {UserID: "user-1", PasswordLength: 51}, {UserID: "user-1", Password: "supplied", PasswordLength: 8}, {UserID: "user-1", Password: "x\n"}, {UserID: "user-1", Password: vngcloud.Secret(string([]byte{255}))}, {UserID: "user-1", Password: vngcloud.Secret(strings.Repeat("x", 4097))}} {
		out, err := c.ResetUserPassword(context.Background(), in)
		if out != nil || !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Fatalf("validation: %v", err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	out, err := c.ResetUserPassword(ctx, &ResetUserPasswordInput{UserID: "user-1"})
	if out != nil || !errors.Is(err, context.Canceled) || calls != 0 {
		t.Fatalf("cancellation calls=%d: %v", calls, err)
	}
}
