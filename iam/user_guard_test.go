package iam

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/testutil"
)

func TestDeleteUserGuard(t *testing.T) {
	for _, tc := range []struct {
		name, caller, target string
		want                 error
		reads                int
	}{
		{"self", `{"userId":"USER-1","userType":"iam-user"}`, ``, ErrSelfChange, 1},
		{"root", `{"userId":"caller","userType":"root-user"}`, ``, ErrPrivilegedChange, 1},
		{"unknown", `{"userId":"caller","userType":"other"}`, ``, ErrPrivilegedChange, 1},
		{"missing-caller-id", `{"userType":"iam-user"}`, ``, ErrPrivilegedChange, 1},
		{"invalid-caller-id", `{"userId":"../x","userType":"iam-user"}`, ``, ErrPrivilegedChange, 1},
		{"other-origin", `{"userId":"caller","userType":"iam-user"}`, `{"id":"user-1","from":"root"}`, ErrOriginNotIAM, 2},
		{"missing-origin", `{"userId":"caller","userType":"iam-user"}`, `{"id":"user-1"}`, ErrOriginNotIAM, 2},
		{"null-origin", `{"userId":"caller","userType":"iam-user"}`, `{"id":"user-1","from":null}`, ErrOriginNotIAM, 2},
		{"wrong-type-origin", `{"userId":"caller","userType":"iam-user"}`, `{"id":"user-1","from":17}`, ErrOriginNotIAM, 2},
		{"mismatched-id", `{"userId":"caller","userType":"iam-user"}`, `{"id":"other","from":"iam"}`, ErrOriginNotIAM, 2},
		{"missing-id", `{"userId":"caller","userType":"iam-user"}`, `{"from":"iam"}`, ErrOriginNotIAM, 2},
		{"iam-caller", `{"userId":"caller","userType":"iam-user"}`, `{"id":"USER-1","from":"iam"}`, nil, 2},
		{"user-sa", `{"userId":"user-1","userType":"user-sa"}`, `{"id":"user-1","from":"iam"}`, nil, 2},
		{"service-sa", `{"userId":"user-1","userType":"service-sa"}`, `{"id":"user-1","from":"iam"}`, nil, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reads, deletes := 0, 0
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodDelete {
					deletes++
					if r.URL.Path != "/accounts-api/v1/iam-users/user-1" {
						t.Error("wrong delete path")
					}
					w.WriteHeader(204)
					return
				}
				reads++
				body := tc.target
				if strings.HasSuffix(r.URL.Path, "/auth/userinfo") {
					body = tc.caller
				}
				_, _ = w.Write([]byte(body))
			}))
			out, err := c.DeleteUser(context.Background(), &DeleteUserInput{UserID: "user-1"})
			if (err != nil) != (tc.want != nil) || tc.want != nil && !errors.Is(err, tc.want) || reads != tc.reads || (out != nil) != (tc.want == nil) {
				t.Fatalf("reads=%d deletes=%d err=%v", reads, deletes, err)
			}
			wantDeletes := 0
			if tc.want == nil {
				wantDeletes = 1
			}
			if deletes != wantDeletes {
				t.Fatal("guard sent mutation")
			}
		})
	}
}

func TestDeleteUserAmbiguousReads(t *testing.T) {
	caller := `{"userId":"caller","userType":"iam-user"}`
	target := `{"id":"user-1","username":"new-user","from":"iam"}`
	for _, dependency := range []string{"caller", "target"} {
		bodies := []string{``, `null`, `[]`, `{} {}`, `{"root":{"$numberLong":"strict-canary"}`, `{"root":1,"root":2}`}
		if dependency == "caller" {
			bodies = append(bodies, `{"userId":"caller","userId":"user-1","userType":"iam-user"}`, `{"userId":"caller","USERID":"user-1","userType":"iam-user"}`, `{"userId":"caller","userType":"iam-user","userType":"root-user"}`, `{"userId":"caller","userType":"iam-user","accountId":{"$numberLong":"strict-canary"}}`)
		} else {
			bodies = append(bodies, `{"id":"user-1","id":"other","from":"iam"}`, `{"id":"user-1","ID":"other","from":"iam"}`, `{"id":"user-1","username":"a","username":"b","from":"iam"}`, `{"id":"user-1","from":"iam","from":"root"}`, `{"id":"user-1","from":17}`, `{"id":"user-1","from":"iam","createdAt":{"$numberLong":"strict-canary"}}`)
		}
		for i, body := range bodies {
			t.Run(dependency+strconv.Itoa(i), func(t *testing.T) {
				deletes := 0
				c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method == http.MethodDelete {
						deletes++
						w.WriteHeader(204)
						return
					}
					response := target
					if strings.HasSuffix(r.URL.Path, "/auth/userinfo") {
						response = caller
						if dependency == "caller" {
							response = body
						}
					} else if dependency == "target" {
						response = body
					}
					_, _ = w.Write([]byte(response))
				}))
				out, err := c.DeleteUser(context.Background(), &DeleteUserInput{UserID: "user-1"})
				if out != nil || err == nil || deletes != 0 || strings.Contains(fmt.Sprintf("%+v", err), "strict-canary") {
					t.Fatalf("deletes=%d err=%v", deletes, err)
				}
			})
		}
	}
}

