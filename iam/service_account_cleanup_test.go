package iam

import (
	"bytes"
	"context"
	"encoding/json"
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

type serviceAccountCleanupTokens struct{ stage atomic.Int32 }

func (s *serviceAccountCleanupTokens) Token(context.Context) (transport.Token, error) {
	values := [...]string{"create-bearer-canary", "confirm-bearer-canary", "cleanup-bearer-canary"}
	return transport.Token{AccessToken: values[min(s.stage.Load(), 2)]}, nil
}

func (s *serviceAccountCleanupTokens) Invalidate(string) { s.stage.Add(1) }

func TestServiceAccountCleanupPrivacyAcrossRefresh(t *testing.T) {
	for _, reflected := range []string{"issued-cleanup-canary", "create-bearer-canary", "confirm-bearer-canary", "cleanup-bearer-canary", "safe-policy"} {
		t.Run(reflected, func(t *testing.T) {
			const issued = "issued-cleanup-canary"
			var policyReads, deletes, captures atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodPost:
					w.WriteHeader(http.StatusCreated)
					_, _ = io.WriteString(w, `{"id":"sa-owned","clientSecret":"`+issued+`"}`)
				case r.Method == http.MethodDelete:
					deletes.Add(1)
					w.WriteHeader(http.StatusNoContent)
				case strings.HasSuffix(r.URL.Path, "/service-accounts/sa-owned"):
					if r.Header.Get("Authorization") == "Bearer create-bearer-canary" {
						w.WriteHeader(http.StatusUnauthorized)
						return
					}
					_, _ = io.WriteString(w, `{"id":"sa-owned","clientId":"client-owned","name":"app"}`)
				case strings.HasSuffix(r.URL.Path, "/auth/userinfo"):
					if r.Header.Get("Authorization") == "Bearer confirm-bearer-canary" {
						w.WriteHeader(http.StatusUnauthorized)
						return
					}
					_, _ = io.WriteString(w, `{"userId":"user-1","userType":"iam-user"}`)
				case strings.HasSuffix(r.URL.Path, "/actions"):
					_, _ = io.WriteString(w, `[{"action":"CreatePolicy","label":"Write","resources":["*"]}]`)
				case strings.Contains(r.URL.Path, "/user-attachments/"):
					_, _ = fmt.Fprintf(w, `{"data":[{"id":%q,"name":"read","createdAt":1700000000000}],"totalItems":1,"totalPages":1}`, "policy-"+reflected)
				case strings.Contains(r.URL.Path, "/policies/"):
					policyReads.Add(1)
					w.WriteHeader(http.StatusForbidden)
					_, _ = io.WriteString(w, `{"code":"`+issued+`","message":"create-bearer-canary confirm-bearer-canary cleanup-bearer-canary `+issued+`"}`)
				default:
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
					w.WriteHeader(http.StatusBadRequest)
				}
			}))
			defer server.Close()
			var logs bytes.Buffer
			cfg := core.NewTestConfig("hcm-3", "", endpoints.Set{Dashboard: server.URL + "/", IAM: server.URL + "/"}, transport.New(transport.Config{
				HTTPClient: server.Client(), TokenSource: new(serviceAccountCleanupTokens), RetryCount: 3,
				Logger:  slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
				Capture: func(transport.Capture) { captures.Add(1) },
			}))
			c := New(cfg)
			out, err := c.CreateServiceAccount(context.Background(), &CreateServiceAccountInput{Name: "app"})
			if err != nil || out == nil {
				t.Fatalf("create error=%v", err)
			}
			bridge, ok := any(out).(interface {
				CleanupInput() *DeleteServiceAccountInput
			})
			if !ok {
				t.Fatal("create output cannot pass operation privacy to cleanup")
			}
			in := bridge.CleanupInput()
			if in.ServiceAccountID != "sa-owned" {
				t.Fatal("cleanup lost attributable identity")
			}
			for _, value := range []any{out, *out, in, *in} {
				for _, format := range []string{"%v", "%+v", "%#v"} {
					assertServiceAccountCleanupPrivate(t, fmt.Sprintf(format, value))
				}
				raw, marshalErr := json.Marshal(value)
				if marshalErr != nil {
					t.Fatal(marshalErr)
				}
				assertServiceAccountCleanupPrivate(t, string(raw))
			}
			for range 2 {
				_, err = c.DeleteServiceAccount(context.Background(), bridge.CleanupInput())
				if err == nil {
					t.Fatal("unsafe cleanup succeeded")
				}
				assertServiceAccountCleanupPrivate(t, fmt.Sprintf("%+v", err))
			}
			wantReads := int32(0)
			if reflected == "safe-policy" {
				wantReads = 2
			}
			if policyReads.Load() != wantReads || deletes.Load() != 0 || captures.Load() != 0 {
				t.Errorf("policy reads=%d want=%d deletes=%d captures=%d", policyReads.Load(), wantReads, deletes.Load(), captures.Load())
			}
			assertServiceAccountCleanupPrivate(t, logs.String())
			in.ServiceAccountID = issued + "/invalid"
			_, err = c.DeleteServiceAccount(context.Background(), in)
			if err == nil {
				t.Fatal("credential-bearing cleanup operand was accepted")
			}
			assertServiceAccountCleanupPrivate(t, fmt.Sprintf("%+v", err))
		})
	}
}

func assertServiceAccountCleanupPrivate(t *testing.T, text string) {
	t.Helper()
	for _, value := range []string{"issued-cleanup-canary", "create-bearer-canary", "confirm-bearer-canary", "cleanup-bearer-canary"} {
		if strings.Contains(text, value) {
			t.Fatal("operation credential escaped cleanup privacy")
		}
	}
}
