package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestContainerRegistrySecretFileFailureWithholdsIssuedName(t *testing.T) {
	path := filepath.Join(unwritableSecretFileDir(t), "secret")
	created := false
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v1/user/permissions": jsonHandler(200, listPermissionsJSON()),
		"/v1/user": func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPost {
				created = true
				_, _ = w.Write([]byte(`{"secretKey":"app-ci"}`))
				return
			}
			if created {
				t.Error("issued secret was reused as a lookup name")
			}
			_, _ = w.Write([]byte(userListJSON()))
		},
		"/v1/user/ra-1": func(_ http.ResponseWriter, _ *http.Request) { t.Error("candidate cleanup must not run") },
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs(append([]string{"--debug"}, containerRegistryCreateUserArgs(path)...))
	err := root.ExecuteContext(context.Background())
	if err == nil || classify(err).Code != "SecretFileFailed" || exitCode(err) != 1 {
		t.Fatalf("file failure classification: %v", err)
	}
	if strings.Contains(fmt.Sprint(err)+stdout.String()+stderr.String(), "app-ci") {
		t.Fatal("issued secret escaped through name-based recovery")
	}
	if !strings.Contains(err.Error(), "reconcile") || strings.Contains(err.Error(), "must be deleted") {
		t.Fatalf("unsafe cleanup guidance: %v", err)
	}
}

func TestContainerRegistrySecretPathCannotExposeIssuedSecret(t *testing.T) {
	path := filepath.Join(t.TempDir(), containerRegistryTestSecret)
	fixture, _ := containerRegistryUserFixture(t, []string{"app-ci"}, nil)
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs(containerRegistryCreateUserArgs(path))
	err := root.ExecuteContext(context.Background())
	if err == nil || classify(err).Code != "UserNotFound" || exitCode(err) != 1 {
		t.Fatalf("uncertain create classification: %v", err)
	}
	data, readErr := os.ReadFile(path)
	if readErr != nil || string(data) != containerRegistryTestSecret+"\n" {
		t.Fatal("issued secret was not preserved")
	}
	if strings.Contains(stdout.String()+stderr.String()+fmt.Sprint(err), containerRegistryTestSecret) {
		t.Fatal("issued secret escaped through the secret-file path")
	}
	var out struct{ SecretFile string }
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil || !strings.Contains(out.SecretFile, "[redacted]") {
		t.Fatal("safe secret-file location was not returned")
	}
}

func TestContainerRegistryIncompleteListDoesNotPrintCompleteInventory(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v1/repository": jsonHandler(200, `{"data":[{"uuid":"repo-1"}],"page":1,"pageSize":1,"totalPage":2,"totalItem":2}`),
	})
	root, stdout, _ := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "containerregistry", "list-repositories"})
	err := root.ExecuteContext(context.Background())
	if err == nil || classify(err).Code != "RequestFailed" || exitCode(err) != 1 || stdout.Len() != 0 || !strings.Contains(err.Error(), "incomplete") {
		t.Fatalf("incomplete read looked complete: output=%s error=%v", stdout.String(), err)
	}
}
