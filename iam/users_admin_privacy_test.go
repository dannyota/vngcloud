package iam

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/endpoints"
	"danny.vn/vngcloud/internal/transport"
)

func TestResetUserPasswordGuardPrivacy(t *testing.T) {
	for _, warm := range []bool{false, true} {
		for _, edge := range privateGuardReads()[:6] {
			if warm && (edge.name == "caller" || edge.name == "actions") {
				continue
			}
			for _, mode := range []string{"error", "malformed", "echo"} {
				t.Run(strconv.FormatBool(warm)+"/"+edge.name+"/"+mode, func(t *testing.T) {
					var posts, captures, hits atomic.Int32
					var logs lockedGuardLog
					armed := atomic.Bool{}
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
								_, _ = io.WriteString(w, `{"code":"{\"password\":\"password-canary\"}","message":"arbitrary-server-text"}`)
							case "malformed":
								_, _ = io.WriteString(w, `{"id":"password-canary","root":{"$numberLong":"password-canary"}`)
							case "echo":
								_, _ = io.WriteString(w, resetEchoBody(edge.name))
							}
							return
						}
						_, _ = io.WriteString(w, resetGuardBody(r.URL.Path))
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
					out, err := c.ResetUserPassword(context.Background(), &ResetUserPasswordInput{UserID: "user-1", Password: "password-canary"})
					if out != nil || err == nil || posts.Load() != 0 || hits.Load() != 1 || captures.Load() != 0 {
						t.Fatalf("posts=%d hits=%d captures=%d err=%v", posts.Load(), hits.Load(), captures.Load(), err)
					}
					if mode == "error" && !errors.Is(err, vngcloud.ErrPermission) {
						t.Fatal("guard error identity lost")
					}
					assertUserErrorPrivate(t, err, "password-canary", "bearer-canary", "arbitrary-server-text")
					if strings.Contains(logs.String(), "password-canary") || strings.Contains(logs.String(), "bearer-canary") {
						t.Fatal("credential logged")
					}
				})
			}
		}
	}
}

func TestResetUserPasswordWarmCacheCredentialRefusal(t *testing.T) {
	for _, cache := range []string{"caller", "actions"} {
		posts := 0
		c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPost {
				posts++
				w.WriteHeader(204)
				return
			}
			body := resetGuardBody(r.URL.Path)
			if cache == "caller" && strings.HasSuffix(r.URL.Path, "/userinfo") {
				body = `{"userId":"caller","userType":"iam-user","username":"warm-canary"}`
			}
			if cache == "actions" && strings.HasSuffix(r.URL.Path, "/actions") {
				body = `[{"action":"warm-canary","label":"Write"}]`
			}
			_, _ = io.WriteString(w, body)
		}))
		if _, err := c.guardCallerIdentity(context.Background()); err != nil {
			t.Fatal(err)
		}
		if _, err := c.guardWriteActionNames(context.Background()); err != nil {
			t.Fatal(err)
		}
		out, err := c.ResetUserPassword(context.Background(), &ResetUserPasswordInput{UserID: "user-1", Password: "WARM-CANARY"})
		if out != nil || err == nil || posts != 0 {
			t.Fatal("cached credential authorized reset")
		}
		assertUserErrorPrivate(t, err, "warm-canary", "WARM-CANARY")
	}
}

func TestResetUserPasswordRefreshCredentialRefusal(t *testing.T) {
	for _, field := range []string{"id", "username"} {
		for _, echo := range []string{"first-bearer", "refreshed-bearer"} {
			var posts, target atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost {
					posts.Add(1)
					w.WriteHeader(204)
					return
				}
				if r.URL.Path == "/accounts-api/v1/iam-users/user-1" {
					if target.Add(1) == 1 {
						w.WriteHeader(401)
						return
					}
					body := map[string]string{"id": "user-1", "from": "iam", "username": "ordinary"}
					body[field] = echo
					_ = json.NewEncoder(w).Encode(body)
					return
				}
				_, _ = io.WriteString(w, resetGuardBody(r.URL.Path))
			}))
			defer server.Close()
			cfg := core.NewTestConfig("hcm-3", "", endpoints.Set{Dashboard: server.URL + "/", IAM: server.URL + "/"}, transport.New(transport.Config{HTTPClient: server.Client(), TokenSource: new(userWriteTokens), RetryCount: 3}))
			out, err := New(cfg).ResetUserPassword(context.Background(), &ResetUserPasswordInput{UserID: "user-1", Password: "password-canary"})
			if out != nil || err == nil || target.Load() != 2 || posts.Load() != 0 {
				t.Fatalf("target=%d posts=%d err=%v", target.Load(), posts.Load(), err)
			}
			assertUserErrorPrivate(t, err, "first-bearer", "refreshed-bearer", "password-canary")
		}
	}
}

