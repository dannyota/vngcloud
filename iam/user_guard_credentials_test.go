package iam

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/endpoints"
	"danny.vn/vngcloud/internal/transport"
)

type guardOperandTokens struct {
	changed atomic.Bool
	later   string
}

func (s *guardOperandTokens) Token(context.Context) (transport.Token, error) {
	value := "initial-bearer"
	if s.changed.Load() {
		value = s.later
	}
	return transport.Token{AccessToken: value}, nil
}

func (*guardOperandTokens) Invalidate(string) {}

type guardOperandLog struct {
	collected *atomic.Bool
	all       lockedGuardLog
	after     lockedGuardLog
}

func (l *guardOperandLog) Write(p []byte) (int, error) {
	if l.collected.Load() {
		_, _ = l.after.Write(p)
	}
	return l.all.Write(p)
}

func guardOperandClient(server *httptest.Server, tokens *guardOperandTokens, logs io.Writer, captures *atomic.Int32) *Client {
	cfg := core.NewTestConfig("hcm-3", "", endpoints.Set{Dashboard: server.URL + "/", IAM: server.URL + "/"}, transport.New(transport.Config{
		HTTPClient: server.Client(), TokenSource: tokens,
		Logger:  slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
		Capture: func(transport.Capture) { captures.Add(1) },
	}))
	return New(cfg)
}

func callGuardOperandOperation(ctx context.Context, c *Client, operation string) (any, error) {
	switch operation {
	case "reset":
		out, err := c.ResetUserPassword(ctx, &ResetUserPasswordInput{UserID: "user-1", Password: "synthetic-password"})
		if out == nil {
			return nil, err
		}
		return out, err
	default:
		out, err := callMFA(ctx, c, strings.HasPrefix(operation, "enable"), "user-1", "google")
		if out == nil {
			return nil, err
		}
		return out, err
	}
}

func TestUserGuardRetainedCredentialOperands(t *testing.T) {
	const (
		target = "/accounts-api/v1/iam-users/user-1"
		page   = "/policies-api/v1/user-attachments/iam-users/user-1/policies"
		groups = "/policies-api/v1/user-attachments/iam-users/user-1/groups"
		policy = "/policies-api/v1/policies/policy-1"
	)
	for _, operation := range []string{"reset", "enable", "disable", "enable-noop", "disable-noop"} {
		for _, tc := range []struct {
			name, credential, changeAfter string
			pageBody, groupsBody          string
			reads                         int32
		}{
			{"initial-target", "user-1", "", `{"data":[],"totalItems":0}`, `[]`, 1},
			{"direct-policy", "future-bearer", page, `{"data":[{"id":"policy-1"},{"id":"future-bearer"}],"totalItems":2}`, `[]`, 5},
			{"group-policy", "future-bearer", groups, `{"data":[],"totalItems":0}`, `[{"id":"first","policies":["policy-1"]},{"id":"second","policies":["future-bearer"]}]`, 6},
			{"target-policies", "user-1", target, `{"data":[],"totalItems":0}`, `[]`, 3},
			{"target-groups", "user-1", page, `{"data":[{"id":"policy-1"}],"totalItems":1}`, `[]`, 5},
			{"mutation-input-case", "user-1", policy, `{"data":[{"id":"policy-1"}],"totalItems":1}`, `[]`, 6},
		} {
			t.Run(operation+"/"+tc.name, func(t *testing.T) {
				tokens := &guardOperandTokens{later: tc.credential}
				tokens.changed.Store(tc.changeAfter == "")
				var reads, writes, unsafe, captures atomic.Int32
				var collected atomic.Bool
				logs := guardOperandLog{collected: &collected}
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if collected.Load() && strings.Contains(r.URL.Path, tc.credential) {
						unsafe.Add(1)
					}
					if r.Header.Get("Authorization") == "Bearer "+tc.credential {
						collected.Store(true)
					}
					if r.Method != http.MethodGet {
						writes.Add(1)
						w.WriteHeader(http.StatusNoContent)
						return
					}
					reads.Add(1)
					body := resetGuardBody(r.URL.Path)
					switch r.URL.Path {
					case target:
						enabled := strings.HasPrefix(operation, "disable")
						if strings.HasSuffix(operation, "-noop") {
							enabled = !enabled
						}
						body = mfaUser(true, enabled)
						if tc.name == "mutation-input-case" {
							body = strings.Replace(body, `"id":"user-1"`, `"id":"USER-1"`, 1)
						}
					case page:
						body = tc.pageBody
					case groups:
						body = tc.groupsBody
					case policy, "/policies-api/v1/policies/future-bearer":
						body = `{"statements":[{"effect":"deny","actions":["iam:CreatePolicy"]}]}`
					}
					if r.URL.Path == tc.changeAfter {
						tokens.changed.Store(true)
					}
					_, _ = io.WriteString(w, body)
				}))
				defer server.Close()
				out, err := callGuardOperandOperation(context.Background(), guardOperandClient(server, tokens, &logs, &captures), operation)
				if unsafe.Load() != 0 || writes.Load() != 0 || reads.Load() != tc.reads || err == nil || out != nil {
					t.Fatalf("guard refusal: unsafe=%d writes=%d reads=%d partial=%t error=%v", unsafe.Load(), writes.Load(), reads.Load(), out != nil, err)
				}
				if !collected.Load() || captures.Load() != 0 || errors.Is(err, ErrUserWriteUncertain) || errors.Is(err, ErrNotSettled) {
					t.Fatal("refusal lost privacy or became a write outcome")
				}
				assertUserErrorPrivate(t, err, tc.credential, "initial-bearer", "synthetic-password")
				// Earlier target URLs can name an ID before a later token takes
				// that value. Every log after collection must withhold it.
				credentialLogs := logs.after.String()
				if tc.credential != "user-1" || tc.changeAfter == "" {
					credentialLogs = logs.all.String()
				}
				if strings.Contains(credentialLogs, tc.credential) || strings.Contains(logs.all.String(), "initial-bearer") {
					t.Fatal("guard URL operand leaked in diagnostics")
				}
				raw, marshalErr := json.Marshal(out)
				if marshalErr != nil || strings.Contains(string(raw), tc.credential) || strings.Contains(fmt.Sprintf("%+v", out), tc.credential) {
					t.Fatal("guard URL operand leaked in output")
				}
			})
		}
	}
}

