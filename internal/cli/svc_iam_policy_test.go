package cli

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// iamCustomerPolicyJSON builds one customer (manager "user") Policy body
// named "app-read" with a single read-only vserver statement, the shape
// every policy-write golden test below uses for a GetPolicy read that must
// pass the not-managed and not-privileged checks in iam/policy_guard.go.
func iamCustomerPolicyJSON(id string) string {
	return `{"id":"` + id + `","name":"app-read","description":"d","manager":"user","scope":"account",` +
		`"root":"123456","statements":[{"effect":"allow","actions":["vserver:ListServers"],"resources":["*"]}],` +
		`"createdAt":1700000000000}`
}

// iamNoAttachmentsRoutes returns the three bare-array attachment list routes
// ListPolicyAttachments makes for policyID, each empty: the shape
// policyAttachedToProtected (iam/policy_guard.go) needs to report the policy
// unattached without reading any group, user, or service account.
func iamNoAttachmentsRoutes(policyID string) map[string]func(http.ResponseWriter, *http.Request) {
	base := "/policies-api/v1/policies/" + policyID
	return map[string]func(http.ResponseWriter, *http.Request){
		base + "/groups":           jsonHandler(http.StatusOK, `[]`),
		base + "/iam-users":        jsonHandler(http.StatusOK, `[]`),
		base + "/service-accounts": jsonHandler(http.StatusOK, `[]`),
	}
}

// writeDocumentFile writes content to a fresh policy.json file under
// t.TempDir() and returns its path, for a --document-file value.
func writeDocumentFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "policy.json")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return path
}

// mergeRoutes combines any number of route maps into one, for tests that
// build a fixture from iamNoAttachmentsRoutes plus their own operation-
// specific routes.
func mergeRoutes(maps ...map[string]func(http.ResponseWriter, *http.Request)) map[string]func(http.ResponseWriter, *http.Request) {
	merged := map[string]func(http.ResponseWriter, *http.Request){}
	for _, m := range maps {
		for k, v := range m {
			merged[k] = v
		}
	}
	return merged
}

// TestIAMCreatePolicyGolden drives create-policy end to end with a
// --document-file in the console's own lower-case form: the guard's own
// caller and action-list reads pass, the POST body carries the decoded
// statements, and the confirm read's own policy prints.
func TestIAMCreatePolicyGolden(t *testing.T) {
	doc := writeDocumentFile(t,
		`{"statements": [{"effect": "allow", "actions": ["vserver:ListServers"], "resources": ["*"]}]}`)

	var postBody []byte
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/accounts-api/v1/auth/userinfo": jsonHandler(http.StatusOK, iamUserInfoJSON("user-1", "iam-user")),
		"/policies-api/v1/actions":       jsonHandler(http.StatusOK, iamWriteActionsJSON),
		"/policies-api/v1/policies": func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				t.Fatalf("method = %s, want POST", r.Method)
			}
			defer func() { _ = r.Body.Close() }()
			postBody, _ = io.ReadAll(r.Body)
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":"policy-2"}`))
		},
		"/policies-api/v1/policies/policy-2": jsonHandler(http.StatusOK, iamCustomerPolicyJSON("policy-2")),
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "iam", "create-policy", "--name", "app-read", "--document-file", doc})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("create-policy: %v (stderr=%s)", err, stderr.String())
	}

	var decoded map[string]any
	if err := json.Unmarshal(postBody, &decoded); err != nil {
		t.Fatalf("POST body is not valid JSON: %v (%s)", err, postBody)
	}
	if decoded["name"] != "app-read" {
		t.Fatalf("POST body = %s, want name=app-read", postBody)
	}
	statements, ok := decoded["statements"].([]any)
	if !ok || len(statements) != 1 {
		t.Fatalf("POST body = %s, want one statement", postBody)
	}
	stmt := statements[0].(map[string]any)
	if stmt["effect"] != "allow" {
		t.Fatalf("POST body statement = %v, want effect=allow", stmt)
	}

	if !strings.Contains(stdout.String(), `"ID": "policy-2"`) {
		t.Fatalf("stdout = %s, want the confirmed policy printed", stdout.String())
	}
}

