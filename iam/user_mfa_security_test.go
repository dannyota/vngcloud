package iam

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/endpoints"
	"danny.vn/vngcloud/internal/transport"
)

func TestUserMFAGuards(t *testing.T) {
	for _, enable := range []bool{false, true} {
		for _, tc := range []struct {
			name, path, body string
			want             error
			reads            int
		}{
			{"self", "/accounts-api/v1/auth/userinfo", `{"userId":"USER-1","userType":"iam-user"}`, ErrSelfChange, 1},
			{"root", "/accounts-api/v1/auth/userinfo", `{"userId":"caller","userType":"root-user"}`, ErrPrivilegedChange, 1},
			{"unknown-caller", "/accounts-api/v1/auth/userinfo", `{"userId":"caller","userType":"unknown"}`, ErrPrivilegedChange, 1},
			{"missing-caller-id", "/accounts-api/v1/auth/userinfo", `{"userType":"iam-user"}`, ErrPrivilegedChange, 1},
			{"invalid-caller-id", "/accounts-api/v1/auth/userinfo", `{"userId":"../x","userType":"iam-user"}`, ErrPrivilegedChange, 1},
			{"non-iam", "/accounts-api/v1/iam-users/user-1", `{"id":"user-1","from":"idp"}`, ErrOriginNotIAM, 2},
			{"null-origin", "/accounts-api/v1/iam-users/user-1", `{"id":"user-1","from":null}`, ErrOriginNotIAM, 2},
			{"wrong-origin-type", "/accounts-api/v1/iam-users/user-1", `{"id":"user-1","from":17}`, ErrOriginNotIAM, 2},
			{"mismatched-id", "/accounts-api/v1/iam-users/user-1", `{"id":"other","from":"iam"}`, ErrOriginNotIAM, 2},
			{"duplicate-target", "/accounts-api/v1/iam-users/user-1", `{"id":"user-1","ID":"other","from":"iam"}`, ErrOriginNotIAM, 2},
			{"direct-privilege", "/policies-api/v1/policies/policy-1", `{"id":"policy-1","statements":[{"effect":"allow","actions":["iam:CreatePolicy"]}]}`, ErrPrivilegedChange, 5},
			{"null-statement", "/policies-api/v1/policies/policy-1", `{"id":"policy-1","statements":[null]}`, ErrPrivilegedChange, 5},
			{"unknown-effect", "/policies-api/v1/policies/policy-1", `{"id":"policy-1","statements":[{"effect":"unknown","actions":["safe:read"]}]}`, ErrPrivilegedChange, 5},
			{"missing-actions", "/policies-api/v1/policies/policy-1", `{"id":"policy-1","statements":[{"effect":"allow"}]}`, ErrPrivilegedChange, 5},
			{"user-sa", "/accounts-api/v1/auth/userinfo", `{"userId":"user-1","userType":"user-sa"}`, nil, 6},
			{"service-sa", "/accounts-api/v1/auth/userinfo", `{"userId":"user-1","userType":"service-sa"}`, nil, 6},
		} {
			t.Run(tc.name, func(t *testing.T) {
				reads, posts := 0, 0
				c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method == http.MethodPost {
						posts++
						w.WriteHeader(204)
						return
					}
					reads++
					body := resetGuardBody(r.URL.Path)
					if r.URL.Path == "/accounts-api/v1/iam-users/user-1" {
						body = mfaUser(true, enable)
					}
					if r.URL.Path == tc.path {
						body = tc.body
					}
					_, _ = io.WriteString(w, body)
				}))
				out, err := callMFA(context.Background(), c, enable, "user-1", "google")
				if reads != tc.reads || (tc.want != nil && !errors.Is(err, tc.want)) || (err == nil) != (tc.want == nil) || (out != nil) != (tc.want == nil) {
					t.Fatalf("reads=%d posts=%d err=%v", reads, posts, err)
				}
				if posts != 0 {
					t.Fatal("guard sent write")
				}
			})
		}
	}
}