func TestDeleteUserStatusesAndRetries(t *testing.T) {
	for _, status := range []int{204, 401, 403, 404, 409, 500, 502, 301, 302, 303, 307, 308} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			deletes, reads := 0, 0
			c := New(testutil.NewRetryConfig(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodDelete {
					deletes++
					w.Header().Set("Location", "/foreign")
					w.WriteHeader(status)
					return
				}
				reads++
				if strings.HasSuffix(r.URL.Path, "/auth/userinfo") {
					_, _ = w.Write([]byte(`{"userId":"caller","userType":"iam-user"}`))
				} else {
					_, _ = w.Write([]byte(`{"id":"user-1","from":"iam"}`))
				}
			})))
			out, err := c.DeleteUser(context.Background(), &DeleteUserInput{UserID: "user-1"})
			wantDeletes := 1
			if status == 502 {
				wantDeletes = 4
			}
			if deletes != wantDeletes || reads != 2 || (out != nil) != (status == 204) || (err == nil) != (status == 204) {
				t.Fatalf("deletes=%d reads=%d err=%v", deletes, reads, err)
			}
			if status == 404 && !errors.Is(err, vngcloud.ErrNotFound) || status == 409 && vngcloud.ErrorCode(err) != "Conflict" {
				t.Fatal("status identity lost")
			}
		})
	}
}

func TestDeleteUserRetryEndsNotFound(t *testing.T) {
	deletes := 0
	c := New(testutil.NewRetryConfig(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			deletes++
			if deletes == 1 {
				w.WriteHeader(502)
			} else {
				w.WriteHeader(404)
			}
			return
		}
		if strings.HasSuffix(r.URL.Path, "/auth/userinfo") {
			_, _ = w.Write([]byte(`{"userId":"caller","userType":"iam-user"}`))
		} else {
			_, _ = w.Write([]byte(`{"id":"user-1","from":"iam"}`))
		}
	})))
	out, err := c.DeleteUser(context.Background(), &DeleteUserInput{UserID: "user-1"})
	if deletes != 2 || out != nil || !errors.Is(err, vngcloud.ErrNotFound) {
		t.Fatalf("deletes=%d err=%v", deletes, err)
	}
}

func TestDeleteUserDependencyLabels(t *testing.T) {
	for _, dependency := range []string{"caller identity", "target user"} {
		deletes := 0
		c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodDelete {
				deletes++
				return
			}
			if dependency == "caller identity" || !strings.HasSuffix(r.URL.Path, "/auth/userinfo") {
				w.WriteHeader(404)
				return
			}
			_, _ = w.Write([]byte(`{"userId":"caller","userType":"iam-user"}`))
		}))
		out, err := c.DeleteUser(context.Background(), &DeleteUserInput{UserID: "user-1"})
		if out != nil || deletes != 0 || !errors.Is(err, vngcloud.ErrNotFound) || !strings.Contains(err.Error(), "read "+dependency) {
			t.Fatalf("dependency: %v", err)
		}
	}
}

func TestDeleteUserInputAndCancellation(t *testing.T) {
	calls := 0
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls++ }))
	for _, in := range []*DeleteUserInput{nil, {}, {UserID: "../x"}} {
		if out, err := c.DeleteUser(context.Background(), in); out != nil || !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Fatal("invalid input accepted")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if out, err := c.DeleteUser(ctx, &DeleteUserInput{UserID: "user-1"}); out != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
	if calls != 0 {
		t.Fatal("local failure made request")
	}
}