// TestIAMCreatePolicyNotSettledOnFailedConfirmRead drives a real create-policy
// call whose POST succeeds and whose confirm read (the GetPolicy call
// CreatePolicy makes right after) fails with a 500. Per iam.CreatePolicy's
// own contract, that failure surfaces as an error wrapping iam.ErrNotSettled,
// with the Output falling back to the new policy's own ID; op.go must still
// print that Output on stdout even though the command exits with an error,
// the same as dns, network, and compute's own NotSettled writes.
func TestIAMCreatePolicyNotSettledOnFailedConfirmRead(t *testing.T) {
	doc := writeDocumentFile(t,
		`{"statements": [{"effect": "allow", "actions": ["vserver:ListServers"], "resources": ["*"]}]}`)

	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/accounts-api/v1/auth/userinfo": jsonHandler(http.StatusOK, iamUserInfoJSON("user-1", "iam-user")),
		"/policies-api/v1/actions":       jsonHandler(http.StatusOK, iamWriteActionsJSON),
		"/policies-api/v1/policies": func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				t.Fatalf("method = %s, want POST", r.Method)
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":"policy-2"}`))
		},
		"/policies-api/v1/policies/policy-2": func(w http.ResponseWriter, _ *http.Request) {
			// The confirm read after the POST: fails, so the write is
			// accepted but never confirmed.
			w.WriteHeader(http.StatusInternalServerError)
		},
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "iam", "create-policy", "--name", "app-read", "--document-file", doc})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected an error")
	}
	if got := classify(err).Code; got != "NotSettled" {
		t.Fatalf("Code = %q, want NotSettled (stderr=%s)", got, stderr.String())
	}
	if got := exitCode(err); got != 1 {
		t.Fatalf("exitCode = %d, want 1", got)
	}
	if got := stdout.String(); !strings.Contains(got, `"ID": "policy-2"`) {
		t.Fatalf("stdout = %s, want the fallback policy ID printed", got)
	}
}

// TestIAMCreatePolicyDocumentFileGoFieldNames checks that a --document-file
// using get-policy's own Go field names (Statements, Effect, Actions,
// Resources) decodes the same way as the console's lower-case form, since
// Go's json decoder matches keys without regard to case.
func TestIAMCreatePolicyDocumentFileGoFieldNames(t *testing.T) {
	doc := writeDocumentFile(t,
		`{"Statements": [{"Effect": "allow", "Actions": ["vserver:ListServers"], "Resources": ["*"]}]}`)

	var postBody []byte
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/accounts-api/v1/auth/userinfo": jsonHandler(http.StatusOK, iamUserInfoJSON("user-1", "iam-user")),
		"/policies-api/v1/actions":       jsonHandler(http.StatusOK, iamWriteActionsJSON),
		"/policies-api/v1/policies": func(w http.ResponseWriter, r *http.Request) {
			defer func() { _ = r.Body.Close() }()
			postBody, _ = io.ReadAll(r.Body)
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":"policy-2"}`))
		},
		"/policies-api/v1/policies/policy-2": jsonHandler(http.StatusOK, iamCustomerPolicyJSON("policy-2")),
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "iam", "create-policy", "--name", "app-read", "--document-file", doc})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("create-policy: %v (stderr=%s)", err, stderr.String())
	}
	if !strings.Contains(string(postBody), `"vserver:ListServers"`) {
		t.Fatalf("POST body = %s, want the decoded action", postBody)
	}
}

// TestIAMCreatePolicyDocumentFileAWSStyleExitsTwoWithZeroRequests checks
// that an AWS-style document (top-level Version and Statement, a
// capitalized Effect, and singular Action and Resource keys) is refused with
// exit 2 before any request, since decoding refuses every unknown field.
func TestIAMCreatePolicyDocumentFileAWSStyleExitsTwoWithZeroRequests(t *testing.T) {
	doc := writeDocumentFile(t,
		`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":["vserver:ListServers"],"Resource":["*"]}]}`)

	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "iam", "create-policy", "--name", "app-read", "--document-file", doc})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected an error for an AWS-style document")
	}
	if got := exitCode(err); got != 2 {
		t.Fatalf("exitCode = %d, want 2 (stderr=%s)", got, stderr.String())
	}
	if n := fixture.requestCount(); n != 0 {
		t.Fatalf("requestCount = %d, want 0", n)
	}
}