func TestResetUserPasswordPostPrivacy(t *testing.T) {
	for _, status := range []int{200, 302, 400, 401, 403, 404, 409, 429, 500} {
		var captures, posts atomic.Int32
		var logs lockedGuardLog
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPost {
				posts.Add(1)
				w.WriteHeader(status)
				_, _ = io.WriteString(w, `{"code":"{\"password\":\"password-canary\"}","message":"bearer-canary arbitrary-server-text"}`)
				return
			}
			_, _ = io.WriteString(w, resetGuardBody(r.URL.Path))
		}))
		c := privateGuardClient(server, &logs, &captures)
		out, err := c.ResetUserPassword(context.Background(), &ResetUserPasswordInput{UserID: "user-1", Password: "password-canary"})
		if err == nil || posts.Load() != 1 || captures.Load() != 0 {
			t.Fatal("unsafe reset diagnostics")
		}
		assertUserErrorPrivate(t, err, "password-canary", "bearer-canary", "arbitrary-server-text")
		if strings.Contains(logs.String(), "canary") {
			t.Fatal("credential logged")
		}
		raw, _ := json.Marshal(out) //nolint:gosec // Verify Secret redaction in serialized output.
		if bytes.Contains(raw, []byte("canary")) {
			t.Fatal("credential output")
		}
		server.Close()
	}
}

func TestResetUserPasswordConcurrentPrivacy(t *testing.T) {
	var posts, captures atomic.Int32
	var logs lockedGuardLog
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			posts.Add(1)
			w.WriteHeader(204)
			return
		}
		body := resetGuardBody(r.URL.Path)
		if strings.Contains(r.URL.Path, "/iam-users/user-") && !strings.Contains(r.URL.Path, "attachments") {
			id := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
			body = fmt.Sprintf(`{"id":%q,"username":"other-operation-password","from":"iam"}`, id)
		}
		if strings.Contains(r.URL.Path, "/user-2/") {
			if strings.HasSuffix(r.URL.Path, "/policies") {
				body = `{"data":[],"totalItems":0}`
			} else {
				body = `[]`
			}
		}
		_, _ = io.WriteString(w, body)
	}))
	defer server.Close()
	c := privateGuardClient(server, &logs, &captures)
	var wg sync.WaitGroup
	for _, tc := range []struct {
		id, password string
		refused      bool
	}{{"user-1", "other-operation-password", true}, {"user-2", "unrelated-password", false}} {
		wg.Go(func() {
			out, err := c.ResetUserPassword(context.Background(), &ResetUserPasswordInput{UserID: tc.id, Password: vngcloud.Secret(tc.password)})
			if tc.refused {
				if out != nil || err == nil {
					t.Error("credential echo allowed")
				}
			} else if out == nil || err != nil {
				t.Errorf("options crossed calls: %v", err)
			}
		})
	}
	wg.Wait()
	if posts.Load() != 1 || captures.Load() != 0 {
		t.Fatalf("posts=%d captures=%d", posts.Load(), captures.Load())
	}
}

func resetEchoBody(edge string) string {
	switch edge {
	case "caller":
		return `{"userId":"caller","userType":"iam-user","username":"password-canary"}`
	case "target":
		return `{"id":"user-1","from":"iam","username":"password-canary"}`
	case "actions":
		return `[{"action":"password-canary","label":"Write"}]`
	case "policy":
		return `{"id":"policy-1","description":"password-canary","statements":[]}`
	case "page":
		return `{"data":[{"id":"password-canary"}],"totalItems":1}`
	default:
		return `[{"id":"password-canary","policies":[]}]`
	}
}