func TestUserGuardPrivateCredentialOperands(t *testing.T) {
	for _, tc := range []struct {
		name string
		call func(context.Context, *Client, userReadOptions) error
	}{
		{"target", func(ctx context.Context, c *Client, opts userReadOptions) error {
			_, err := c.getUser(ctx, "iam.ResetUserPassword", "future-bearer", opts)
			return err
		}},
		{"confirmation", func(ctx context.Context, c *Client, opts userReadOptions) error {
			_, err := c.confirmUser(ctx, "future-bearer", "ordinary", opts)
			return err
		}},
		{"policy", func(ctx context.Context, c *Client, opts userReadOptions) error {
			_, err := c.getPolicy(ctx, &GetPolicyInput{PolicyID: "future-bearer"}, opts)
			return err
		}},
		{"page", func(ctx context.Context, c *Client, opts userReadOptions) error {
			_, err := c.guardPagedPolicySummaries(ctx, "iam.ResetUserPassword", []string{"user-attachments", "iam-users", "future-bearer", "policies"}, opts)
			return err
		}},
		{"array", func(ctx context.Context, c *Client, opts userReadOptions) error {
			_, err := guardFetchAttachmentArray[string](ctx, c, "iam.ResetUserPassword", []string{"policies", "future-bearer", "iam-users"}, opts)
			return err
		}},
		{"group", func(ctx context.Context, c *Client, opts userReadOptions) error {
			_, _, err := c.guardGetGroupAttachments(ctx, "iam.ResetUserPassword", "future-bearer", opts)
			return err
		}},
		{"cached-policy", func(ctx context.Context, c *Client, opts userReadOptions) error {
			_, err := c.anyPolicyIDPrivileged(ctx, []string{"future-bearer"}, []string{"iam:createpolicy"}, map[string]*Policy{
				"future-bearer": {ID: "ordinary", Statements: []Statement{{Effect: "deny", Actions: []string{"iam:CreatePolicy"}}}},
			}, opts)
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requests, captures atomic.Int32
			var logs lockedGuardLog
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests.Add(1)
				_, _ = io.WriteString(w, `{"id":"ordinary","mode":"iam","policies":[],"iamUsers":[],"data":[],"totalItems":0}`)
			}))
			defer server.Close()
			c := guardOperandClient(server, &guardOperandTokens{}, &logs, &captures)
			credentials := []string{"future-bearer"}
			err := tc.call(context.Background(), c, userReadOptions{SentCredentials: &credentials})
			if err == nil || requests.Load() != 0 || captures.Load() != 0 {
				t.Fatalf("private URL operand was used: requests=%d error=%v", requests.Load(), err)
			}
			assertUserErrorPrivate(t, err, "future-bearer")
			if logs.String() != "" {
				t.Fatal("refused operand reached the transport logger")
			}
		})
	}
}

func TestUserGuardOperandOptionsStayLocal(t *testing.T) {
	var requests, captures atomic.Int32
	var logs lockedGuardLog
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = io.WriteString(w, `{"id":"future-bearer"}`)
	}))
	defer server.Close()
	c := guardOperandClient(server, &guardOperandTokens{}, &logs, &captures)
	opts := userReadOptions{Redact: []string{"future-bearer"}}
	if out, err := c.getPolicy(context.Background(), &GetPolicyInput{PolicyID: "future-bearer"}, opts); out != nil || err == nil || requests.Load() != 0 {
		t.Fatal("private policy operand was not refused")
	}
	if out, err := c.GetPolicy(context.Background(), &GetPolicyInput{PolicyID: "future-bearer"}); err != nil || out == nil || out.Policy.ID != "future-bearer" || requests.Load() != 1 || captures.Load() != 1 {
		t.Fatalf("private options changed an ordinary policy read: %v", err)
	}
	if out, err := c.GetUser(context.Background(), &GetUserInput{UserID: "future-bearer"}); err != nil || out == nil || out.User.ID != "future-bearer" || requests.Load() != 2 || captures.Load() != 1 {
		t.Fatalf("private options changed an ordinary user read: %v", err)
	}
}
