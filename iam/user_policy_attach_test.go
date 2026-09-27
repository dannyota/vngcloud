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

// unprivilegedUserAttachFixture builds a guardFixture whose policy-1 is a
// customer policy with unprivileged statements and user-x holds no
// privileged policy, so the attach and detach guard checks pass through to
// the write.
func unprivilegedUserAttachFixture() guardFixture {
	g := unprivilegedGuardFixture()
	g.policies = map[string]Policy{
		"policy-1": {ID: "policy-1", Manager: "user", Statements: readOnlyStatements()},
	}
	return g
}

func TestAttachUserPolicy(t *testing.T) {
	g := unprivilegedUserAttachFixture()
	var attached atomic.Bool
	c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
		mux.HandleFunc("POST /policies-api/v1/policies/policy-1/iam-users/user-x", func(w http.ResponseWriter, r *http.Request) {
			attached.Store(true)
			w.WriteHeader(http.StatusNoContent)
		})
	})
	if _, err := c.AttachUserPolicy(context.Background(), &AttachUserPolicyInput{PolicyID: "policy-1", UserID: "user-x"}); err != nil {
		t.Fatalf("AttachUserPolicy() error = %v", err)
	}
	if !attached.Load() {
		t.Fatal("attach request was never sent")
	}
}

func TestDetachUserPolicy(t *testing.T) {
	g := unprivilegedUserAttachFixture()
	var detached atomic.Bool
	c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
		mux.HandleFunc("DELETE /policies-api/v1/policies/policy-1/iam-users/user-x", func(w http.ResponseWriter, r *http.Request) {
			detached.Store(true)
			w.WriteHeader(http.StatusNoContent)
		})
	})
	if _, err := c.DetachUserPolicy(context.Background(), &DetachUserPolicyInput{PolicyID: "policy-1", UserID: "user-x"}); err != nil {
		t.Fatalf("DetachUserPolicy() error = %v", err)
	}
	if !detached.Load() {
		t.Fatal("detach request was never sent")
	}
}

// TestAttachUserPolicyRefusesCaller checks that attaching a policy to the
// caller's own user ID refuses as a self-change, with no request sent.
func TestAttachUserPolicyRefusesCaller(t *testing.T) {
	g := unprivilegedUserAttachFixture()
	c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
		mux.HandleFunc("POST /policies-api/v1/policies/policy-1/iam-users/user-1", func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("no write request expected")
		})
	})
	_, err := c.AttachUserPolicy(context.Background(), &AttachUserPolicyInput{PolicyID: "policy-1", UserID: "user-1"})
	if !errors.Is(err, ErrSelfChange) {
		t.Fatalf("AttachUserPolicy() err = %v, want ErrSelfChange", err)
	}
}

// TestDetachUserPolicyRefusesCaller is TestAttachUserPolicyRefusesCaller for
// detach.
func TestDetachUserPolicyRefusesCaller(t *testing.T) {
	g := unprivilegedUserAttachFixture()
	c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
		mux.HandleFunc("DELETE /policies-api/v1/policies/policy-1/iam-users/user-1", func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("no write request expected")
		})
	})
	_, err := c.DetachUserPolicy(context.Background(), &DetachUserPolicyInput{PolicyID: "policy-1", UserID: "user-1"})
	if !errors.Is(err, ErrSelfChange) {
		t.Fatalf("DetachUserPolicy() err = %v, want ErrSelfChange", err)
	}
}

// TestAttachUserPolicyRefusesCallerCaseInsensitive checks that the caller's
// own id is matched against a target user id case-insensitively, since
// nothing guarantees the server returns userinfo's id and a user's own id in
// the same case.
func TestAttachUserPolicyRefusesCallerCaseInsensitive(t *testing.T) {
	g := unprivilegedUserAttachFixture()
	g.caller = userInfoResponse{UserID: "USER-1", UserType: callerTypeIAMUser}
	c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
		mux.HandleFunc("POST /policies-api/v1/policies/policy-1/iam-users/user-1", func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("no write request expected")
		})
	})
	_, err := c.AttachUserPolicy(context.Background(), &AttachUserPolicyInput{PolicyID: "policy-1", UserID: "user-1"})
	if !errors.Is(err, ErrSelfChange) {
		t.Fatalf("AttachUserPolicy() err = %v, want ErrSelfChange", err)
	}
}