// TestIAMCreatePolicyDocumentFileUnknownKeyExitsTwoWithZeroRequests checks
// that a document holding a valid statements array plus one extra top-level
// key is refused with exit 2 before any request, distinct from the
// AWS-style shape above.
func TestIAMCreatePolicyDocumentFileUnknownKeyExitsTwoWithZeroRequests(t *testing.T) {
	doc := writeDocumentFile(t,
		`{"statements":[{"effect":"allow","actions":["vserver:ListServers"],"resources":["*"]}],"extra":"nope"}`)

	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "iam", "create-policy", "--name", "app-read", "--document-file", doc})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected an error for an unknown top-level key")
	}
	if got := exitCode(err); got != 2 {
		t.Fatalf("exitCode = %d, want 2 (stderr=%s)", got, stderr.String())
	}
	if n := fixture.requestCount(); n != 0 {
		t.Fatalf("requestCount = %d, want 0", n)
	}
}

// TestIAMCreatePolicyDocumentFileOversizedExitsTwoWithZeroRequests checks
// that a --document-file larger than the design's 64 KiB cap is refused with
// exit 2 before any request.
func TestIAMCreatePolicyDocumentFileOversizedExitsTwoWithZeroRequests(t *testing.T) {
	oversized := strings.Repeat(" ", maxPolicyDocumentFileSize+1)
	doc := writeDocumentFile(t, oversized)

	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "iam", "create-policy", "--name", "app-read", "--document-file", doc})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected an error for an oversized document file")
	}
	if got := exitCode(err); got != 2 {
		t.Fatalf("exitCode = %d, want 2 (stderr=%s)", got, stderr.String())
	}
	if n := fixture.requestCount(); n != 0 {
		t.Fatalf("requestCount = %d, want 0", n)
	}
}

// TestIAMCreatePolicyRequiresStatementsWithZeroRequests checks that
// create-policy refuses, before any request, when neither --document-file
// nor --cli-input-json ever sets Statements.
func TestIAMCreatePolicyRequiresStatementsWithZeroRequests(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "iam", "create-policy", "--name", "app-read"})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected an error without Statements")
	}
	if got := exitCode(err); got != 2 {
		t.Fatalf("exitCode = %d, want 2 (stderr=%s)", got, stderr.String())
	}
	if n := fixture.requestCount(); n != 0 {
		t.Fatalf("requestCount = %d, want 0", n)
	}
}

// TestIAMUpdatePolicyGolden drives update-policy end to end: the guard reads
// the current policy, finds it unmanaged and unattached, and the PUT sends
// the merged body; the confirm read's own policy prints.
func TestIAMUpdatePolicyGolden(t *testing.T) {
	var putBody []byte
	fixture := newSvcFixture(mergeRoutes(
		iamNoAttachmentsRoutes("policy-1"),
		map[string]func(http.ResponseWriter, *http.Request){
			"/accounts-api/v1/auth/userinfo": jsonHandler(http.StatusOK, iamUserInfoJSON("user-1", "iam-user")),
			"/policies-api/v1/actions":       jsonHandler(http.StatusOK, iamWriteActionsJSON),
			"/policies-api/v1/policies/policy-1": func(w http.ResponseWriter, r *http.Request) {
				switch r.Method {
				case http.MethodGet:
					jsonHandler(http.StatusOK, iamCustomerPolicyJSON("policy-1"))(w, r)
				case http.MethodPut:
					defer func() { _ = r.Body.Close() }()
					putBody, _ = io.ReadAll(r.Body)
					w.WriteHeader(http.StatusNoContent)
				default:
					t.Fatalf("unexpected method %s", r.Method)
				}
			},
		},
	))
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "--yes", "iam", "update-policy", "--policy-id", "policy-1", "--description", "new-desc"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("update-policy: %v (stderr=%s)", err, stderr.String())
	}
	var decoded map[string]any
	if err := json.Unmarshal(putBody, &decoded); err != nil {
		t.Fatalf("PUT body is not valid JSON: %v (%s)", err, putBody)
	}
	if decoded["description"] != "new-desc" {
		t.Fatalf("PUT body = %s, want description=new-desc", putBody)
	}
	if decoded["name"] != "app-read" {
		t.Fatalf("PUT body = %s, want the current name kept", putBody)
	}
	if !strings.Contains(stdout.String(), `"ID": "policy-1"`) {
		t.Fatalf("stdout = %s, want the confirmed policy printed", stdout.String())
	}
}

