package cli

import (
	"context"
	"net/http"
	"testing"
)

// TestIAMAddUserToGroupGolden drives add-user-to-group end to end: the
// guard finds the user and the group both unprotected, then sends the add
// POST.
func TestIAMAddUserToGroupGolden(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/accounts-api/v1/auth/userinfo":                              jsonHandler(http.StatusOK, iamUserInfoJSON("user-1", "iam-user")),
		"/policies-api/v1/actions":                                    jsonHandler(http.StatusOK, iamWriteActionsJSON),
		"/policies-api/v1/user-attachments/iam-users/user-x/policies": jsonHandler(http.StatusOK, iamNoPoliciesJSON),
		"/policies-api/v1/user-attachments/iam-users/user-x/groups":   jsonHandler(http.StatusOK, `[]`),
		"/policies-api/v1/groups/group-1":                             jsonHandler(http.StatusOK, `{"policies":[],"iamUsers":[]}`),
		"/policies-api/v1/groups/group-1/iam-users/user-x": func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				t.Fatalf("method = %s, want POST", r.Method)
			}
			w.WriteHeader(http.StatusNoContent)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "--yes", "iam", "add-user-to-group", "--group-id", "group-1", "--user-id", "user-x"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("add-user-to-group: %v (stderr=%s)", err, stderr.String())
	}
	if got, ok := fixture.methodFor("/policies-api/v1/groups/group-1/iam-users/user-x"); !ok || got != http.MethodPost {
		t.Fatalf("method = %q, ok=%v, want POST", got, ok)
	}
}

// TestIAMAddUserToGroupRequiresYesWithZeroRequests checks that
// add-user-to-group, Write and Destructive, refuses before any request when
// --yes is missing.
func TestIAMAddUserToGroupRequiresYesWithZeroRequests(t *testing.T) {
	unexpected := func(_ http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
	}
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/accounts-api/v1/auth/userinfo":                   unexpected,
		"/policies-api/v1/groups/group-1/iam-users/user-x": unexpected,
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "iam", "add-user-to-group", "--group-id", "group-1", "--user-id", "user-x"})
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

// TestIAMAddUserToGroupSelfChangeMapsToDesignCode checks that
// iam.ErrSelfChange, returned when the target user is the caller itself,
// reaches the CLI's stderr as SelfChange, with no add sent. The group's own
// attachments are still read (the guard checks both independently), so the
// fixture must answer that read too even though its result cannot change
// the outcome.
func TestIAMAddUserToGroupSelfChangeMapsToDesignCode(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/accounts-api/v1/auth/userinfo":  jsonHandler(http.StatusOK, iamUserInfoJSON("user-1", "iam-user")),
		"/policies-api/v1/actions":        jsonHandler(http.StatusOK, iamWriteActionsJSON),
		"/policies-api/v1/groups/group-1": jsonHandler(http.StatusOK, `{"policies":[],"iamUsers":[]}`),
		"/policies-api/v1/groups/group-1/iam-users/user-1": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "--yes", "iam", "add-user-to-group", "--group-id", "group-1", "--user-id", "user-1"})
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

// TestIAMAddUserToGroupPrivilegedChangeMapsToDesignCode checks that
// iam.ErrPrivilegedChange, returned when the target user already holds a
// privileged policy directly, reaches the CLI's stderr as PrivilegedChange,
// with no add sent.
func TestIAMAddUserToGroupPrivilegedChangeMapsToDesignCode(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/accounts-api/v1/auth/userinfo":                              jsonHandler(http.StatusOK, iamUserInfoJSON("user-1", "iam-user")),
		"/policies-api/v1/actions":                                    jsonHandler(http.StatusOK, iamWriteActionsJSON),
		"/policies-api/v1/user-attachments/iam-users/user-x/policies": jsonHandler(http.StatusOK, iamPrivilegedPolicyAttachmentJSON),
		"/policies-api/v1/policies/policy-1":                          jsonHandler(http.StatusOK, iamPrivilegedPolicyJSON),
		"/policies-api/v1/groups/group-1":                             jsonHandler(http.StatusOK, `{"policies":[],"iamUsers":[]}`),
		"/policies-api/v1/groups/group-1/iam-users/user-x": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "--yes", "iam", "add-user-to-group", "--group-id", "group-1", "--user-id", "user-x"})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected a PrivilegedChange error")
	}
	if got := classify(err).Code; got != "PrivilegedChange" {
		t.Fatalf("Code = %q, want PrivilegedChange (stderr=%s)", got, stderr.String())
	}
	if got := exitCode(err); got != 1 {
		t.Fatalf("exitCode = %d, want 1 (stderr=%s)", got, stderr.String())
	}
}

// TestIAMRemoveUserFromGroupGolden mirrors TestIAMAddUserToGroupGolden for
// the remove path.
func TestIAMRemoveUserFromGroupGolden(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/accounts-api/v1/auth/userinfo":                              jsonHandler(http.StatusOK, iamUserInfoJSON("user-1", "iam-user")),
		"/policies-api/v1/actions":                                    jsonHandler(http.StatusOK, iamWriteActionsJSON),
		"/policies-api/v1/user-attachments/iam-users/user-x/policies": jsonHandler(http.StatusOK, iamNoPoliciesJSON),
		"/policies-api/v1/user-attachments/iam-users/user-x/groups":   jsonHandler(http.StatusOK, `[]`),
		"/policies-api/v1/groups/group-1":                             jsonHandler(http.StatusOK, `{"policies":[],"iamUsers":["user-x"]}`),
		"/policies-api/v1/groups/group-1/iam-users/user-x": func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodDelete {
				t.Fatalf("method = %s, want DELETE", r.Method)
			}
			w.WriteHeader(http.StatusNoContent)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "--yes", "iam", "remove-user-from-group", "--group-id", "group-1", "--user-id", "user-x"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("remove-user-from-group: %v (stderr=%s)", err, stderr.String())
	}
	if got, ok := fixture.methodFor("/policies-api/v1/groups/group-1/iam-users/user-x"); !ok || got != http.MethodDelete {
		t.Fatalf("method = %q, ok=%v, want DELETE", got, ok)
	}
}

// TestIAMRemoveUserFromGroupRequiresYesWithZeroRequests checks that
// remove-user-from-group, Write and Destructive, refuses before any request
// when --yes is missing.
func TestIAMRemoveUserFromGroupRequiresYesWithZeroRequests(t *testing.T) {
	unexpected := func(_ http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
	}
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/accounts-api/v1/auth/userinfo":                   unexpected,
		"/policies-api/v1/groups/group-1/iam-users/user-x": unexpected,
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "iam", "remove-user-from-group", "--group-id", "group-1", "--user-id", "user-x"})
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