func TestAttachUserPolicyRefusesPrivilegedPolicy(t *testing.T) {
	g := unprivilegedUserAttachFixture()
	g.policies["policy-1"] = Policy{ID: "policy-1", Manager: "user", Statements: privilegedStatements()}
	c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
		mux.HandleFunc("POST /policies-api/v1/policies/policy-1/iam-users/user-x", func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("no write request expected")
		})
	})
	_, err := c.AttachUserPolicy(context.Background(), &AttachUserPolicyInput{PolicyID: "policy-1", UserID: "user-x"})
	if !errors.Is(err, ErrPrivilegedChange) {
		t.Fatalf("AttachUserPolicy() err = %v, want ErrPrivilegedChange", err)
	}
}

// TestDetachUserPolicyRefusesPrivilegedPolicy checks that the same refusal
// applies to detach: the design refuses a privileged policy either way.
func TestDetachUserPolicyRefusesPrivilegedPolicy(t *testing.T) {
	g := unprivilegedUserAttachFixture()
	g.policies["policy-1"] = Policy{ID: "policy-1", Manager: "user", Statements: privilegedStatements()}
	c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
		mux.HandleFunc("DELETE /policies-api/v1/policies/policy-1/iam-users/user-x", func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("no write request expected")
		})
	})
	_, err := c.DetachUserPolicy(context.Background(), &DetachUserPolicyInput{PolicyID: "policy-1", UserID: "user-x"})
	if !errors.Is(err, ErrPrivilegedChange) {
		t.Fatalf("DetachUserPolicy() err = %v, want ErrPrivilegedChange", err)
	}
}

// TestAttachUserPolicyRefusesProtectedUserDirect checks that a user who
// already holds a privileged policy directly refuses attaching another one.
func TestAttachUserPolicyRefusesProtectedUserDirect(t *testing.T) {
	g := unprivilegedUserAttachFixture()
	g.userPolicyIDs = map[string][]string{"user-x": {"policy-priv"}}
	g.policies["policy-priv"] = Policy{ID: "policy-priv", Manager: "user", Statements: privilegedStatements()}
	c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
		mux.HandleFunc("POST /policies-api/v1/policies/policy-1/iam-users/user-x", func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("no write request expected")
		})
	})
	_, err := c.AttachUserPolicy(context.Background(), &AttachUserPolicyInput{PolicyID: "policy-1", UserID: "user-x"})
	if !errors.Is(err, ErrPrivilegedChange) {
		t.Fatalf("AttachUserPolicy() err = %v, want ErrPrivilegedChange", err)
	}
}

// TestAttachUserPolicyRefusesProtectedUserByGroup checks that a user with no
// privileged policy directly attached, but who belongs to a group that has
// one, still refuses the attach: "a user protected only through a group."
func TestAttachUserPolicyRefusesProtectedUserByGroup(t *testing.T) {
	g := unprivilegedUserAttachFixture()
	g.userGroups = map[string][]Group{
		"user-x": {{ID: "group-y", PolicyIDs: []string{"policy-priv"}}},
	}
	g.policies["policy-priv"] = Policy{ID: "policy-priv", Manager: "user", Statements: privilegedStatements()}
	c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
		mux.HandleFunc("POST /policies-api/v1/policies/policy-1/iam-users/user-x", func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("no write request expected")
		})
	})
	_, err := c.AttachUserPolicy(context.Background(), &AttachUserPolicyInput{PolicyID: "policy-1", UserID: "user-x"})
	if !errors.Is(err, ErrPrivilegedChange) {
		t.Fatalf("AttachUserPolicy() err = %v, want ErrPrivilegedChange", err)
	}
}

// TestDetachUserPolicyRefusesProtectedUser checks that detaching from a user
// who already holds a different privileged policy directly refuses the same
// way an attach does: the design refuses either direction on a protected
// user, not only a privileged policy being attached or detached itself.
func TestDetachUserPolicyRefusesProtectedUser(t *testing.T) {
	g := unprivilegedUserAttachFixture()
	g.userPolicyIDs = map[string][]string{"user-x": {"policy-priv"}}
	g.policies["policy-priv"] = Policy{ID: "policy-priv", Manager: "user", Statements: privilegedStatements()}
	c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
		mux.HandleFunc("DELETE /policies-api/v1/policies/policy-1/iam-users/user-x", func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("no write request expected")
		})
	})
	_, err := c.DetachUserPolicy(context.Background(), &DetachUserPolicyInput{PolicyID: "policy-1", UserID: "user-x"})
	if !errors.Is(err, ErrPrivilegedChange) {
		t.Fatalf("DetachUserPolicy() err = %v, want ErrPrivilegedChange", err)
	}
}