func TestUserMFAIncompleteGuards(t *testing.T) {
	for _, tc := range []struct{ path, body string }{
		{"/policies-api/v1/actions", `[]`},
		{"/policies-api/v1/user-attachments/iam-users/user-1/policies", `{"data":[],"totalItems":1}`},
		{"/policies-api/v1/user-attachments/iam-users/user-1/policies", `{"data":null,"totalItems":0}`},
		{"/policies-api/v1/user-attachments/iam-users/user-1/groups", `[{"id":"group-1","policies":null}]`},
		{"/policies-api/v1/user-attachments/iam-users/user-1/groups", `[{"id":"group-1"}]`},
	} {
		posts := 0
		c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPost {
				posts++
				return
			}
			body := resetGuardBody(r.URL.Path)
			if r.URL.Path == "/accounts-api/v1/iam-users/user-1" {
				body = mfaUser(true, true)
			}
			if r.URL.Path == tc.path {
				body = tc.body
			}
			_, _ = io.WriteString(w, body)
		}))
		out, err := callMFA(context.Background(), c, true, "user-1", "google")
		if out != nil || err == nil || posts != 0 {
			t.Fatal("incomplete guard authorized MFA")
		}
	}
}

func TestUserMFAGroupPrivilege(t *testing.T) {
	posts := 0
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			posts++
			return
		}
		body := resetGuardBody(r.URL.Path)
		if r.URL.Path == "/accounts-api/v1/iam-users/user-1" {
			body = mfaUser(true, true)
		}
		if strings.HasSuffix(r.URL.Path, "/user-1/policies") {
			body = `{"data":[],"totalItems":0}`
		}
		if strings.HasSuffix(r.URL.Path, "/policies/policy-1") {
			body = `{"id":"policy-1","statements":[{"effect":"allow","actions":["iam:CreatePolicy"]}]}`
		}
		_, _ = io.WriteString(w, body)
	}))
	out, err := callMFA(context.Background(), c, true, "user-1", "google")
	if out != nil || posts != 0 || !errors.Is(err, ErrPrivilegedChange) {
		t.Fatalf("group protection: %v", err)
	}
}

func TestUserMFAGuardPrivacy(t *testing.T) {
	for _, warm := range []bool{false, true} {
		for _, edge := range privateGuardReads()[:6] {
			if warm && (edge.name == "caller" || edge.name == "actions") {
				continue
			}
			for _, mode := range []string{"error", "malformed", "echo"} {
				t.Run(edge.name+"/"+mode+"/warm="+strconv.FormatBool(warm), func(t *testing.T) {
					var posts, captures, hits atomic.Int32
					var armed atomic.Bool
					var logs lockedGuardLog
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						if r.Method == http.MethodPost {
							posts.Add(1)
							w.WriteHeader(204)
							return
						}
						if armed.Load() && r.URL.Path == edge.path {
							hits.Add(1)
							switch mode {
							case "error":
								w.WriteHeader(403)
								_, _ = io.WriteString(w, `{"code":"hostile-canary","message":"hostile-canary bearer-canary"}`)
							case "malformed":
								_, _ = io.WriteString(w, `{"id":"bearer-canary","root":{"$numberLong":"hostile-canary"}`)
							case "echo":
								_, _ = io.WriteString(w, strings.ReplaceAll(strings.ReplaceAll(resetEchoBody(edge.name), "password-canary", "bearer-canary"), "passwordcanary", "bearer-canary"))
							}
							return
						}
						body := resetGuardBody(r.URL.Path)
						if r.URL.Path == "/accounts-api/v1/iam-users/user-1" {
							body = mfaUser(true, false)
						}
						_, _ = io.WriteString(w, body)
					}))
					defer server.Close()
					c := privateGuardClient(server, &logs, &captures)
					if warm {
						if _, err := c.guardCallerIdentity(context.Background()); err != nil {
							t.Fatal(err)
						}
						if _, err := c.guardWriteActionNames(context.Background()); err != nil {
							t.Fatal(err)
						}
						captures.Store(0)
					}
					armed.Store(true)
					out, err := callMFA(context.Background(), c, true, "user-1", "google")
					if out != nil || err == nil || posts.Load() != 0 || hits.Load() != 1 || captures.Load() != 0 {
						t.Fatalf("privacy: posts=%d hits=%d captures=%d err=%v", posts.Load(), hits.Load(), captures.Load(), err)
					}
					assertUserErrorPrivate(t, err, "hostile-canary", "bearer-canary")
					if strings.Contains(logs.String(), "canary") {
						t.Fatal("credential logged")
					}
				})
			}
		}
	}
}

