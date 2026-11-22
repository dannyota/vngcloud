package cli

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"danny.vn/vngcloud/iam"
)

func TestIAMMissingCreateIDRetainsSecretWithoutCleanupAuthority(t *testing.T) {
	for _, unwritable := range []bool{false, true} {
		label := "saved"
		dir := t.TempDir()
		if unwritable {
			label = "save-failed"
			dir = unwritableSecretFileDir(t)
		}
		t.Run(label, func(t *testing.T) {
			path := filepath.Join(dir, "secret")
			fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
				"/accounts-api/v1/service-accounts": func(w http.ResponseWriter, r *http.Request) {
					if r.Method != http.MethodPost {
						t.Error("only one create is authorized")
					}
					w.WriteHeader(201)
					_, _ = w.Write([]byte(`{"clientSecret":"issued-once"}`))
				},
			})
			root, stdout, stderr := newSvcRoot(t, fixture)
			root.SetArgs([]string{"--region", "hcm-3", "iam", "create-service-account", "--name", "app", "--secret-file", path})
			err := root.ExecuteContext(context.Background())
			want := "RequestFailed"
			if unwritable {
				want = "SecretFileFailed"
			}
			if err == nil || classify(err).Code != want || exitCode(err) != 1 {
				t.Fatalf("uncertain create classification: %v", err)
			}
			if fixture.requestCount() != 1 || !strings.Contains(err.Error(), "ownership") || strings.Contains(err.Error(), "was created") {
				t.Fatalf("missing ID gained cleanup authority: requests=%d error=%v", fixture.requestCount(), err)
			}
			if strings.Contains(stdout.String()+stderr.String()+err.Error(), "issued-once") {
				t.Fatal("one-time secret escaped")
			}
			if !unwritable {
				data, readErr := os.ReadFile(path)
				if readErr != nil || string(data) != "issued-once\n" {
					t.Fatal("one-time secret was lost")
				}
			}
		})
	}
}

func TestIAMUpdateUnsettledOutputWinsOverConfirmation404(t *testing.T) {
	for _, format := range []string{"json", "table", "text"} {
		t.Run(format, func(t *testing.T) {
			fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
				"/accounts-api/v1/auth/userinfo":                                   jsonHandler(200, iamUserInfoJSON("user-1", "iam-user")),
				"/policies-api/v1/actions":                                         jsonHandler(200, iamWriteActionsJSON),
				"/policies-api/v1/user-attachments/service-accounts/sa-1/policies": jsonHandler(200, iamNoPoliciesJSON),
				"/accounts-api/v1/service-accounts/sa-1": func(w http.ResponseWriter, r *http.Request) {
					if r.Method == http.MethodPatch {
						w.WriteHeader(200)
						return
					}
					w.WriteHeader(404)
				},
			})
			root, stdout, _ := newSvcRoot(t, fixture)
			root.SetArgs([]string{"--region", "hcm-3", "--output", format, "--query", "ServiceAccount.Name", "iam", "update-service-account", "--service-account-id", "sa-1", "--description", "new"})
			err := root.ExecuteContext(context.Background())
			if !errors.Is(err, iam.ErrNotSettled) || classify(err).Code != "NotSettled" || exitCode(err) != 1 || !strings.Contains(stdout.String(), "sa-1") {
				t.Fatalf("settlement or partial output lost: output=%s error=%v", stdout.String(), err)
			}
		})
	}
}

func TestIAMCleanupUsesAttributableCreateIDAfterSwappedConfirmation(t *testing.T) {
	path := filepath.Join(unwritableSecretFileDir(t), "secret")
	deleted := false
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/accounts-api/v1/service-accounts": jsonHandler(201, `{"id":"sa-owned","clientSecret":"issued-once"}`),
		"/accounts-api/v1/service-accounts/sa-owned": func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodGet {
				_, _ = w.Write([]byte(serviceAccountJSON("sa-other", "client-other")))
				return
			}
			if r.Method != http.MethodDelete {
				t.Error("unexpected cleanup method")
			}
			deleted = true
			w.WriteHeader(204)
		},
		"/accounts-api/v1/service-accounts/sa-other":                           func(_ http.ResponseWriter, _ *http.Request) { t.Error("substituted identity became a cleanup target") },
		"/accounts-api/v1/auth/userinfo":                                       jsonHandler(200, iamUserInfoJSON("user-1", "iam-user")),
		"/policies-api/v1/actions":                                             jsonHandler(200, iamWriteActionsJSON),
		"/policies-api/v1/user-attachments/service-accounts/sa-owned/policies": jsonHandler(200, iamNoPoliciesJSON),
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "iam", "create-service-account", "--name", "app", "--secret-file", path})
	err := root.ExecuteContext(context.Background())
	if err == nil || classify(err).Code != "SecretFileFailed" || !deleted {
		t.Fatalf("attributable cleanup failed: error=%v deleted=%v", err, deleted)
	}
	if strings.Contains(stdout.String()+stderr.String()+err.Error(), "issued-once") {
		t.Fatal("issued secret escaped cleanup")
	}
}