// TestAttachUserPolicyRefusesWhenGetPolicyFails checks that a guard-read
// failure on the policy itself refuses the attach with no request sent.
func TestAttachUserPolicyRefusesWhenGetPolicyFails(t *testing.T) {
	g := unprivilegedGuardFixture()
	g.policyHandler = func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}
	c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
		mux.HandleFunc("POST /policies-api/v1/policies/policy-1/iam-users/user-x", func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("no write request expected")
		})
	})
	if _, err := c.AttachUserPolicy(context.Background(), &AttachUserPolicyInput{PolicyID: "policy-1", UserID: "user-x"}); err == nil {
		t.Fatal("AttachUserPolicy() error = nil, want a guard-read failure")
	}
}

// TestAttachUserPolicyRefusesWhenUserPolicySummariesFail checks that a
// failure of the target user's own direct-policy read refuses the attach
// with nothing sent.
func TestAttachUserPolicyRefusesWhenUserPolicySummariesFail(t *testing.T) {
	g := unprivilegedUserAttachFixture()
	g.userPoliciesHandler = func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}
	c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
		mux.HandleFunc("POST /policies-api/v1/policies/policy-1/iam-users/user-x", func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("no write request expected")
		})
	})
	if _, err := c.AttachUserPolicy(context.Background(), &AttachUserPolicyInput{PolicyID: "policy-1", UserID: "user-x"}); err == nil {
		t.Fatal("AttachUserPolicy() error = nil, want a guard-read failure")
	}
}

func TestAttachUserPolicyWriteStatuses(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusForbidden, http.StatusNotFound, http.StatusConflict, http.StatusInternalServerError} {
		g := unprivilegedUserAttachFixture()
		c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
			mux.HandleFunc("POST /policies-api/v1/policies/policy-1/iam-users/user-x", func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(status)
			})
		})
		if _, err := c.AttachUserPolicy(context.Background(), &AttachUserPolicyInput{PolicyID: "policy-1", UserID: "user-x"}); err == nil {
			t.Fatalf("status %d: AttachUserPolicy() error = nil, want an error", status)
		}
	}
}

func TestDetachUserPolicyWriteStatuses(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusForbidden, http.StatusNotFound, http.StatusConflict, http.StatusInternalServerError} {
		g := unprivilegedUserAttachFixture()
		c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
			mux.HandleFunc("DELETE /policies-api/v1/policies/policy-1/iam-users/user-x", func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(status)
			})
		})
		if _, err := c.DetachUserPolicy(context.Background(), &DetachUserPolicyInput{PolicyID: "policy-1", UserID: "user-x"}); err == nil {
			t.Fatalf("status %d: DetachUserPolicy() error = nil, want an error", status)
		}
	}
}

// TestAttachUserPolicyNoRetryOn502 checks that the attach, a plain POST, is
// not retried after a 502: only a 429 or a failed dial is safe to retry for
// a non-idempotent request (ADR 0002 rule 2).
func TestAttachUserPolicyNoRetryOn502(t *testing.T) {
	g := unprivilegedUserAttachFixture()
	var calls int32
	handler := g.mux(t, func(mux *http.ServeMux) {
		mux.HandleFunc("POST /policies-api/v1/policies/policy-1/iam-users/user-x", func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt32(&calls, 1)
			w.WriteHeader(http.StatusBadGateway)
		})
	})
	server := httptest.NewServer(handler)
	defer server.Close()
	tc := transport.New(transport.Config{HTTPClient: server.Client(), RetryCount: 3, RetryInterval: time.Millisecond})
	cfg := core.NewTestConfig("hcm-3", "project-1", endpoints.Set{Region: "hcm-3", Dashboard: server.URL + "/", IAM: server.URL + "/"}, tc)
	c := New(cfg)

	_, err := c.AttachUserPolicy(context.Background(), &AttachUserPolicyInput{PolicyID: "policy-1", UserID: "user-x"})
	if err == nil {
		t.Fatal("AttachUserPolicy() error = nil, want an error")
	}
	if calls != 1 {
		t.Fatalf("server received %d request(s), want 1 (no retry after 502)", calls)
	}
}

func TestUserPolicyAttachErrorTextNamesNoDetail(t *testing.T) {
	g := unprivilegedGuardFixture()
	g.policies = map[string]Policy{
		"policy-1": {ID: "policy-1", Name: "TopSecretPolicyName", Manager: "user", Statements: privilegedStatements()},
	}
	c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
		mux.HandleFunc("POST /policies-api/v1/policies/policy-1/iam-users/user-x", func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("no write request expected")
		})
	})
	_, err := c.AttachUserPolicy(context.Background(), &AttachUserPolicyInput{PolicyID: "policy-1", UserID: "user-x"})
	if err == nil {
		t.Fatal("AttachUserPolicy() error = nil, want ErrPrivilegedChange")
	}
	if strings.Contains(err.Error(), "TopSecretPolicyName") {
		t.Fatalf("error names the policy: %v", err)
	}
}