// TestIAMUpdatePolicyRequiresYesWithZeroRequests checks that update-policy,
// Write and Destructive, refuses before any request, including the SDK's own
// guard reads, when --yes is missing.
func TestIAMUpdatePolicyRequiresYesWithZeroRequests(t *testing.T) {
	unexpected := func(_ http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
	}
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/accounts-api/v1/auth/userinfo":     unexpected,
		"/policies-api/v1/policies/policy-1": unexpected,
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "iam", "update-policy", "--policy-id", "policy-1", "--description", "x"})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected an error without --yes")
	}
	if got := exitCode(err); got != 2 {
		t.Fatalf("exitCode = %d, want 2 (stderr=%s)", got, stderr.String())
	}
	if n := fixture.requestCount(); n != 0 {
		t.Fatalf("requestCount = %d, want 0", n)
	}
}

// TestIAMUpdatePolicyGuardSentinelsMapToDesignCodes checks that
// iam.ErrManagedPolicy and iam.ErrInUse, returned by the SDK's own guard,
// reach the CLI's stderr as the design's ManagedPolicy and ResourceInUse
// error classes, with no write request sent.
func TestIAMUpdatePolicyGuardSentinelsMapToDesignCodes(t *testing.T) {
	unexpectedPut := func(_ http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			t.Errorf("unexpected write: %s %s", r.Method, r.URL.Path)
		}
	}
	managedPolicyJSON := `{"id":"policy-1","name":"managed","description":"d","manager":"VNG CLOUD","scope":"account","statements":[],"createdAt":1700000000000}`

	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/accounts-api/v1/auth/userinfo": jsonHandler(http.StatusOK, iamUserInfoJSON("user-1", "iam-user")),
		"/policies-api/v1/policies/policy-1": func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodGet {
				jsonHandler(http.StatusOK, managedPolicyJSON)(w, r)
				return
			}
			unexpectedPut(w, r)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "--yes", "iam", "update-policy", "--policy-id", "policy-1", "--description", "x"})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected a ManagedPolicy error")
	}
	if got := classify(err).Code; got != "ManagedPolicy" {
		t.Fatalf("Code = %q, want ManagedPolicy (stderr=%s)", got, stderr.String())
	}
	if got := exitCode(err); got != 1 {
		t.Fatalf("exitCode = %d, want 1 (stderr=%s)", got, stderr.String())
	}
}

// TestIAMDeletePolicyGolden drives delete-policy end to end: the guard reads
// the policy and its three attachment lists, finds it unmanaged and
// unattached, and the DELETE is sent.
func TestIAMDeletePolicyGolden(t *testing.T) {
	deleted := false
	fixture := newSvcFixture(mergeRoutes(
		iamNoAttachmentsRoutes("policy-1"),
		map[string]func(http.ResponseWriter, *http.Request){
			"/accounts-api/v1/auth/userinfo": jsonHandler(http.StatusOK, iamUserInfoJSON("user-1", "iam-user")),
			"/policies-api/v1/policies/policy-1": func(w http.ResponseWriter, r *http.Request) {
				switch r.Method {
				case http.MethodGet:
					jsonHandler(http.StatusOK, iamCustomerPolicyJSON("policy-1"))(w, r)
				case http.MethodDelete:
					deleted = true
					w.WriteHeader(http.StatusNoContent)
				default:
					t.Fatalf("unexpected method %s", r.Method)
				}
			},
		},
	))
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "--yes", "iam", "delete-policy", "--policy-id", "policy-1"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("delete-policy: %v (stderr=%s)", err, stderr.String())
	}
	if !deleted {
		t.Fatal("the DELETE was never sent")
	}
}

