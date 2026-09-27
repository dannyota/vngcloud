package iam

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/endpoints"
	"danny.vn/vngcloud/internal/transport"
)

// unprivilegedGroupAttachFixture builds a guardFixture whose policy-1 is a
// customer policy with unprivileged statements and group-1 holds no member
// and no policy, so the attach and detach guard checks pass through to the
// write.
func unprivilegedGroupAttachFixture() guardFixture {
	g := unprivilegedGuardFixture()
	g.groups = map[string]Group{"group-1": {ID: "group-1"}}
	g.policies = map[string]Policy{
		"policy-1": {ID: "policy-1", Manager: "user", Statements: readOnlyStatements()},
	}
	return g
}

func TestAttachGroupPolicy(t *testing.T) {
	g := unprivilegedGroupAttachFixture()
	var attached atomic.Bool
	c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
		mux.HandleFunc("POST /policies-api/v1/policies/policy-1/groups/group-1", func(w http.ResponseWriter, r *http.Request) {
			attached.Store(true)
			w.WriteHeader(http.StatusNoContent)
		})
	})
	if _, err := c.AttachGroupPolicy(context.Background(), &AttachGroupPolicyInput{PolicyID: "policy-1", GroupID: "group-1"}); err != nil {
		t.Fatalf("AttachGroupPolicy() error = %v", err)
	}
	if !attached.Load() {
		t.Fatal("attach request was never sent")
	}
}

func TestDetachGroupPolicy(t *testing.T) {
	g := unprivilegedGroupAttachFixture()
	var detached atomic.Bool
	c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
		mux.HandleFunc("DELETE /policies-api/v1/policies/policy-1/groups/group-1", func(w http.ResponseWriter, r *http.Request) {
			detached.Store(true)
			w.WriteHeader(http.StatusNoContent)
		})
	})
	if _, err := c.DetachGroupPolicy(context.Background(), &DetachGroupPolicyInput{PolicyID: "policy-1", GroupID: "group-1"}); err != nil {
		t.Fatalf("DetachGroupPolicy() error = %v", err)
	}
	if !detached.Load() {
		t.Fatal("detach request was never sent")
	}
}

func TestAttachGroupPolicyRefusesPrivilegedPolicy(t *testing.T) {
	g := unprivilegedGroupAttachFixture()
	g.policies["policy-1"] = Policy{ID: "policy-1", Manager: "user", Statements: privilegedStatements()}
	c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
		mux.HandleFunc("POST /policies-api/v1/policies/policy-1/groups/group-1", func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("no write request expected")
		})
	})
	_, err := c.AttachGroupPolicy(context.Background(), &AttachGroupPolicyInput{PolicyID: "policy-1", GroupID: "group-1"})
	if !errors.Is(err, ErrPrivilegedChange) {
		t.Fatalf("AttachGroupPolicy() err = %v, want ErrPrivilegedChange", err)
	}
}

// TestDetachGroupPolicyRefusesPrivilegedPolicy checks that the same refusal
// applies to detach: the design refuses a privileged policy either way.
func TestDetachGroupPolicyRefusesPrivilegedPolicy(t *testing.T) {
	g := unprivilegedGroupAttachFixture()
	g.policies["policy-1"] = Policy{ID: "policy-1", Manager: "user", Statements: privilegedStatements()}
	c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
		mux.HandleFunc("DELETE /policies-api/v1/policies/policy-1/groups/group-1", func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("no write request expected")
		})
	})
	_, err := c.DetachGroupPolicy(context.Background(), &DetachGroupPolicyInput{PolicyID: "policy-1", GroupID: "group-1"})
	if !errors.Is(err, ErrPrivilegedChange) {
		t.Fatalf("DetachGroupPolicy() err = %v, want ErrPrivilegedChange", err)
	}
}

