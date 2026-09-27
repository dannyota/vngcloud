package cli

import (
	"context"
	"net/http"
	"testing"
)

// TestIAMAttachGroupPolicyGolden drives attach-group-policy end to end: the
// guard finds the group unprotected and the policy unprivileged, then sends
// the attach POST.
func TestIAMAttachGroupPolicyGolden(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/accounts-api/v1/auth/userinfo":     jsonHandler(http.StatusOK, iamUserInfoJSON("user-1", "iam-user")),
		"/policies-api/v1/actions":           jsonHandler(http.StatusOK, iamWriteActionsJSON),
		"/policies-api/v1/groups/group-1":    jsonHandler(http.StatusOK, `{"policies":[],"iamUsers":[],"mode":"iam"}`),
		"/policies-api/v1/policies/policy-1": jsonHandler(http.StatusOK, iamCustomerPolicyJSON("policy-1")),
		"/policies-api/v1/policies/policy-1/groups/group-1": func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				t.Fatalf("method = %s, want POST", r.Method)
			}
			w.WriteHeader(http.StatusNoContent)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "--yes", "iam", "attach-group-policy", "--policy-id", "policy-1", "--group-id", "group-1"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("attach-group-policy: %v (stderr=%s)", err, stderr.String())
	}
	if got, ok := fixture.methodFor("/policies-api/v1/policies/policy-1/groups/group-1"); !ok || got != http.MethodPost {
		t.Fatalf("method = %q, ok=%v, want POST", got, ok)
	}
}

// TestIAMAttachGroupPolicyRequiresYesWithZeroRequests checks that
// attach-group-policy, Write and Destructive, refuses before any request
// when --yes is missing.
func TestIAMAttachGroupPolicyRequiresYesWithZeroRequests(t *testing.T) {
	unexpected := func(_ http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
	}
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/accounts-api/v1/auth/userinfo":                    unexpected,
		"/policies-api/v1/policies/policy-1/groups/group-1": unexpected,
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "iam", "attach-group-policy", "--policy-id", "policy-1", "--group-id", "group-1"})
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

// TestIAMAttachGroupPolicySelfChangeMapsToDesignCode checks that
// iam.ErrSelfChange, returned when the target group already counts the
// caller as a member, reaches the CLI's stderr as SelfChange, with no
// attach sent and no GetPolicy read.
func TestIAMAttachGroupPolicySelfChangeMapsToDesignCode(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/accounts-api/v1/auth/userinfo":  jsonHandler(http.StatusOK, iamUserInfoJSON("user-1", "iam-user")),
		"/policies-api/v1/actions":        jsonHandler(http.StatusOK, iamWriteActionsJSON),
		"/policies-api/v1/groups/group-1": jsonHandler(http.StatusOK, `{"policies":[],"iamUsers":["user-1"],"mode":"iam"}`),
		"/policies-api/v1/policies/policy-1": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
		"/policies-api/v1/policies/policy-1/groups/group-1": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "--yes", "iam", "attach-group-policy", "--policy-id", "policy-1", "--group-id", "group-1"})
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

// TestIAMAttachGroupPolicyPrivilegedChangeMapsToDesignCode checks that
// iam.ErrPrivilegedChange, returned when the policy itself grants an IAM
// write action, reaches the CLI's stderr as PrivilegedChange, with no
// attach sent.
func TestIAMAttachGroupPolicyPrivilegedChangeMapsToDesignCode(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/accounts-api/v1/auth/userinfo":     jsonHandler(http.StatusOK, iamUserInfoJSON("user-1", "iam-user")),
		"/policies-api/v1/actions":           jsonHandler(http.StatusOK, iamWriteActionsJSON),
		"/policies-api/v1/groups/group-1":    jsonHandler(http.StatusOK, `{"policies":[],"iamUsers":[],"mode":"iam"}`),
		"/policies-api/v1/policies/policy-1": jsonHandler(http.StatusOK, iamPrivilegedPolicyJSON),
		"/policies-api/v1/policies/policy-1/groups/group-1": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "--yes", "iam", "attach-group-policy", "--policy-id", "policy-1", "--group-id", "group-1"})
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

// TestIAMDetachGroupPolicyGolden mirrors TestIAMAttachGroupPolicyGolden for
// the detach path.
func TestIAMDetachGroupPolicyGolden(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/accounts-api/v1/auth/userinfo":     jsonHandler(http.StatusOK, iamUserInfoJSON("user-1", "iam-user")),
		"/policies-api/v1/actions":           jsonHandler(http.StatusOK, iamWriteActionsJSON),
		"/policies-api/v1/groups/group-1":    jsonHandler(http.StatusOK, `{"policies":["policy-1"],"iamUsers":[],"mode":"iam"}`),
		"/policies-api/v1/policies/policy-1": jsonHandler(http.StatusOK, iamCustomerPolicyJSON("policy-1")),
		"/policies-api/v1/policies/policy-1/groups/group-1": func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodDelete {
				t.Fatalf("method = %s, want DELETE", r.Method)
			}
			w.WriteHeader(http.StatusNoContent)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "--yes", "iam", "detach-group-policy", "--policy-id", "policy-1", "--group-id", "group-1"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("detach-group-policy: %v (stderr=%s)", err, stderr.String())
	}
	if got, ok := fixture.methodFor("/policies-api/v1/policies/policy-1/groups/group-1"); !ok || got != http.MethodDelete {
		t.Fatalf("method = %q, ok=%v, want DELETE", got, ok)
	}
}

// TestIAMDetachGroupPolicyRequiresYesWithZeroRequests checks that
// detach-group-policy, Write and Destructive, refuses before any request
// when --yes is missing.
func TestIAMDetachGroupPolicyRequiresYesWithZeroRequests(t *testing.T) {
	unexpected := func(_ http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
	}
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/accounts-api/v1/auth/userinfo":                    unexpected,
		"/policies-api/v1/policies/policy-1/groups/group-1": unexpected,
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "iam", "detach-group-policy", "--policy-id", "policy-1", "--group-id", "group-1"})
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