// exercisePrivateUserGuard composes the existing guards without sending a write.
func exercisePrivateUserGuard(ctx context.Context, c *Client, id string, opts userReadOptions) error {
	const op = "iam.privateGuard"
	if err := c.guardUserWrite(ctx, op, id, opts); err != nil {
		return err
	}
	caller, err := c.guardCallerIdentity(ctx, opts)
	if err != nil {
		return err
	}
	actions, err := c.guardWriteActionNames(ctx, opts)
	if err != nil {
		return err
	}
	reason, err := c.userIsProtected(ctx, op, id, caller, actions, map[string]*Policy{}, opts)
	if err != nil {
		return err
	}
	if reason == protectedBySelf {
		return ErrSelfChange
	}
	if reason == protectedByPrivilege {
		return ErrPrivilegedChange
	}
	return nil
}

func TestPrivateUserGuardColdWarmCaches(t *testing.T) {
	for _, warm := range []bool{false, true} {
		for _, dependency := range []string{"", "target", "page", "groups", "policy"} {
			t.Run(strconv.FormatBool(warm)+"/"+dependency, func(t *testing.T) {
				var callerReads, actionReads, policyReads, captures atomic.Int32
				var logs lockedGuardLog
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					path := r.URL.Path
					if strings.HasSuffix(path, "/auth/userinfo") {
						callerReads.Add(1)
					}
					if strings.HasSuffix(path, "/actions") {
						actionReads.Add(1)
					}
					if strings.HasSuffix(path, "/policies/policy-1") {
						policyReads.Add(1)
					}
					for _, edge := range privateGuardReads() {
						if edge.name == dependency && path == edge.path {
							w.WriteHeader(404)
							_, _ = w.Write([]byte(`{"code":"password-canary","message":"arbitrary-server-text"}`))
							return
						}
					}
					body := privateGuardBody(path)
					if strings.HasSuffix(path, "/user-1/policies") {
						body = `{"data":[{"id":"policy-1"}],"totalItems":1}`
					}
					if strings.HasSuffix(path, "/user-1/groups") {
						body = `[{"id":"group-1","policies":["policy-1"]}]`
					}
					_, _ = w.Write([]byte(body))
				}))
				t.Cleanup(server.Close)
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
				for range 2 {
					err := exercisePrivateUserGuard(context.Background(), c, "user-1", userReadOptions{Redact: []string{"password-canary"}})
					if dependency == "" && err != nil || dependency != "" && !errors.Is(err, vngcloud.ErrNotFound) {
						t.Fatalf("guard result: %v", err)
					}
					assertUserErrorPrivate(t, err, "password-canary", "arbitrary-server-text")
				}
				wantActions := int32(1)
				if dependency == "target" && !warm {
					wantActions = 0
				}
				if callerReads.Load() != 1 || actionReads.Load() != wantActions || captures.Load() != 0 {
					t.Fatalf("caller=%d actions=%d captures=%d", callerReads.Load(), actionReads.Load(), captures.Load())
				}
				if dependency == "" && policyReads.Load() != 2 {
					t.Fatal("policy map must deduplicate within each call and refetch between calls")
				}
			})
		}
	}
}

func TestPrivateUserGuardPrecedence(t *testing.T) {
	for _, tc := range []struct {
		caller, target string
		want           error
		reads          int
	}{
		{`{"userId":"USER-1","userType":"iam-user"}`, ``, ErrSelfChange, 1},
		{`{"userId":"caller","userType":"root-user"}`, ``, ErrPrivilegedChange, 1},
		{`{"userId":"caller","userType":"unknown"}`, ``, ErrPrivilegedChange, 1},
		{`{"userId":"caller","userType":"iam-user"}`, `{"id":"user-1","from":"idp"}`, ErrOriginNotIAM, 2},
	} {
		reads := 0
		c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			reads++
			body := tc.target
			if strings.HasSuffix(r.URL.Path, "/auth/userinfo") {
				body = tc.caller
			}
			_, _ = w.Write([]byte(body))
		}))
		err := exercisePrivateUserGuard(context.Background(), c, "user-1", userReadOptions{})
		if !errors.Is(err, tc.want) || reads != tc.reads {
			t.Fatalf("reads=%d err=%v", reads, err)
		}
	}
}