// TestAttachGroupPolicyRefusesIdpModeGroup checks the case an adversarial
// review raised: an idp group whose own attached policy denies rather than
// grants would not, by itself, make the group privileged, so without a
// dedicated mode check the attach could slip through unrefused. The design
// excludes idp groups entirely, so guardGetGroupAttachments refuses any group
// whose mode is not exactly "iam" before it ever looks at that policy or the
// group's members.
func TestAttachGroupPolicyRefusesIdpModeGroup(t *testing.T) {
	g := unprivilegedGroupAttachFixture()
	g.policies["policy-deny"] = Policy{ID: "policy-deny", Manager: "user", Statements: []Statement{
		{Effect: "deny", Actions: []string{"iam:*"}, Resources: []string{"*"}},
	}}
	g.groupHandler = func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"id":"group-1","mode":"idp","iamUsers":[],"policies":["policy-deny"]}`))
	}
	c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
		mux.HandleFunc("POST /policies-api/v1/policies/policy-1/groups/group-1", func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("no write request expected")
		})
	})
	if _, err := c.AttachGroupPolicy(context.Background(), &AttachGroupPolicyInput{PolicyID: "policy-1", GroupID: "group-1"}); err == nil {
		t.Fatal("AttachGroupPolicy() error = nil, want a refusal for an idp-mode group")
	}
}

// TestAttachGroupPolicyRefusesProtectedGroupByPrivilege checks that a group
// which already holds a privileged policy refuses attaching another one.
func TestAttachGroupPolicyRefusesProtectedGroupByPrivilege(t *testing.T) {
	g := unprivilegedGroupAttachFixture()
	g.groups["group-1"] = Group{ID: "group-1", PolicyIDs: []string{"policy-priv"}}
	g.policies["policy-priv"] = Policy{ID: "policy-priv", Manager: "user", Statements: privilegedStatements()}
	c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
		mux.HandleFunc("POST /policies-api/v1/policies/policy-1/groups/group-1", func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("no write request expected")
		})
	})
	_, err := c.AttachGroupPolicy(context.Background(), &AttachGroupPolicyInput{PolicyID: "policy-1", GroupID: "group-1"})
	if !errors.Is(err, ErrPrivilegedChange) {
		t.Fatalf("AttachGroupPolicy() err = %v, want ErrPrivilegedChange", err)
	}
}

// TestAttachGroupPolicyRefusesProtectedGroupByMember checks that a group
// with no privileged policy of its own, but a member who holds one
// directly, still refuses the attach: "a group protected only through a
// member."
func TestAttachGroupPolicyRefusesProtectedGroupByMember(t *testing.T) {
	g := unprivilegedGroupAttachFixture()
	g.groups["group-1"] = Group{ID: "group-1", UserIDs: []string{"user-m"}}
	g.userPolicyIDs = map[string][]string{"user-m": {"policy-priv"}}
	g.policies["policy-priv"] = Policy{ID: "policy-priv", Manager: "user", Statements: privilegedStatements()}
	c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
		mux.HandleFunc("POST /policies-api/v1/policies/policy-1/groups/group-1", func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("no write request expected")
		})
	})
	_, err := c.AttachGroupPolicy(context.Background(), &AttachGroupPolicyInput{PolicyID: "policy-1", GroupID: "group-1"})
	if !errors.Is(err, ErrPrivilegedChange) {
		t.Fatalf("AttachGroupPolicy() err = %v, want ErrPrivilegedChange", err)
	}
}

// TestAttachGroupPolicyRefusesCallerGroupMembership checks that attaching a
// policy to a group the caller belongs to refuses as a self-change: the
// group is protected because the caller is one of its members, so attaching
// a policy to it would change the caller's own rights.
func TestAttachGroupPolicyRefusesCallerGroupMembership(t *testing.T) {
	g := unprivilegedGroupAttachFixture()
	g.groups["group-1"] = Group{ID: "group-1", UserIDs: []string{"user-1"}}
	c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
		mux.HandleFunc("POST /policies-api/v1/policies/policy-1/groups/group-1", func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("no write request expected")
		})
	})
	_, err := c.AttachGroupPolicy(context.Background(), &AttachGroupPolicyInput{PolicyID: "policy-1", GroupID: "group-1"})
	if !errors.Is(err, ErrSelfChange) {
		t.Fatalf("AttachGroupPolicy() err = %v, want ErrSelfChange", err)
	}
}

// TestAttachGroupPolicySelfWinsOverPrivilege checks that a group the caller
// belongs to, which is separately protected by an already-attached
// privileged policy, still refuses as ErrSelfChange rather than
// ErrPrivilegedChange.
func TestAttachGroupPolicySelfWinsOverPrivilege(t *testing.T) {
	g := unprivilegedGroupAttachFixture()
	g.groups["group-1"] = Group{ID: "group-1", UserIDs: []string{"user-1"}, PolicyIDs: []string{"policy-priv"}}
	g.policies["policy-priv"] = Policy{ID: "policy-priv", Manager: "user", Statements: privilegedStatements()}
	c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
		mux.HandleFunc("POST /policies-api/v1/policies/policy-1/groups/group-1", func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("no write request expected")
		})
	})
	_, err := c.AttachGroupPolicy(context.Background(), &AttachGroupPolicyInput{PolicyID: "policy-1", GroupID: "group-1"})
	if !errors.Is(err, ErrSelfChange) {
		t.Fatalf("AttachGroupPolicy() err = %v, want ErrSelfChange", err)
	}
	if errors.Is(err, ErrPrivilegedChange) {
		t.Fatal("err also matches ErrPrivilegedChange; ErrSelfChange must win alone")
	}
}

// TestDetachGroupPolicyRefusesProtectedGroup checks that detaching from a
// group that already holds a different privileged policy refuses the same
// way an attach does: the design refuses either direction on a protected
// group, not only a privileged policy being attached or detached itself.
func TestDetachGroupPolicyRefusesProtectedGroup(t *testing.T) {
	g := unprivilegedGroupAttachFixture()
	g.groups["group-1"] = Group{ID: "group-1", PolicyIDs: []string{"policy-priv"}}
	g.policies["policy-priv"] = Policy{ID: "policy-priv", Manager: "user", Statements: privilegedStatements()}
	c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
		mux.HandleFunc("DELETE /policies-api/v1/policies/policy-1/groups/group-1", func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("no write request expected")
		})
	})
	_, err := c.DetachGroupPolicy(context.Background(), &DetachGroupPolicyInput{PolicyID: "policy-1", GroupID: "group-1"})
	if !errors.Is(err, ErrPrivilegedChange) {
		t.Fatalf("DetachGroupPolicy() err = %v, want ErrPrivilegedChange", err)
	}
}

// TestAttachGroupPolicyRefusesWhenGetPolicyFails checks that a guard-read
// failure on the policy itself refuses the attach with no request sent.
func TestAttachGroupPolicyRefusesWhenGetPolicyFails(t *testing.T) {
	g := unprivilegedGroupAttachFixture()
	g.policyHandler = func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}
	c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
		mux.HandleFunc("POST /policies-api/v1/policies/policy-1/groups/group-1", func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("no write request expected")
		})
	})
	if _, err := c.AttachGroupPolicy(context.Background(), &AttachGroupPolicyInput{PolicyID: "policy-1", GroupID: "group-1"}); err == nil {
		t.Fatal("AttachGroupPolicy() error = nil, want a guard-read failure")
	}
}

// TestAttachGroupPolicyRefusesWhenGroupAttachmentsFail checks that a failure
// of the target group's own attachments read refuses the attach with
// nothing sent.
func TestAttachGroupPolicyRefusesWhenGroupAttachmentsFail(t *testing.T) {
	g := unprivilegedGroupAttachFixture()
	g.groupHandler = func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}
	c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
		mux.HandleFunc("POST /policies-api/v1/policies/policy-1/groups/group-1", func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("no write request expected")
		})
	})
	if _, err := c.AttachGroupPolicy(context.Background(), &AttachGroupPolicyInput{PolicyID: "policy-1", GroupID: "group-1"}); err == nil {
		t.Fatal("AttachGroupPolicy() error = nil, want a guard-read failure")
	}
}

func TestAttachGroupPolicyWriteStatuses(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusForbidden, http.StatusNotFound, http.StatusConflict, http.StatusInternalServerError} {
		g := unprivilegedGroupAttachFixture()
		c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
			mux.HandleFunc("POST /policies-api/v1/policies/policy-1/groups/group-1", func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(status)
			})
		})
		if _, err := c.AttachGroupPolicy(context.Background(), &AttachGroupPolicyInput{PolicyID: "policy-1", GroupID: "group-1"}); err == nil {
			t.Fatalf("status %d: AttachGroupPolicy() error = nil, want an error", status)
		}
	}
}

func TestDetachGroupPolicyWriteStatuses(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusForbidden, http.StatusNotFound, http.StatusConflict, http.StatusInternalServerError} {
		g := unprivilegedGroupAttachFixture()
		c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
			mux.HandleFunc("DELETE /policies-api/v1/policies/policy-1/groups/group-1", func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(status)
			})
		})
		if _, err := c.DetachGroupPolicy(context.Background(), &DetachGroupPolicyInput{PolicyID: "policy-1", GroupID: "group-1"}); err == nil {
			t.Fatalf("status %d: DetachGroupPolicy() error = nil, want an error", status)
		}
	}
}

// TestAttachGroupPolicyNoRetryOn502 checks that the attach, a plain POST, is
// not retried after a 502: only a 429 or a failed dial is safe to retry for
// a non-idempotent request (ADR 0002 rule 2).
func TestAttachGroupPolicyNoRetryOn502(t *testing.T) {
	g := unprivilegedGroupAttachFixture()
	var calls int32
	handler := g.mux(t, func(mux *http.ServeMux) {
		mux.HandleFunc("POST /policies-api/v1/policies/policy-1/groups/group-1", func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt32(&calls, 1)
			w.WriteHeader(http.StatusBadGateway)
		})
	})
	server := httptest.NewServer(handler)
	defer server.Close()
	tc := transport.New(transport.Config{HTTPClient: server.Client(), RetryCount: 3, RetryInterval: time.Millisecond})
	cfg := core.NewTestConfig("hcm-3", "project-1", endpoints.Set{Region: "hcm-3", Dashboard: server.URL + "/", IAM: server.URL + "/"}, tc)
	c := New(cfg)

	_, err := c.AttachGroupPolicy(context.Background(), &AttachGroupPolicyInput{PolicyID: "policy-1", GroupID: "group-1"})
	if err == nil {
		t.Fatal("AttachGroupPolicy() error = nil, want an error")
	}
	if calls != 1 {
		t.Fatalf("server received %d request(s), want 1 (no retry after 502)", calls)
	}
}

func TestGroupPolicyAttachErrorTextNamesNoDetail(t *testing.T) {
	g := unprivilegedGuardFixture()
	g.groups = map[string]Group{"group-1": {ID: "group-1"}}
	g.policies = map[string]Policy{
		"policy-1": {ID: "policy-1", Name: "TopSecretPolicyName", Manager: "user", Statements: privilegedStatements()},
	}
	c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
		mux.HandleFunc("POST /policies-api/v1/policies/policy-1/groups/group-1", func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("no write request expected")
		})
	})
	_, err := c.AttachGroupPolicy(context.Background(), &AttachGroupPolicyInput{PolicyID: "policy-1", GroupID: "group-1"})
	if err == nil {
		t.Fatal("AttachGroupPolicy() error = nil, want ErrPrivilegedChange")
	}
	if strings.Contains(err.Error(), "TopSecretPolicyName") {
		t.Fatalf("error names the policy: %v", err)
	}
}
