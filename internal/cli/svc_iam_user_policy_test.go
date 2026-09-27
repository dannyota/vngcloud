package cli

import (
	"context"
	"net/http"
	"testing"
)

// TestIAMAttachUserPolicyGolden drives attach-user-policy end to end: the
// guard finds the target user unprotected and the policy unprivileged, then
// sends the attach POST.
func TestIAMAttachUserPolicyGolden(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/accounts-api/v1/auth/userinfo":                              jsonHandler(http.StatusOK, iamUserInfoJSON("user-1", "iam-user")),
		"/policies-api/v1/actions":                                    jsonHandler(http.StatusOK, iamWriteActionsJSON),
		"/policies-api/v1/user-attachments/iam-users/user-x/policies": jsonHandler(http.StatusOK, iamNoPoliciesJSON),
		"/policies-api/v1/user-attachments/iam-users/user-x/groups":   jsonHandler(http.StatusOK, `[]`),
		"/policies-api/v1/policies/policy-1":                          jsonHandler(http.StatusOK, iamCustomerPolicyJSON("policy-1")),
		"/policies-api/v1/policies/policy-1/iam-users/user-x": func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				t.Fatalf("method = %s, want POST", r.Method)
			}
			w.WriteHeader(http.StatusNoContent)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "--yes", "iam", "attach-user-policy", "--policy-id", "policy-1", "--user-id", "user-x"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("attach-user-policy: %v (stderr=%s)", err, stderr.String())
	}
	if got, ok := fixture.methodFor("/policies-api/v1/policies/policy-1/iam-users/user-x"); !ok || got != http.MethodPost {
		t.Fatalf("method = %q, ok=%v, want POST", got, ok)
	}
}

// TestIAMAttachUserPolicyRequiresYesWithZeroRequests checks that
// attach-user-policy, Write and Destructive, refuses before any request
// when --yes is missing.
func TestIAMAttachUserPolicyRequiresYesWithZeroRequests(t *testing.T) {
	unexpected := func(_ http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
	}
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/accounts-api/v1/auth/userinfo":                      unexpected,
		"/policies-api/v1/policies/policy-1/iam-users/user-x": unexpected,
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "iam", "attach-user-policy", "--policy-id", "policy-1", "--user-id", "user-x"})
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

// TestIAMAttachUserPolicySelfChangeMapsToDesignCode checks that
// iam.ErrSelfChange, returned when the target user is the caller itself,
// reaches the CLI's stderr as SelfChange, with no attach sent and no
// GetPolicy read.
func TestIAMAttachUserPolicySelfChangeMapsToDesignCode(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/accounts-api/v1/auth/userinfo": jsonHandler(http.StatusOK, iamUserInfoJSON("user-1", "iam-user")),
		"/policies-api/v1/actions":       jsonHandler(http.StatusOK, iamWriteActionsJSON),
		"/policies-api/v1/policies/policy-1": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
		"/policies-api/v1/policies/policy-1/iam-users/user-1": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "--yes", "iam", "attach-user-policy", "--policy-id", "policy-1", "--user-id", "user-1"})
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

// TestIAMAttachUserPolicyPrivilegedChangeMapsToDesignCode checks that
// iam.ErrPrivilegedChange, returned when the policy itself grants an IAM
// write action, reaches the CLI's stderr as PrivilegedChange, with no
// attach sent.
func TestIAMAttachUserPolicyPrivilegedChangeMapsToDesignCode(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/accounts-api/v1/auth/userinfo":                              jsonHandler(http.StatusOK, iamUserInfoJSON("user-1", "iam-user")),
		"/policies-api/v1/actions":                                    jsonHandler(http.StatusOK, iamWriteActionsJSON),
		"/policies-api/v1/user-attachments/iam-users/user-x/policies": jsonHandler(http.StatusOK, iamNoPoliciesJSON),
		"/policies-api/v1/user-attachments/iam-users/user-x/groups":   jsonHandler(http.StatusOK, `[]`),
		"/policies-api/v1/policies/policy-1":                          jsonHandler(http.StatusOK, iamPrivilegedPolicyJSON),
		"/policies-api/v1/policies/policy-1/iam-users/user-x": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "--yes", "iam", "attach-user-policy", "--policy-id", "policy-1", "--user-id", "user-x"})
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

// TestIAMDetachUserPolicyGolden mirrors TestIAMAttachUserPolicyGolden for
// the detach path.
func TestIAMDetachUserPolicyGolden(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/accounts-api/v1/auth/userinfo":                              jsonHandler(http.StatusOK, iamUserInfoJSON("user-1", "iam-user")),
		"/policies-api/v1/actions":                                    jsonHandler(http.StatusOK, iamWriteActionsJSON),
		"/policies-api/v1/user-attachments/iam-users/user-x/policies": jsonHandler(http.StatusOK, iamNoPoliciesJSON),
		"/policies-api/v1/user-attachments/iam-users/user-x/groups":   jsonHandler(http.StatusOK, `[]`),
		"/policies-api/v1/policies/policy-1":                          jsonHandler(http.StatusOK, iamCustomerPolicyJSON("policy-1")),
		"/policies-api/v1/policies/policy-1/iam-users/user-x": func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodDelete {
				t.Fatalf("method = %s, want DELETE", r.Method)
			}
			w.WriteHeader(http.StatusNoContent)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "--yes", "iam", "detach-user-policy", "--policy-id", "policy-1", "--user-id", "user-x"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("detach-user-policy: %v (stderr=%s)", err, stderr.String())
	}
	if got, ok := fixture.methodFor("/policies-api/v1/policies/policy-1/iam-users/user-x"); !ok || got != http.MethodDelete {
		t.Fatalf("method = %q, ok=%v, want DELETE", got, ok)
	}
}

// TestIAMDetachUserPolicyRequiresYesWithZeroRequests checks that
// detach-user-policy, Write and Destructive, refuses before any request
// when --yes is missing.
func TestIAMDetachUserPolicyRequiresYesWithZeroRequests(t *testing.T) {
	unexpected := func(_ http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
	}
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/accounts-api/v1/auth/userinfo":                      unexpected,
		"/policies-api/v1/policies/policy-1/iam-users/user-x": unexpected,
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "iam", "detach-user-policy", "--policy-id", "policy-1", "--user-id", "user-x"})
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
