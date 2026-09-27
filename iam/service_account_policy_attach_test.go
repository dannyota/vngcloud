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

// unprivilegedAttachFixture builds a guardFixture whose policy-1 is a
// customer policy with unprivileged statements and sa-1 holds no privileged
// policy, so the attach and detach guard checks pass through to the write.
func unprivilegedAttachFixture() guardFixture {
	g := unprivilegedGuardFixture()
	g.policies = map[string]Policy{
		"policy-1": {ID: "policy-1", Manager: "user", Statements: readOnlyStatements()},
	}
	return g
}

func TestAttachServiceAccountPolicy(t *testing.T) {
	g := unprivilegedAttachFixture()
	var attached atomic.Bool
	c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
		mux.HandleFunc("POST /policies-api/v1/policies/policy-1/service-accounts/sa-1", func(w http.ResponseWriter, r *http.Request) {
			attached.Store(true)
			w.WriteHeader(http.StatusNoContent)
		})
	})
	if _, err := c.AttachServiceAccountPolicy(context.Background(), &AttachServiceAccountPolicyInput{PolicyID: "policy-1", ServiceAccountID: "sa-1"}); err != nil {
		t.Fatalf("AttachServiceAccountPolicy() error = %v", err)
	}
	if !attached.Load() {
		t.Fatal("attach request was never sent")
	}
}

func TestDetachServiceAccountPolicy(t *testing.T) {
	g := unprivilegedAttachFixture()
	var detached atomic.Bool
	c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
		mux.HandleFunc("DELETE /policies-api/v1/policies/policy-1/service-accounts/sa-1", func(w http.ResponseWriter, r *http.Request) {
			detached.Store(true)
			w.WriteHeader(http.StatusNoContent)
		})
	})
	if _, err := c.DetachServiceAccountPolicy(context.Background(), &DetachServiceAccountPolicyInput{PolicyID: "policy-1", ServiceAccountID: "sa-1"}); err != nil {
		t.Fatalf("DetachServiceAccountPolicy() error = %v", err)
	}
	if !detached.Load() {
		t.Fatal("detach request was never sent")
	}
}

// TestAttachServiceAccountPolicyRefusesPrivilegedPolicy checks that
// attaching a privileged policy refuses with no request sent.
func TestAttachServiceAccountPolicyRefusesPrivilegedPolicy(t *testing.T) {
	g := unprivilegedGuardFixture()
	g.policies = map[string]Policy{
		"policy-1": {ID: "policy-1", Manager: "user", Statements: privilegedStatements()},
	}
	c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
		mux.HandleFunc("POST /policies-api/v1/policies/policy-1/service-accounts/sa-1", func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("no write request expected")
		})
	})
	_, err := c.AttachServiceAccountPolicy(context.Background(), &AttachServiceAccountPolicyInput{PolicyID: "policy-1", ServiceAccountID: "sa-1"})
	if !errors.Is(err, ErrPrivilegedChange) {
		t.Fatalf("AttachServiceAccountPolicy() err = %v, want ErrPrivilegedChange", err)
	}
}

// TestDetachServiceAccountPolicyRefusesPrivilegedPolicy checks that the same
// refusal applies to detach: the design refuses a privileged policy either
// way.
func TestDetachServiceAccountPolicyRefusesPrivilegedPolicy(t *testing.T) {
	g := unprivilegedGuardFixture()
	g.policies = map[string]Policy{
		"policy-1": {ID: "policy-1", Manager: "user", Statements: privilegedStatements()},
	}
	c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
		mux.HandleFunc("DELETE /policies-api/v1/policies/policy-1/service-accounts/sa-1", func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("no write request expected")
		})
	})
	_, err := c.DetachServiceAccountPolicy(context.Background(), &DetachServiceAccountPolicyInput{PolicyID: "policy-1", ServiceAccountID: "sa-1"})
	if !errors.Is(err, ErrPrivilegedChange) {
		t.Fatalf("DetachServiceAccountPolicy() err = %v, want ErrPrivilegedChange", err)
	}
}