func TestResetUserPasswordGuardBoundsAndRedirects(t *testing.T) {
	for _, edge := range privateGuardReads()[:6] {
		for _, mode := range []string{"overflow", "redirect"} {
			t.Run(edge.name+"/"+mode, func(t *testing.T) {
				var posts, foreign, captures atomic.Int32
				var logs lockedGuardLog
				destination := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { foreign.Add(1) }))
				defer destination.Close()
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method == http.MethodPost {
						posts.Add(1)
						w.WriteHeader(204)
						return
					}
					if r.URL.Path == edge.path {
						if mode == "redirect" {
							w.Header().Set("Location", destination.URL+"/foreign")
							w.WriteHeader(302)
						} else {
							size := userObjectMaxBody
							if edge.name == "actions" || edge.name == "page" || edge.name == "groups" {
								size = userListMaxBody
							}
							_, _ = io.WriteString(w, strings.Repeat("x", size+1))
						}
						return
					}
					_, _ = io.WriteString(w, resetGuardBody(r.URL.Path))
				}))
				defer server.Close()
				out, err := privateGuardClient(server, &logs, &captures).ResetUserPassword(context.Background(), &ResetUserPasswordInput{UserID: "user-1", Password: "password-canary"})
				if out != nil || err == nil || posts.Load() != 0 || foreign.Load() != 0 || captures.Load() != 0 {
					t.Fatalf("posts=%d foreign=%d captures=%d err=%v", posts.Load(), foreign.Load(), captures.Load(), err)
				}
			})
		}
	}
}

func TestResetUserPasswordGeneratedEchoPrivacy(t *testing.T) {
	var captures atomic.Int32
	var logs lockedGuardLog
	var submitted string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			var body map[string]string
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			submitted = body["password"]
			w.WriteHeader(502)
			_ = json.NewEncoder(w).Encode(map[string]string{"code": `{"password":"` + submitted + `"}`, "message": submitted})
			return
		}
		_, _ = io.WriteString(w, resetGuardBody(r.URL.Path))
	}))
	defer server.Close()
	out, err := privateGuardClient(server, &logs, &captures).ResetUserPassword(context.Background(), &ResetUserPasswordInput{UserID: "user-1"})
	if out == nil || out.Password.Reveal() != submitted || !errors.Is(err, ErrUserWriteUncertain) || captures.Load() != 0 {
		t.Fatalf("generated outcome: %v", err)
	}
	assertUserErrorPrivate(t, err, submitted)
	raw, _ := json.Marshal(out) //nolint:gosec // Verify Secret redaction in serialized output.
	for _, value := range []string{string(raw), fmt.Sprintf("%v", out), fmt.Sprintf("%+v", out), fmt.Sprintf("%#v", out), logs.String()} {
		if strings.Contains(value, submitted) {
			t.Fatal("generated secret in diagnostics")
		}
	}
}

func TestResetUserPasswordCompetingResets(t *testing.T) {
	var posts, captures atomic.Int32
	var logs lockedGuardLog
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			posts.Add(1)
			w.WriteHeader(204)
			return
		}
		_, _ = io.WriteString(w, resetGuardBody(r.URL.Path))
	}))
	defer server.Close()
	c := privateGuardClient(server, &logs, &captures)
	passwords := make(chan vngcloud.Secret, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() {
			out, err := c.ResetUserPassword(context.Background(), &ResetUserPasswordInput{UserID: "user-1"})
			if err != nil || out == nil || out.Password == "" {
				t.Errorf("competing reset: %v", err)
				return
			}
			passwords <- out.Password
		})
	}
	wg.Wait()
	close(passwords)
	// Both acceptances are reportable even though only the server's final
	// password can work. The SDK never attempts to sign in or resolve the race.
	if posts.Load() != 2 || len(passwords) != 2 || captures.Load() != 0 {
		t.Fatalf("posts=%d outputs=%d captures=%d", posts.Load(), len(passwords), captures.Load())
	}
}