// TestIAMDeletePolicyRequiresYesWithZeroRequests checks that delete-policy,
// Write and Destructive, refuses before any request when --yes is missing.
func TestIAMDeletePolicyRequiresYesWithZeroRequests(t *testing.T) {
	unexpected := func(_ http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
	}
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/accounts-api/v1/auth/userinfo":     unexpected,
		"/policies-api/v1/policies/policy-1": unexpected,
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "iam", "delete-policy", "--policy-id", "policy-1"})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected an error without --yes")
	}
	if got := exitCode(err); got != 2 {
		t.Fatalf("exitCode = %d, want 2 (stderr=%s)", got, stderr.String())
	}
	if n := fixture.requestCount(); n != 0 {
		t.Fatalf("requestCount = %d, want 0", n)
	}
}

// TestIAMDeletePolicyInUseMapsToResourceInUse checks that iam.ErrInUse,
// returned by the SDK's own guard when a policy is still attached, reaches
// the CLI's stderr as the design's ResourceInUse error class, with no
// DELETE sent.
func TestIAMDeletePolicyInUseMapsToResourceInUse(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/accounts-api/v1/auth/userinfo": jsonHandler(http.StatusOK, iamUserInfoJSON("user-1", "iam-user")),
		"/policies-api/v1/policies/policy-1": func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodDelete {
				t.Fatal("the DELETE must not be sent while the policy is attached")
			}
			jsonHandler(http.StatusOK, iamCustomerPolicyJSON("policy-1"))(w, r)
		},
		"/policies-api/v1/policies/policy-1/groups":           jsonHandler(http.StatusOK, `[{"id":"group-1","name":"g","createdAt":1700000000000}]`),
		"/policies-api/v1/policies/policy-1/iam-users":        jsonHandler(http.StatusOK, `[]`),
		"/policies-api/v1/policies/policy-1/service-accounts": jsonHandler(http.StatusOK, `[]`),
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "--yes", "iam", "delete-policy", "--policy-id", "policy-1"})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected a ResourceInUse error")
	}
	if got := classify(err).Code; got != "ResourceInUse" {
		t.Fatalf("Code = %q, want ResourceInUse (stderr=%s)", got, stderr.String())
	}
	if got := exitCode(err); got != 1 {
		t.Fatalf("exitCode = %d, want 1 (stderr=%s)", got, stderr.String())
	}
}

// TestIAMAttachServiceAccountPolicyGolden drives attach-service-account-policy
// end to end: the guard finds the caller unclassified as a service account,
// the target service account unprotected, and the policy unprivileged, then
// sends the attach POST.
func TestIAMAttachServiceAccountPolicyGolden(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/accounts-api/v1/auth/userinfo":                                   jsonHandler(http.StatusOK, iamUserInfoJSON("user-1", "iam-user")),
		"/policies-api/v1/actions":                                         jsonHandler(http.StatusOK, iamWriteActionsJSON),
		"/policies-api/v1/user-attachments/service-accounts/sa-1/policies": jsonHandler(http.StatusOK, iamNoPoliciesJSON),
		"/policies-api/v1/policies/policy-1":                               jsonHandler(http.StatusOK, iamCustomerPolicyJSON("policy-1")),
		"/policies-api/v1/policies/policy-1/service-accounts/sa-1": func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				t.Fatalf("method = %s, want POST", r.Method)
			}
			w.WriteHeader(http.StatusNoContent)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "--yes", "iam", "attach-service-account-policy", "--policy-id", "policy-1", "--service-account-id", "sa-1"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("attach-service-account-policy: %v (stderr=%s)", err, stderr.String())
	}
	if got, ok := fixture.methodFor("/policies-api/v1/policies/policy-1/service-accounts/sa-1"); !ok || got != http.MethodPost {
		t.Fatalf("method = %q, ok=%v, want POST", got, ok)
	}
}

