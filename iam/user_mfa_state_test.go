package iam

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"danny.vn/vngcloud"
)

func TestUserMFANullableDecode(t *testing.T) {
	for _, tc := range []struct {
		state string
		want  string
	}{
		{``, ``}, {`,"twoFactorAuth":null`, ``},
		{`,"twoFactorAuth":{}`, `"twoFactorAuth":{"email":null,"googleAuthenticator":null}`},
		{`,"twoFactorAuth":{"email":null,"googleAuthenticator":{"verified":null}}`, `"googleAuthenticator":{"verified":null,"enabled":null}`},
		{`,"twoFactorAuth":{"googleAuthenticator":{"verified":false,"enabled":false,"extra":null}}`, `"googleAuthenticator":{"verified":false,"enabled":false}`},
		{`,"twoFactorAuth":{"googleAuthenticator":{"verified":true,"enabled":true}}`, `"googleAuthenticator":{"verified":true,"enabled":true}`},
	} {
		t.Run(tc.state, func(t *testing.T) {
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, `{"id":"user-1"`+tc.state+`}`) }))
			out, err := c.GetUser(context.Background(), &GetUserInput{UserID: "user-1"})
			if err != nil {
				t.Fatal(err)
			}
			raw, err := json.Marshal(out.User)
			if err != nil {
				t.Fatal(err)
			}
			if tc.want == "" {
				if out.User.MFA != nil || strings.Contains(string(raw), "twoFactorAuth") {
					t.Fatal("absent state became known")
				}
			} else if !strings.Contains(string(raw), tc.want) {
				t.Fatalf("nullable state lost: %s", raw)
			}
		})
	}
}

func TestUserMFAStrictState(t *testing.T) {
	for _, state := range []string{
		`"twoFactorAuth":false`, `"twoFactorAuth":[]`,
		`"twoFactorAuth":{},"twoFactorAuth":{}`, `"twoFactorAuth":{},"TWOFACTORAUTH":{}`,
		`"twoFactorAuth":{"googleAuthenticator":false}`, `"twoFactorAuth":{"googleAuthenticator":{},"googleAuthenticator":{}}`,
		`"twoFactorAuth":{"googleAuthenticator":{},"GOOGLEAUTHENTICATOR":{}}`,
		`"twoFactorAuth":{"googleAuthenticator":{"verified":true,"VERIFIED":false,"enabled":true}}`,
		`"twoFactorAuth":{"googleAuthenticator":{"verified":true,"verified":true,"enabled":true}}`,
		`"twoFactorAuth":{"googleAuthenticator":{"verified":true,"enabled":true,"ENABLED":false}}`,
		`"twoFactorAuth":{"googleAuthenticator":{"verified":"decode-canary","enabled":false}}`,
		`"twoFactorAuth":{"googleAuthenticator":{"verified":true,"enabled":0}}`,
		`"twoFactorAuth":{"email":{"verified":true,"VERIFIED":false}}`,
	} {
		t.Run(state, func(t *testing.T) {
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, `{"id":"user-1",`+state+`}`) }))
			out, err := c.GetUser(context.Background(), &GetUserInput{UserID: "user-1"})
			if out != nil || err == nil {
				t.Fatal("ambiguous state decoded")
			}
			assertUserErrorPrivate(t, err, "decode-canary")
		})
	}
}

func TestUserMFAUnavailableState(t *testing.T) {
	for _, enable := range []bool{false, true} {
		for _, state := range []string{``, `,"twoFactorAuth":null`, `,"twoFactorAuth":{}`, `,"twoFactorAuth":{"googleAuthenticator":null}`, `,"twoFactorAuth":{"googleAuthenticator":{}}`, `,"twoFactorAuth":{"googleAuthenticator":{"verified":null,"enabled":true}}`, `,"twoFactorAuth":{"googleAuthenticator":{"verified":true,"enabled":null}}`} {
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost {
					t.Error("missing state authorized write")
					return
				}
				body := resetGuardBody(r.URL.Path)
				if r.URL.Path == "/accounts-api/v1/iam-users/user-1" {
					body = `{"id":"user-1","from":"iam"` + state + `}`
				}
				_, _ = io.WriteString(w, body)
			}))
			out, err := callMFA(context.Background(), c, enable, "user-1", "google")
			if out != nil || err == nil || errors.Is(err, vngcloud.ErrInvalidInput) || vngcloud.ErrorCode(err) != "RequestFailed" {
				t.Fatalf("unknown state: %+v %v", out, err)
			}
		}
	}
}

func TestUserMFAConfirmation(t *testing.T) {
	for _, enable := range []bool{false, true} {
		for _, mode := range []string{"stale", "missing", "wrong-id", "non-iam", "unverified", "null", "malformed", "404", "401"} {
			t.Run(fmt.Sprintf("%t/%s", enable, mode), func(t *testing.T) {
				posts, after := 0, 0
				c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method == http.MethodPost {
						posts++
						w.WriteHeader(204)
						return
					}
					body := resetGuardBody(r.URL.Path)
					if r.URL.Path == "/accounts-api/v1/iam-users/user-1" {
						body = mfaUser(true, !enable)
						if posts > 0 {
							after++
							switch mode {
							case "stale":
							case "missing":
								body = `{"id":"user-1","from":"iam"}`
							case "wrong-id":
								body = strings.Replace(mfaUser(true, enable), "user-1", "user-2", 1)
							case "non-iam":
								body = strings.Replace(mfaUser(true, enable), `"iam"`, `"idp"`, 1)
							case "unverified":
								body = mfaUser(false, enable)
							case "null":
								body = `null`
							case "malformed":
								body = `{"id":`
							case "404":
								w.WriteHeader(404)
								return
							case "401":
								w.WriteHeader(401)
								return
							}
						}
					}
					_, _ = io.WriteString(w, body)
				}))
				out, err := callMFA(context.Background(), c, enable, "user-1", "google")
				if posts != 1 || out == nil || !out.Changed || after < 1 || after > 2 {
					t.Fatalf("counts: posts=%d after=%d out=%+v", posts, after, out)
				}
				if mode == "unverified" && !enable {
					if err != nil || out.Enabled == nil || *out.Enabled {
						t.Fatalf("disable requires verification: %+v %v", out, err)
					}
					return
				}
				if !errors.Is(err, ErrNotSettled) || errors.Is(err, ErrUserWriteUncertain) {
					t.Fatalf("lost acceptance: %v", err)
				}
				if mode == "stale" || mode == "unverified" {
					if out.Enabled == nil {
						t.Fatal("fresh known state dropped")
					}
				} else if out.Enabled != nil {
					t.Fatal("untrusted confirmation retained")
				}
				if mode == "404" && !errors.Is(err, vngcloud.ErrNotFound) {
					t.Fatal("lost nested not found")
				}
				if mode == "401" && !errors.Is(err, vngcloud.ErrAuth) {
					t.Fatal("lost nested auth")
				}
			})
		}
	}
}

func TestUserMFADisableUnverifiedEnabled(t *testing.T) {
	posts := 0
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			posts++
			w.WriteHeader(204)
			return
		}
		body := resetGuardBody(r.URL.Path)
		if r.URL.Path == "/accounts-api/v1/iam-users/user-1" {
			body = mfaUser(false, posts == 0)
		}
		_, _ = io.WriteString(w, body)
	}))
	out, err := callMFA(context.Background(), c, false, "user-1", "google")
	if err != nil || out == nil || !out.Changed || out.Enabled == nil || *out.Enabled || posts != 1 {
		t.Fatalf("disable: %+v %v", out, err)
	}
}
