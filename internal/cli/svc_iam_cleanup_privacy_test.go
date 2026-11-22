package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/iam"
)

func TestIAMCleanupWithholdsIssuedSecretThroughGuard(t *testing.T) {
	for _, confirmed := range []bool{true, false} {
		t.Run(fmt.Sprintf("confirmed=%t", confirmed), func(t *testing.T) {
			const secret = "issued-cleanup-canary"
			var unsafeRequests atomic.Int32
			fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
				"/accounts-api/v1/service-accounts": jsonHandler(201, `{"id":"sa-owned","clientSecret":"`+secret+`"}`),
				"/accounts-api/v1/service-accounts/sa-owned": func(w http.ResponseWriter, r *http.Request) {
					if r.Method != http.MethodGet {
						t.Error("unsafe cleanup mutation")
					}
					if !confirmed {
						jsonHandler(403, `{"code":"`+secret+`","message":"`+secret+`"}`)(w, r)
						return
					}
					jsonHandler(200, serviceAccountJSON("sa-owned", "client-owned"))(w, r)
				},
				"/accounts-api/v1/auth/userinfo": jsonHandler(200, iamUserInfoJSON("user-1", "iam-user")),
				"/policies-api/v1/actions":       jsonHandler(200, iamWriteActionsJSON),
				"/policies-api/v1/user-attachments/service-accounts/sa-owned/policies": jsonHandler(200,
					`{"data":[{"id":"policy-`+secret+`","name":"read","createdAt":1700000000000}],"totalItems":1,"totalPages":1}`),
				"/policies-api/v1/policies/policy-" + secret: func(w http.ResponseWriter, r *http.Request) {
					unsafeRequests.Add(1)
					jsonHandler(403, `{"code":"`+secret+`","message":"`+secret+`"}`)(w, r)
				},
			})
			root, stdout, stderr := newSvcRoot(t, fixture)
			path := filepath.Join(unwritableSecretFileDir(t), "secret")
			root.SetArgs([]string{"--region", "hcm-3", "--debug", "iam", "create-service-account", "--name", "app", "--secret-file", path})
			err := root.ExecuteContext(context.Background())
			if err == nil || classify(err).Code != "SecretFileFailed" {
				t.Fatalf("error=%v, want SecretFileFailed", err)
			}
			if unsafeRequests.Load() != 0 {
				t.Errorf("credential-bearing policy requests=%d, want 0", unsafeRequests.Load())
			}
			if strings.Contains(stdout.String()+stderr.String()+fmt.Sprintf("%+v", err), secret) {
				t.Error("issued secret escaped through cleanup diagnostics")
			}
			if !strings.Contains(stderr.String(), "msg=request") {
				t.Error("debug logger was not exercised")
			}
		})
	}
}

type iamCleanupRefreshProvider struct{ refreshed atomic.Bool }

func (p *iamCleanupRefreshProvider) Token(context.Context) (vngcloud.Token, error) {
	value := "cleanup-initial-canary"
	if p.refreshed.Load() {
		value = "cleanup-later-canary"
	}
	return vngcloud.Token{AccessToken: value}, nil
}

func (p *iamCleanupRefreshProvider) Invalidate(string) { p.refreshed.Store(true) }

func TestIAMCleanupDiagnosticsOmitUntrustedIdentity(t *testing.T) {
	for _, tc := range []struct {
		name        string
		id          string
		status      int
		wantDeletes int32
	}{
		{"new cleanup credential", "cleanup-later-canary", http.StatusForbidden, 0},
		{"deletion failed", "sa-owned", http.StatusForbidden, 1},
		{"deletion succeeded", "sa-owned", http.StatusNoContent, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider := new(iamCleanupRefreshProvider)
			var unsafeRequests, deletes atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if provider.refreshed.Load() && strings.Contains(r.URL.Path, "cleanup-later-canary") {
					unsafeRequests.Add(1)
				}
				switch {
				case r.Method == http.MethodPost:
					w.WriteHeader(http.StatusCreated)
					_, _ = fmt.Fprintf(w, `{"id":%q,"clientSecret":"issued-cleanup-canary"}`, tc.id)
				case r.Method == http.MethodDelete:
					deletes.Add(1)
					w.WriteHeader(tc.status)
				case strings.HasSuffix(r.URL.Path, "/service-accounts/"+tc.id):
					_, _ = io.WriteString(w, serviceAccountJSON(tc.id, "client-owned"))
				case strings.HasSuffix(r.URL.Path, "/auth/userinfo"):
					if !provider.refreshed.Load() {
						w.WriteHeader(http.StatusUnauthorized)
						return
					}
					_, _ = io.WriteString(w, iamUserInfoJSON("user-1", "iam-user"))
				case strings.HasSuffix(r.URL.Path, "/actions"):
					_, _ = io.WriteString(w, iamWriteActionsJSON)
				case strings.Contains(r.URL.Path, "/user-attachments/"):
					_, _ = io.WriteString(w, iamNoPoliciesJSON)
				default:
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
					w.WriteHeader(http.StatusBadRequest)
				}
			}))
			defer server.Close()
			var logs bytes.Buffer
			cfg, err := vngcloud.NewConfig(vngcloud.WithRegion("hcm-3"), vngcloud.WithCredentialsProvider(provider),
				vngcloud.WithHTTPClient(server.Client()), vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{Dashboard: server.URL + "/", IAM: server.URL + "/"}),
				vngcloud.WithLogger(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))))
			if err != nil {
				t.Fatal(err)
			}
			client := iam.New(cfg)
			out, err := client.CreateServiceAccount(context.Background(), &iam.CreateServiceAccountInput{Name: "app"})
			if err != nil {
				t.Fatalf("create failed: %v", err)
			}
			logs.Reset()
			err = writeServiceAccountSecretFile(client, context.Background(), out, filepath.Join(t.TempDir(), "missing", "secret"))
			if err == nil || classify(err).Code != "SecretFileFailed" {
				t.Fatalf("error=%v, want SecretFileFailed", err)
			}
			if strings.Contains(err.Error(), tc.id) {
				t.Error("cleanup diagnostic retained an untrusted identity")
			}
			for _, secret := range []string{"issued-cleanup-canary", "cleanup-initial-canary", "cleanup-later-canary"} {
				if strings.Contains(fmt.Sprintf("%+v", err)+logs.String(), secret) {
					t.Error("cleanup diagnostic disclosed an operation credential")
				}
			}
			if unsafeRequests.Load() != 0 || deletes.Load() != tc.wantDeletes {
				t.Errorf("unsafe requests=%d, deletes=%d want=%d", unsafeRequests.Load(), deletes.Load(), tc.wantDeletes)
			}
			if tc.status != http.StatusNoContent && (!strings.Contains(err.Error(), "list-service-accounts") || !strings.Contains(err.Error(), "ownership")) {
				t.Error("cleanup failure omitted ownership reconciliation guidance")
			}
			if tc.status == http.StatusNoContent && !strings.Contains(err.Error(), "was deleted") {
				t.Error("successful cleanup was not reported")
			}
		})
	}
}