// TestIAMAttachServiceAccountPolicyRequiresYesWithZeroRequests checks that
// attach-service-account-policy, Write and Destructive, refuses before any
// request when --yes is missing.
func TestIAMAttachServiceAccountPolicyRequiresYesWithZeroRequests(t *testing.T) {
	unexpected := func(_ http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
	}
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/accounts-api/v1/auth/userinfo":                           unexpected,
		"/policies-api/v1/policies/policy-1/service-accounts/sa-1": unexpected,
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "iam", "attach-service-account-policy", "--policy-id", "policy-1", "--service-account-id", "sa-1"})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected an error without --yes")
	}
	if got := exitCode(err); got != 2 {
		t.Fatalf("exitCode = %d, want 2 (stderr=%s)", got, stderr.String())
	}
	if n := fixture.requestCount(); n != 0 {
		t.Fatalf("requestCount = %d, want 0", n)
	}
}

// TestIAMDetachServiceAccountPolicyGolden mirrors
// TestIAMAttachServiceAccountPolicyGolden for the detach path.
func TestIAMDetachServiceAccountPolicyGolden(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/accounts-api/v1/auth/userinfo":                                   jsonHandler(http.StatusOK, iamUserInfoJSON("user-1", "iam-user")),
		"/policies-api/v1/actions":                                         jsonHandler(http.StatusOK, iamWriteActionsJSON),
		"/policies-api/v1/user-attachments/service-accounts/sa-1/policies": jsonHandler(http.StatusOK, iamNoPoliciesJSON),
		"/policies-api/v1/policies/policy-1":                               jsonHandler(http.StatusOK, iamCustomerPolicyJSON("policy-1")),
		"/policies-api/v1/policies/policy-1/service-accounts/sa-1": func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodDelete {
				t.Fatalf("method = %s, want DELETE", r.Method)
			}
			w.WriteHeader(http.StatusNoContent)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "--yes", "iam", "detach-service-account-policy", "--policy-id", "policy-1", "--service-account-id", "sa-1"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("detach-service-account-policy: %v (stderr=%s)", err, stderr.String())
	}
	if got, ok := fixture.methodFor("/policies-api/v1/policies/policy-1/service-accounts/sa-1"); !ok || got != http.MethodDelete {
		t.Fatalf("method = %q, ok=%v, want DELETE", got, ok)
	}
}

// TestIAMDetachServiceAccountPolicyRequiresYesWithZeroRequests checks that
// detach-service-account-policy, Write and Destructive, refuses before any
// request when --yes is missing.
func TestIAMDetachServiceAccountPolicyRequiresYesWithZeroRequests(t *testing.T) {
	unexpected := func(_ http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
	}
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/accounts-api/v1/auth/userinfo":                           unexpected,
		"/policies-api/v1/policies/policy-1/service-accounts/sa-1": unexpected,
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "iam", "detach-service-account-policy", "--policy-id", "policy-1", "--service-account-id", "sa-1"})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected an error without --yes")
	}
	if got := exitCode(err); got != 2 {
		t.Fatalf("exitCode = %d, want 2 (stderr=%s)", got, stderr.String())
	}
	if n := fixture.requestCount(); n != 0 {
		t.Fatalf("requestCount = %d, want 0", n)
	}
}

// TestIAMAttachServiceAccountPolicySelfChangeMapsToDesignCode checks that
// iam.ErrSelfChange, returned when the caller's own type is a service
// account, reaches the CLI's stderr as SelfChange, with no attach sent.
func TestIAMAttachServiceAccountPolicySelfChangeMapsToDesignCode(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/accounts-api/v1/auth/userinfo": jsonHandler(http.StatusOK, iamUserInfoJSON("sa-1", "user-sa")),
		"/policies-api/v1/policies/policy-1/service-accounts/sa-1": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "--yes", "iam", "attach-service-account-policy", "--policy-id", "policy-1", "--service-account-id", "sa-1"})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected a SelfChange error")
	}
	if got := classify(err).Code; got != "SelfChange" {
		t.Fatalf("Code = %q, want SelfChange (stderr=%s)", got, stderr.String())
	}
	if got := exitCode(err); got != 1 {
		t.Fatalf("exitCode = %d, want 1 (stderr=%s)", got, stderr.String())
	}
}