func TestUserMFARefreshCredentialEcho(t *testing.T) {
	for _, phase := range []string{"target", "confirm"} {
		for _, field := range []string{"id", "username"} {
			for _, echo := range []string{"first-bearer", "refreshed-bearer"} {
				var posts, targets atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method == http.MethodPost {
						posts.Add(1)
						w.WriteHeader(204)
						return
					}
					body := resetGuardBody(r.URL.Path)
					if r.URL.Path == "/accounts-api/v1/iam-users/user-1" {
						n := targets.Add(1)
						body = mfaUser(true, false)
						armed := phase == "target" || posts.Load() != 0
						if armed {
							first := int32(1)
							if phase == "confirm" {
								first = 2
							}
							if n == first {
								w.WriteHeader(401)
								return
							}
							body = strings.Replace(mfaUser(true, true), `"`+field+`":"`+map[string]string{"id": "user-1", "username": "ordinary"}[field]+`"`, `"`+field+`":"`+echo+`"`, 1)
						}
					}
					_, _ = io.WriteString(w, body)
				}))
				cfg := core.NewTestConfig("hcm-3", "", endpoints.Set{Dashboard: server.URL + "/", IAM: server.URL + "/"}, transport.New(transport.Config{HTTPClient: server.Client(), TokenSource: new(userWriteTokens), RetryCount: 3}))
				out, err := callMFA(context.Background(), New(cfg), true, "user-1", "google")
				server.Close()
				if phase == "target" {
					if out != nil || err == nil || posts.Load() != 0 || targets.Load() != 2 {
						t.Fatalf("target echo: %v", err)
					}
				} else if out == nil || !out.Changed || out.Enabled != nil || !errors.Is(err, ErrNotSettled) || posts.Load() != 1 || targets.Load() != 3 {
					t.Fatalf("confirm echo: %+v %v", out, err)
				}
				assertUserErrorPrivate(t, err, "first-bearer", "refreshed-bearer")
			}
		}
	}
}

func TestUserMFAPostAndConfirmationPrivacy(t *testing.T) {
	for _, phase := range []string{"post", "confirm"} {
		var posts, captures atomic.Int32
		var logs lockedGuardLog
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPost {
				posts.Add(1)
				if phase == "confirm" {
					w.WriteHeader(204)
					return
				}
				w.WriteHeader(500)
				_, _ = io.WriteString(w, `{"code":"hostile-canary","message":"bearer-canary"}`)
				return
			}
			if posts.Load() != 0 {
				w.WriteHeader(403)
				_, _ = io.WriteString(w, `{"code":"hostile-canary","message":"bearer-canary"}`)
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
		want := ErrUserWriteUncertain
		if phase == "confirm" {
			want = ErrNotSettled
		}
		if !errors.Is(err, want) || posts.Load() != 1 || captures.Load() != 0 {
			t.Fatalf("privacy outcome: %v", err)
		}
		if phase == "post" && out != nil {
			t.Fatal("uncertain output retained")
		}
		assertUserErrorPrivate(t, err, "hostile-canary", "bearer-canary")
		if strings.Contains(logs.String(), "canary") {
			t.Fatal("credential logged")
		}
	}
}

func TestUserMFAOpposingConcurrentWrites(t *testing.T) {
	var posts, targets, captures atomic.Int32
	var logs lockedGuardLog
	firstRead, bothRead := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			posts.Add(1)
			w.WriteHeader(204)
			return
		}
		body := resetGuardBody(r.URL.Path)
		if r.URL.Path == "/accounts-api/v1/iam-users/user-1" {
			switch targets.Add(1) {
			case 1:
				close(firstRead)
				<-bothRead
				body = mfaUser(true, false)
			case 2:
				close(bothRead)
				body = mfaUser(true, true)
			default:
				body = mfaUser(true, false)
			}
		}
		_, _ = io.WriteString(w, body)
	}))
	defer server.Close()
	c := privateGuardClient(server, &logs, &captures)
	var wg sync.WaitGroup
	wg.Go(func() {
		out, err := callMFA(context.Background(), c, true, "user-1", "google")
		if out == nil || !out.Changed || out.Enabled == nil || *out.Enabled || !errors.Is(err, ErrNotSettled) {
			t.Errorf("enable after opposing change: %+v %v", out, err)
		}
	})
	<-firstRead
	wg.Go(func() {
		out, err := callMFA(context.Background(), c, false, "user-1", "google")
		if out == nil || !out.Changed || out.Enabled == nil || *out.Enabled || err != nil {
			t.Errorf("disable after opposing change: %+v %v", out, err)
		}
	})
	wg.Wait()
	if posts.Load() != 2 || targets.Load() != 4 || captures.Load() != 0 {
		t.Fatalf("posts=%d targets=%d captures=%d", posts.Load(), targets.Load(), captures.Load())
	}
}