func TestPrivateGuardDerivedActionCredentials(t *testing.T) {
	for _, tc := range []struct {
		name, action, password, token string
		accumulate                    bool
	}{
		{name: "password-case", action: "PASSWORD-CANARY", password: "password-canary"},
		{name: "password-prefix", action: "CreatePolicy", password: "IAM:CREATEPOLICY"},
		{name: "sent-case", action: "BEARER-CANARY"},
		{name: "sent-prefix", action: "CreatePolicy", token: "IAM:CREATEPOLICY"},
		{name: "accumulated-prefix", action: "CreatePolicy", token: "IAM:CREATEPOLICY", accumulate: true},
		{name: "safe-action", action: "canary"},
		{name: "accumulated-case", action: "BEARER-CANARY", accumulate: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var captures atomic.Int32
			var logs lockedGuardLog
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = fmt.Fprintf(w, `[{"action":%q,"label":"Write"}]`, tc.action)
			}))
			t.Cleanup(server.Close)
			c := privateGuardClient(server, &logs, &captures)
			if tc.token != "" {
				cfg, err := vngcloud.NewConfig(vngcloud.WithRegion("hcm-3"), vngcloud.WithStaticToken(tc.token),
					vngcloud.WithHTTPClient(server.Client()), vngcloud.WithRetry(0, 0),
					vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{IAM: server.URL + "/"}))
				if err != nil {
					t.Fatal(err)
				}
				c = New(cfg)
			}
			opts := userReadOptions{Redact: []string{tc.password}}
			var sent []string
			if tc.accumulate {
				opts.SentCredentials = &sent
			}
			names, err := c.guardWriteActionNames(context.Background(), opts)
			if tc.name == "safe-action" {
				// This action contains no credential before or after qualification.
				if err != nil || len(names) != 1 {
					t.Fatalf("safe action refused: %v", err)
				}
				return
			}
			if err == nil || names != nil || len(c.guardWriteActions) != 0 {
				t.Fatal("credential-bearing action names returned or cached")
			}
			assertUserErrorPrivate(t, err, "bearer-canary", "BEARER-CANARY")
			for _, secret := range []string{tc.password, tc.token} {
				if secret != "" {
					assertUserErrorPrivate(t, err, secret)
				}
			}
			if tc.accumulate && len(sent) == 0 {
				t.Fatal("sent credential not accumulated")
			}
		})
	}
}

func TestPrivateGuardWarmCacheCollisions(t *testing.T) {
	for _, cache := range []string{"caller", "actions"} {
		for _, source := range []string{"password", "credential"} {
			t.Run(cache+"/"+source, func(t *testing.T) {
				var reads atomic.Int32
				c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					reads.Add(1)
					body := `[{"action":"LaterOperationCanary","label":"Write"}]`
					if strings.HasSuffix(r.URL.Path, "/userinfo") {
						body = `{"userId":"caller","userType":"iam-user","username":"LaterOperationCanary"}`
					}
					_, _ = w.Write([]byte(body))
				}))
				read := func(opts userReadOptions) error {
					if cache == "caller" {
						_, err := c.guardCallerIdentity(context.Background(), opts)
						return err
					}
					_, err := c.guardWriteActionNames(context.Background(), opts)
					return err
				}
				benign := userReadOptions{Redact: []string{"earlier-operation-canary"}}
				if err := read(benign); err != nil {
					t.Fatal(err)
				}
				opts := userReadOptions{Redact: []string{"LATEROPERATIONCANARY"}}
				if source == "credential" {
					sent := []string{"LATEROPERATIONCANARY"}
					opts = userReadOptions{SentCredentials: &sent}
				}
				err := read(opts)
				if err == nil {
					t.Fatal("cache hit ignored current operation credentials")
				}
				assertUserErrorPrivate(t, err, "LATEROPERATIONCANARY", "LaterOperationCanary", "lateroperationcanary")
				if err := read(benign); err != nil {
					t.Fatal("collision changed the shared cache")
				}
				if reads.Load() != 1 {
					t.Fatal("cache collision caused a refetch")
				}
			})
		}
	}
}
