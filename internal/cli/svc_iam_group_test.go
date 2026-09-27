package cli

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

// groupJSON builds one bare Group object, the shape GetGroup decodes and
// every confirm read after a group write uses.
func groupJSON(id, name string) string {
	return `{"id":"` + id + `","name":"` + name + `","description":"d","mode":"iam","root":"123",` +
		`"iamUsers":[],"policies":[],"createdAt":1700000000000}`
}

// TestIAMCreateGroupGolden drives create-group end to end: the POST body
// always sends mode "iam" with no iamUsers or policies, and the confirm
// read's own group prints.
func TestIAMCreateGroupGolden(t *testing.T) {
	var postBody []byte
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/policies-api/v1/groups": func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				t.Fatalf("method = %s, want POST", r.Method)
			}
			defer func() { _ = r.Body.Close() }()
			postBody, _ = io.ReadAll(r.Body)
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":"group-1"}`))
		},
		"/policies-api/v1/groups/group-1": jsonHandler(http.StatusOK, groupJSON("group-1", "app-readers")),
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "iam", "create-group", "--name", "app-readers", "--description", "read only"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("create-group: %v (stderr=%s)", err, stderr.String())
	}

	var decoded map[string]any
	if err := json.Unmarshal(postBody, &decoded); err != nil {
		t.Fatalf("POST body is not valid JSON: %v (%s)", err, postBody)
	}
	if decoded["name"] != "app-readers" || decoded["description"] != "read only" || decoded["mode"] != "iam" {
		t.Fatalf("POST body = %s, want name=app-readers, description=\"read only\", mode=iam", postBody)
	}
	if _, ok := decoded["iamUsers"]; ok {
		t.Fatalf("POST body = %s, want iamUsers omitted", postBody)
	}
	if _, ok := decoded["policies"]; ok {
		t.Fatalf("POST body = %s, want policies omitted", postBody)
	}
	if !strings.Contains(stdout.String(), `"ID": "group-1"`) {
		t.Fatalf("stdout = %s, want the confirmed group printed", stdout.String())
	}
}

// TestIAMCreateGroupNotSettledOnFailedConfirmRead mirrors
// TestIAMCreatePolicyNotSettledOnFailedConfirmRead for create-group: the
// POST succeeds but the confirm GetGroup fails, so the CLI must still print
// the fallback Output (holding only the new group's ID) on stdout even
// though the command exits with a NotSettled error.
func TestIAMCreateGroupNotSettledOnFailedConfirmRead(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/policies-api/v1/groups": func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				t.Fatalf("method = %s, want POST", r.Method)
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":"group-1"}`))
		},
		"/policies-api/v1/groups/group-1": func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		},
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "iam", "create-group", "--name", "app"})
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
	if got := stdout.String(); !strings.Contains(got, `"ID": "group-1"`) {
		t.Fatalf("stdout = %s, want the fallback group ID printed", got)
	}
}

// TestIAMUpdateGroupGolden drives update-group end to end: Name left unset
// is filled from a read of the group's current state first, since the PATCH
// requires a name, and the confirm read's own group prints. Unlike every
// Destructive iam write, update-group needs no --yes.
func TestIAMUpdateGroupGolden(t *testing.T) {
	var patchBody []byte
	var gets int
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/policies-api/v1/groups/group-1": func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodGet:
				gets++
				jsonHandler(http.StatusOK, groupJSON("group-1", "g"))(w, r)
			case http.MethodPatch:
				defer func() { _ = r.Body.Close() }()
				patchBody, _ = io.ReadAll(r.Body)
				w.WriteHeader(http.StatusNoContent)
			default:
				t.Fatalf("unexpected method %s", r.Method)
			}
		},
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "iam", "update-group", "--group-id", "group-1", "--description", "new-desc"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("update-group: %v (stderr=%s)", err, stderr.String())
	}
	var decoded map[string]any
	if err := json.Unmarshal(patchBody, &decoded); err != nil {
		t.Fatalf("PATCH body is not valid JSON: %v (%s)", err, patchBody)
	}
	if decoded["name"] != "g" {
		t.Fatalf("PATCH body = %s, want the current name filled in", patchBody)
	}
	if decoded["description"] != "new-desc" {
		t.Fatalf("PATCH body = %s, want description=new-desc", patchBody)
	}
	if gets != 2 {
		t.Fatalf("GET count = %d, want 2 (fill-name read, then the confirm read)", gets)
	}
	if !strings.Contains(stdout.String(), `"ID": "group-1"`) {
		t.Fatalf("stdout = %s, want the confirmed group printed", stdout.String())
	}
}

// TestIAMUpdateGroupNotSettledOnFailedConfirmRead checks that a failed
// confirm read after a landed PATCH prints the fallback Output (holding only
// GroupID) on stdout, the same as create-group's own NotSettled case.
func TestIAMUpdateGroupNotSettledOnFailedConfirmRead(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/policies-api/v1/groups/group-1": func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPatch {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			w.WriteHeader(http.StatusInternalServerError)
		},
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "iam", "update-group", "--group-id", "group-1", "--name", "renamed"})
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
	if got := stdout.String(); !strings.Contains(got, `"ID": "group-1"`) {
		t.Fatalf("stdout = %s, want the fallback group ID printed", got)
	}
}

// TestIAMDeleteGroupRequiresYesWithZeroRequests checks that delete-group,
// Write and Destructive, refuses before any request when --yes is missing.
func TestIAMDeleteGroupRequiresYesWithZeroRequests(t *testing.T) {
	unexpected := func(_ http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
	}
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/accounts-api/v1/auth/userinfo":  unexpected,
		"/policies-api/v1/groups/group-1": unexpected,
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "iam", "delete-group", "--group-id", "group-1"})
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

// TestIAMDeleteGroupGolden drives delete-group end to end: the guard finds
// the group empty (no member, no attached policy) and the DELETE is sent.
func TestIAMDeleteGroupGolden(t *testing.T) {
	deleted := false
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/accounts-api/v1/auth/userinfo": jsonHandler(http.StatusOK, iamUserInfoJSON("user-1", "iam-user")),
		"/policies-api/v1/groups/group-1": func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodGet:
				jsonHandler(http.StatusOK, `{"policies":[],"iamUsers":[]}`)(w, r)
			case http.MethodDelete:
				deleted = true
				w.WriteHeader(http.StatusNoContent)
			default:
				t.Fatalf("unexpected method %s", r.Method)
			}
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "--yes", "iam", "delete-group", "--group-id", "group-1"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("delete-group: %v (stderr=%s)", err, stderr.String())
	}
	if !deleted {
		t.Fatal("the DELETE was never sent")
	}
}

// TestIAMDeleteGroupInUseMapsToResourceInUse checks that iam.ErrInUse,
// returned by the SDK's own guard when a group still has a member, reaches
// the CLI's stderr as the design's ResourceInUse error class, with no
// DELETE sent.
func TestIAMDeleteGroupInUseMapsToResourceInUse(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/accounts-api/v1/auth/userinfo": jsonHandler(http.StatusOK, iamUserInfoJSON("user-1", "iam-user")),
		"/policies-api/v1/groups/group-1": func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodDelete {
				t.Fatal("the DELETE must not be sent while the group has a member")
			}
			jsonHandler(http.StatusOK, `{"policies":[],"iamUsers":["user-2"]}`)(w, r)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "--yes", "iam", "delete-group", "--group-id", "group-1"})
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