func TestUserMFATargetEchoOfLaterGuardCredential(t *testing.T) {
	var posts, policyReads atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			posts.Add(1)
			w.WriteHeader(204)
			return
		}
		body := resetGuardBody(r.URL.Path)
		if r.URL.Path == "/accounts-api/v1/iam-users/user-1" {
			body = strings.Replace(mfaUser(true, false), "ordinary", "refreshed-bearer", 1)
		}
		if strings.HasSuffix(r.URL.Path, "/policies/policy-1") && policyReads.Add(1) == 1 {
			w.WriteHeader(401)
			return
		}
		_, _ = io.WriteString(w, body)
	}))
	defer server.Close()
	cfg := core.NewTestConfig("hcm-3", "", endpoints.Set{Dashboard: server.URL + "/", IAM: server.URL + "/"}, transport.New(transport.Config{HTTPClient: server.Client(), TokenSource: new(userWriteTokens), RetryCount: 3}))
	out, err := callMFA(context.Background(), New(cfg), true, "user-1", "google")
	if out != nil || err == nil || posts.Load() != 0 {
		t.Fatalf("later credential authorized write: posts=%d out=%+v err=%v", posts.Load(), out, err)
	}
	assertUserErrorPrivate(t, err, "first-bearer", "refreshed-bearer")
}

type mfaChangingTokens struct{ calls atomic.Int32 }

func (s *mfaChangingTokens) Token(context.Context) (transport.Token, error) {
	value := "first-bearer"
	if s.calls.Add(1) > 6 {
		value = "user-1"
	}
	return transport.Token{AccessToken: value}, nil
}
func (*mfaChangingTokens) Invalidate(string) {}

func TestUserMFAPostCredentialRefusesConfirmationPath(t *testing.T) {
	var posts, after atomic.Int32
	tokens := new(mfaChangingTokens)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			posts.Add(1)
			w.WriteHeader(204)
			return
		}
		if posts.Load() != 0 {
			after.Add(1)
		}
		body := resetGuardBody(r.URL.Path)
		if r.URL.Path == "/accounts-api/v1/iam-users/user-1" {
			body = mfaUser(true, posts.Load() != 0)
		}
		_, _ = io.WriteString(w, body)
	}))
	defer server.Close()
	cfg := core.NewTestConfig("hcm-3", "", endpoints.Set{Dashboard: server.URL + "/", IAM: server.URL + "/"}, transport.New(transport.Config{HTTPClient: server.Client(), TokenSource: tokens}))
	out, err := callMFA(context.Background(), New(cfg), true, "user-1", "google")
	if posts.Load() != 1 || after.Load() != 0 || tokens.calls.Load() != 7 || out == nil || !out.Changed || out.Enabled != nil || !errors.Is(err, ErrNotSettled) {
		t.Fatalf("confirmation reused credential: posts=%d after=%d tokens=%d", posts.Load(), after.Load(), tokens.calls.Load())
	}
	assertUserErrorPrivate(t, err, "first-bearer", "user-1")
}