// TestAttachServiceAccountPolicyRefusesProtectedTarget checks that attaching
// to a service account which already holds a privileged policy refuses with
// no request sent.
func TestAttachServiceAccountPolicyRefusesProtectedTarget(t *testing.T) {
	g := unprivilegedAttachFixture()
	g.attachedPolicyIDs = []string{"policy-priv"}
	g.policies["policy-priv"] = Policy{ID: "policy-priv", Manager: "user", Statements: privilegedStatements()}
	c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
		mux.HandleFunc("POST /policies-api/v1/policies/policy-1/service-accounts/sa-1", func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("no write request expected")
		})
	})
	_, err := c.AttachServiceAccountPolicy(context.Background(), &AttachServiceAccountPolicyInput{PolicyID: "policy-1", ServiceAccountID: "sa-1"})
	if !errors.Is(err, ErrPrivilegedChange) {
		t.Fatalf("AttachServiceAccountPolicy() err = %v, want ErrPrivilegedChange", err)
	}
}

// TestAttachServiceAccountPolicyRefusesServiceAccountCaller checks that a
// service-account caller refuses to attach to any service account, per the
// design's open question on the caller identity form.
func TestAttachServiceAccountPolicyRefusesServiceAccountCaller(t *testing.T) {
	g := unprivilegedAttachFixture()
	g.caller = userInfoResponse{UserID: "sa-1", UserType: callerTypeUserSA}
	c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
		mux.HandleFunc("POST /policies-api/v1/policies/policy-1/service-accounts/sa-1", func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("no write request expected")
		})
	})
	_, err := c.AttachServiceAccountPolicy(context.Background(), &AttachServiceAccountPolicyInput{PolicyID: "policy-1", ServiceAccountID: "sa-1"})
	if !errors.Is(err, ErrSelfChange) {
		t.Fatalf("AttachServiceAccountPolicy() err = %v, want ErrSelfChange", err)
	}
}

// TestDetachServiceAccountPolicyRefusesProtectedTarget is
// TestAttachServiceAccountPolicyRefusesProtectedTarget for detach: the
// design refuses a protected target either way.
func TestDetachServiceAccountPolicyRefusesProtectedTarget(t *testing.T) {
	g := unprivilegedAttachFixture()
	g.attachedPolicyIDs = []string{"policy-priv"}
	g.policies["policy-priv"] = Policy{ID: "policy-priv", Manager: "user", Statements: privilegedStatements()}
	c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
		mux.HandleFunc("DELETE /policies-api/v1/policies/policy-1/service-accounts/sa-1", func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("no write request expected")
		})
	})
	_, err := c.DetachServiceAccountPolicy(context.Background(), &DetachServiceAccountPolicyInput{PolicyID: "policy-1", ServiceAccountID: "sa-1"})
	if !errors.Is(err, ErrPrivilegedChange) {
		t.Fatalf("DetachServiceAccountPolicy() err = %v, want ErrPrivilegedChange", err)
	}
}

// TestDetachServiceAccountPolicyRefusesServiceAccountCaller is
// TestAttachServiceAccountPolicyRefusesServiceAccountCaller for detach.
func TestDetachServiceAccountPolicyRefusesServiceAccountCaller(t *testing.T) {
	g := unprivilegedAttachFixture()
	g.caller = userInfoResponse{UserID: "sa-1", UserType: callerTypeUserSA}
	c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
		mux.HandleFunc("DELETE /policies-api/v1/policies/policy-1/service-accounts/sa-1", func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("no write request expected")
		})
	})
	_, err := c.DetachServiceAccountPolicy(context.Background(), &DetachServiceAccountPolicyInput{PolicyID: "policy-1", ServiceAccountID: "sa-1"})
	if !errors.Is(err, ErrSelfChange) {
		t.Fatalf("DetachServiceAccountPolicy() err = %v, want ErrSelfChange", err)
	}
}

func TestAttachServiceAccountPolicyWriteStatuses(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusForbidden, http.StatusNotFound, http.StatusConflict, http.StatusInternalServerError} {
		g := unprivilegedAttachFixture()
		c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
			mux.HandleFunc("POST /policies-api/v1/policies/policy-1/service-accounts/sa-1", func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(status)
			})
		})
		if _, err := c.AttachServiceAccountPolicy(context.Background(), &AttachServiceAccountPolicyInput{PolicyID: "policy-1", ServiceAccountID: "sa-1"}); err == nil {
			t.Fatalf("status %d: AttachServiceAccountPolicy() error = nil, want an error", status)
		}
	}
}

func TestDetachServiceAccountPolicyWriteStatuses(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusForbidden, http.StatusNotFound, http.StatusConflict, http.StatusInternalServerError} {
		g := unprivilegedAttachFixture()
		c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
			mux.HandleFunc("DELETE /policies-api/v1/policies/policy-1/service-accounts/sa-1", func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(status)
			})
		})
		if _, err := c.DetachServiceAccountPolicy(context.Background(), &DetachServiceAccountPolicyInput{PolicyID: "policy-1", ServiceAccountID: "sa-1"}); err == nil {
			t.Fatalf("status %d: DetachServiceAccountPolicy() error = nil, want an error", status)
		}
	}
}

// TestAttachServiceAccountPolicyNoRetryOn502 checks that the attach, a plain
// POST, is not retried after a 502: only a 429 or a failed dial is safe to
// retry for a non-idempotent request (ADR 0002 rule 2).
func TestAttachServiceAccountPolicyNoRetryOn502(t *testing.T) {
	g := unprivilegedAttachFixture()
	var calls int32
	handler := g.mux(t, func(mux *http.ServeMux) {
		mux.HandleFunc("POST /policies-api/v1/policies/policy-1/service-accounts/sa-1", func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt32(&calls, 1)
			w.WriteHeader(http.StatusBadGateway)
		})
	})
	server := httptest.NewServer(handler)
	defer server.Close()
	tc := transport.New(transport.Config{HTTPClient: server.Client(), RetryCount: 3, RetryInterval: time.Millisecond})
	cfg := core.NewTestConfig("hcm-3", "project-1", endpoints.Set{Region: "hcm-3", Dashboard: server.URL + "/", IAM: server.URL + "/"}, tc)
	c := New(cfg)

	_, err := c.AttachServiceAccountPolicy(context.Background(), &AttachServiceAccountPolicyInput{PolicyID: "policy-1", ServiceAccountID: "sa-1"})
	if err == nil {
		t.Fatal("AttachServiceAccountPolicy() error = nil, want an error")
	}
	if calls != 1 {
		t.Fatalf("server received %d request(s), want 1 (no retry after 502)", calls)
	}
}

// Path ID rejection for PolicyID and ServiceAccountID on Attach and Detach
// is covered by TestIAMPathIDRejection in reads_test.go, alongside every
// other IAM call taking a path ID.

func TestServiceAccountPolicyAttachErrorTextNamesNoDetail(t *testing.T) {
	g := unprivilegedGuardFixture()
	g.policies = map[string]Policy{
		"policy-1": {ID: "policy-1", Name: "TopSecretPolicyName", Manager: "user", Statements: privilegedStatements()},
	}
	c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
		mux.HandleFunc("POST /policies-api/v1/policies/policy-1/service-accounts/sa-1", func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("no write request expected")
		})
	})
	_, err := c.AttachServiceAccountPolicy(context.Background(), &AttachServiceAccountPolicyInput{PolicyID: "policy-1", ServiceAccountID: "sa-1"})
	if err == nil {
		t.Fatal("AttachServiceAccountPolicy() error = nil, want ErrPrivilegedChange")
	}
	if strings.Contains(err.Error(), "TopSecretPolicyName") {
		t.Fatalf("error names the policy: %v", err)
	}
}
