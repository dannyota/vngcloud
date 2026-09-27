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

// unprivilegedMembershipFixture builds a guardFixture with a group holding
// no member and no policy, so AddUserToGroup and RemoveUserFromGroup guard
// checks pass through to the write.
func unprivilegedMembershipFixture() guardFixture {
	g := unprivilegedGuardFixture()
	g.groups = map[string]Group{"group-1": {ID: "group-1"}}
	return g
}

func TestAddUserToGroup(t *testing.T) {
	g := unprivilegedMembershipFixture()
	var added atomic.Bool
	c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
		mux.HandleFunc("POST /policies-api/v1/groups/group-1/iam-users/user-x", func(w http.ResponseWriter, r *http.Request) {
			added.Store(true)
			w.WriteHeader(http.StatusNoContent)
		})
	})
	if _, err := c.AddUserToGroup(context.Background(), &AddUserToGroupInput{GroupID: "group-1", UserID: "user-x"}); err != nil {
		t.Fatalf("AddUserToGroup() error = %v", err)
	}
	if !added.Load() {
		t.Fatal("add request was never sent")
	}
}

func TestRemoveUserFromGroup(t *testing.T) {
	g := unprivilegedMembershipFixture()
	var removed atomic.Bool
	c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
		mux.HandleFunc("DELETE /policies-api/v1/groups/group-1/iam-users/user-x", func(w http.ResponseWriter, r *http.Request) {
			removed.Store(true)
			w.WriteHeader(http.StatusNoContent)
		})
	})
	if _, err := c.RemoveUserFromGroup(context.Background(), &RemoveUserFromGroupInput{GroupID: "group-1", UserID: "user-x"}); err != nil {
		t.Fatalf("RemoveUserFromGroup() error = %v", err)
	}
	if !removed.Load() {
		t.Fatal("remove request was never sent")
	}
}

// TestAddUserToGroupRefusesCallerAsUser checks that adding the caller itself
// refuses as a self-change, with no request sent.
func TestAddUserToGroupRefusesCallerAsUser(t *testing.T) {
	g := unprivilegedMembershipFixture()
	c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
		mux.HandleFunc("POST /policies-api/v1/groups/group-1/iam-users/user-1", func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("no write request expected")
		})
	})
	_, err := c.AddUserToGroup(context.Background(), &AddUserToGroupInput{GroupID: "group-1", UserID: "user-1"})
	if !errors.Is(err, ErrSelfChange) {
		t.Fatalf("AddUserToGroup() err = %v, want ErrSelfChange", err)
	}
}

// TestRemoveUserFromGroupRefusesCallerAsUser is
// TestAddUserToGroupRefusesCallerAsUser for remove.
func TestRemoveUserFromGroupRefusesCallerAsUser(t *testing.T) {
	g := unprivilegedMembershipFixture()
	c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
		mux.HandleFunc("DELETE /policies-api/v1/groups/group-1/iam-users/user-1", func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("no write request expected")
		})
	})
	_, err := c.RemoveUserFromGroup(context.Background(), &RemoveUserFromGroupInput{GroupID: "group-1", UserID: "user-1"})
	if !errors.Is(err, ErrSelfChange) {
		t.Fatalf("RemoveUserFromGroup() err = %v, want ErrSelfChange", err)
	}
}

// TestAddUserToGroupRefusesCallerGroupMembership checks that adding a
// different user to a group the caller already belongs to refuses as a
// self-change: the group is protected because the caller is one of its
// members.
func TestAddUserToGroupRefusesCallerGroupMembership(t *testing.T) {
	g := unprivilegedMembershipFixture()
	g.groups["group-1"] = Group{ID: "group-1", UserIDs: []string{"user-1"}}
	c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
		mux.HandleFunc("POST /policies-api/v1/groups/group-1/iam-users/user-x", func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("no write request expected")
		})
	})
	_, err := c.AddUserToGroup(context.Background(), &AddUserToGroupInput{GroupID: "group-1", UserID: "user-x"})
	if !errors.Is(err, ErrSelfChange) {
		t.Fatalf("AddUserToGroup() err = %v, want ErrSelfChange", err)
	}
}

// TestRemoveUserFromGroupRefusesCallerGroupMembership is
// TestAddUserToGroupRefusesCallerGroupMembership for remove: removing a
// different user from a group the caller already belongs to still refuses as
// a self-change.
func TestRemoveUserFromGroupRefusesCallerGroupMembership(t *testing.T) {
	g := unprivilegedMembershipFixture()
	g.groups["group-1"] = Group{ID: "group-1", UserIDs: []string{"user-1"}}
	c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
		mux.HandleFunc("DELETE /policies-api/v1/groups/group-1/iam-users/user-x", func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("no write request expected")
		})
	})
	_, err := c.RemoveUserFromGroup(context.Background(), &RemoveUserFromGroupInput{GroupID: "group-1", UserID: "user-x"})
	if !errors.Is(err, ErrSelfChange) {
		t.Fatalf("RemoveUserFromGroup() err = %v, want ErrSelfChange", err)
	}
}

// TestAddUserToGroupRefusesIdpModeGroup checks that groupIsProtected, reused
// by the membership guard, refuses an idp-mode target group the same way
// every other group-reading guard does.
func TestAddUserToGroupRefusesIdpModeGroup(t *testing.T) {
	g := unprivilegedMembershipFixture()
	g.groupHandler = func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"id":"group-1","mode":"idp","iamUsers":[],"policies":[]}`))
	}
	c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
		mux.HandleFunc("POST /policies-api/v1/groups/group-1/iam-users/user-x", func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("no write request expected")
		})
	})
	if _, err := c.AddUserToGroup(context.Background(), &AddUserToGroupInput{GroupID: "group-1", UserID: "user-x"}); err == nil {
		t.Fatal("AddUserToGroup() error = nil, want a refusal for an idp-mode group")
	}
}

// TestAddUserToGroupGroupIsProtectedSelfWinsOverOwnPrivilege checks that
// groupIsProtected itself, not only the outer combine in
// guardGroupMembershipWrite, folds a group's own privileged policy together
// with the caller's own membership into protectedBySelf: the target group
// holds both, and the add's own target user is someone else entirely, so the
// only source of self here is the caller's membership found inside
// groupIsProtected's own member loop.
func TestAddUserToGroupGroupIsProtectedSelfWinsOverOwnPrivilege(t *testing.T) {
	g := unprivilegedMembershipFixture()
	g.groups["group-1"] = Group{ID: "group-1", PolicyIDs: []string{"policy-priv"}, UserIDs: []string{"user-1"}}
	g.policies = map[string]Policy{
		"policy-priv": {ID: "policy-priv", Manager: "user", Statements: privilegedStatements()},
	}
	c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
		mux.HandleFunc("POST /policies-api/v1/groups/group-1/iam-users/user-x", func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("no write request expected")
		})
	})
	_, err := c.AddUserToGroup(context.Background(), &AddUserToGroupInput{GroupID: "group-1", UserID: "user-x"})
	if !errors.Is(err, ErrSelfChange) {
		t.Fatalf("AddUserToGroup() err = %v, want ErrSelfChange", err)
	}
	if errors.Is(err, ErrPrivilegedChange) {
		t.Fatal("err also matches ErrPrivilegedChange; ErrSelfChange must win alone")
	}
}

// TestAddUserToGroupRefusesGroupProtectedByPrivilegedPolicy checks that a
// group with a privileged policy attached refuses membership changes.
func TestAddUserToGroupRefusesGroupProtectedByPrivilegedPolicy(t *testing.T) {
	g := unprivilegedMembershipFixture()
	g.groups["group-1"] = Group{ID: "group-1", PolicyIDs: []string{"policy-priv"}}
	g.policies = map[string]Policy{
		"policy-priv": {ID: "policy-priv", Manager: "user", Statements: privilegedStatements()},
	}
	c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
		mux.HandleFunc("POST /policies-api/v1/groups/group-1/iam-users/user-x", func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("no write request expected")
		})
	})
	_, err := c.AddUserToGroup(context.Background(), &AddUserToGroupInput{GroupID: "group-1", UserID: "user-x"})
	if !errors.Is(err, ErrPrivilegedChange) {
		t.Fatalf("AddUserToGroup() err = %v, want ErrPrivilegedChange", err)
	}
}

// TestAddUserToGroupRefusesUserProtectedByGroup checks that the target user,
// unrelated to the target group, still refuses when that user is itself
// protected through a different group's privileged policy: "the user or the
// group is protected" covers the user's own status too.
func TestAddUserToGroupRefusesUserProtectedByGroup(t *testing.T) {
	g := unprivilegedMembershipFixture()
	g.userGroups = map[string][]Group{
		"user-x": {{ID: "group-other", PolicyIDs: []string{"policy-priv"}}},
	}
	g.policies = map[string]Policy{
		"policy-priv": {ID: "policy-priv", Manager: "user", Statements: privilegedStatements()},
	}
	c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
		mux.HandleFunc("POST /policies-api/v1/groups/group-1/iam-users/user-x", func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("no write request expected")
		})
	})
	_, err := c.AddUserToGroup(context.Background(), &AddUserToGroupInput{GroupID: "group-1", UserID: "user-x"})
	if !errors.Is(err, ErrPrivilegedChange) {
		t.Fatalf("AddUserToGroup() err = %v, want ErrPrivilegedChange", err)
	}
}

// TestAddUserToGroupSelfWinsOverPrivilege checks that adding the caller to a
// group that is separately protected by its own privileged policy still
// refuses as ErrSelfChange, not ErrPrivilegedChange: self wins even when
// both would apply.
func TestAddUserToGroupSelfWinsOverPrivilege(t *testing.T) {
	g := unprivilegedMembershipFixture()
	g.groups["group-1"] = Group{ID: "group-1", PolicyIDs: []string{"policy-priv"}}
	g.policies = map[string]Policy{
		"policy-priv": {ID: "policy-priv", Manager: "user", Statements: privilegedStatements()},
	}
	c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
		mux.HandleFunc("POST /policies-api/v1/groups/group-1/iam-users/user-1", func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("no write request expected")
		})
	})
	_, err := c.AddUserToGroup(context.Background(), &AddUserToGroupInput{GroupID: "group-1", UserID: "user-1"})
	if !errors.Is(err, ErrSelfChange) {
		t.Fatalf("AddUserToGroup() err = %v, want ErrSelfChange", err)
	}
	if errors.Is(err, ErrPrivilegedChange) {
		t.Fatal("err also matches ErrPrivilegedChange; ErrSelfChange must win alone")
	}
}

// TestAddUserToGroupRefusesWhenUserGroupsReadFails checks that a failure of
// the target user's own group-membership read refuses the write with
// nothing sent.
func TestAddUserToGroupRefusesWhenUserGroupsReadFails(t *testing.T) {
	g := unprivilegedMembershipFixture()
	g.userGroupsHandler = func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}
	c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
		mux.HandleFunc("POST /policies-api/v1/groups/group-1/iam-users/user-x", func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("no write request expected")
		})
	})
	if _, err := c.AddUserToGroup(context.Background(), &AddUserToGroupInput{GroupID: "group-1", UserID: "user-x"}); err == nil {
		t.Fatal("AddUserToGroup() error = nil, want a guard-read failure")
	}
}

// TestAddUserToGroupRefusesWhenGroupAttachmentsFail checks that a failure of
// the target group's own attachments read refuses the write with nothing
// sent.
func TestAddUserToGroupRefusesWhenGroupAttachmentsFail(t *testing.T) {
	g := unprivilegedMembershipFixture()
	g.groupHandler = func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}
	c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
		mux.HandleFunc("POST /policies-api/v1/groups/group-1/iam-users/user-x", func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("no write request expected")
		})
	})
	if _, err := c.AddUserToGroup(context.Background(), &AddUserToGroupInput{GroupID: "group-1", UserID: "user-x"}); err == nil {
		t.Fatal("AddUserToGroup() error = nil, want a guard-read failure")
	}
}

// TestAddUserToGroupRefusesUnknownCallerType checks that an unclassified
// caller refuses this guarded write, per the design's Terms section.
func TestAddUserToGroupRefusesUnknownCallerType(t *testing.T) {
	g := unprivilegedMembershipFixture()
	g.caller = userInfoResponse{UserID: "user-1", UserType: callerTypeRoot}
	c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
		mux.HandleFunc("POST /policies-api/v1/groups/group-1/iam-users/user-x", func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("no write request expected")
		})
	})
	if _, err := c.AddUserToGroup(context.Background(), &AddUserToGroupInput{GroupID: "group-1", UserID: "user-x"}); !errors.Is(err, ErrPrivilegedChange) {
		t.Fatalf("AddUserToGroup() err = %v, want ErrPrivilegedChange", err)
	}
}

func TestAddUserToGroupWriteStatuses(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusForbidden, http.StatusNotFound, http.StatusConflict, http.StatusInternalServerError} {
		g := unprivilegedMembershipFixture()
		c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
			mux.HandleFunc("POST /policies-api/v1/groups/group-1/iam-users/user-x", func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(status)
			})
		})
		if _, err := c.AddUserToGroup(context.Background(), &AddUserToGroupInput{GroupID: "group-1", UserID: "user-x"}); err == nil {
			t.Fatalf("status %d: AddUserToGroup() error = nil, want an error", status)
		}
	}
}

func TestRemoveUserFromGroupWriteStatuses(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusForbidden, http.StatusNotFound, http.StatusConflict, http.StatusInternalServerError} {
		g := unprivilegedMembershipFixture()
		c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
			mux.HandleFunc("DELETE /policies-api/v1/groups/group-1/iam-users/user-x", func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(status)
			})
		})
		if _, err := c.RemoveUserFromGroup(context.Background(), &RemoveUserFromGroupInput{GroupID: "group-1", UserID: "user-x"}); err == nil {
			t.Fatalf("status %d: RemoveUserFromGroup() error = nil, want an error", status)
		}
	}
}

// TestAddUserToGroupNoRetryOn502 checks that the add, a plain POST, is not
// retried after a 502: only a 429 or a failed dial is safe to retry for a
// non-idempotent request (ADR 0002 rule 2).
func TestAddUserToGroupNoRetryOn502(t *testing.T) {
	g := unprivilegedMembershipFixture()
	var calls int32
	handler := g.mux(t, func(mux *http.ServeMux) {
		mux.HandleFunc("POST /policies-api/v1/groups/group-1/iam-users/user-x", func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt32(&calls, 1)
			w.WriteHeader(http.StatusBadGateway)
		})
	})
	server := httptest.NewServer(handler)
	defer server.Close()
	tc := transport.New(transport.Config{HTTPClient: server.Client(), RetryCount: 3, RetryInterval: time.Millisecond})
	cfg := core.NewTestConfig("hcm-3", "project-1", endpoints.Set{Region: "hcm-3", Dashboard: server.URL + "/", IAM: server.URL + "/"}, tc)
	c := New(cfg)

	_, err := c.AddUserToGroup(context.Background(), &AddUserToGroupInput{GroupID: "group-1", UserID: "user-x"})
	if err == nil {
		t.Fatal("AddUserToGroup() error = nil, want an error")
	}
	if calls != 1 {
		t.Fatalf("server received %d request(s), want 1 (no retry after 502)", calls)
	}
}

// TestGroupMembershipGuardErrorTextNamesNoDetail checks that a refusal's
// error text never holds a policy name.
func TestGroupMembershipGuardErrorTextNamesNoDetail(t *testing.T) {
	g := unprivilegedMembershipFixture()
	g.groups["group-1"] = Group{ID: "group-1", PolicyIDs: []string{"policy-priv"}}
	g.policies = map[string]Policy{
		"policy-priv": {ID: "policy-priv", Name: "TopSecretPolicyName", Manager: "user", Statements: privilegedStatements()},
	}
	c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
		mux.HandleFunc("POST /policies-api/v1/groups/group-1/iam-users/user-x", func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("no write request expected")
		})
	})
	_, err := c.AddUserToGroup(context.Background(), &AddUserToGroupInput{GroupID: "group-1", UserID: "user-x"})
	if !errors.Is(err, ErrPrivilegedChange) {
		t.Fatalf("AddUserToGroup() err = %v, want ErrPrivilegedChange", err)
	}
	if strings.Contains(err.Error(), "TopSecretPolicyName") {
		t.Fatalf("error names the policy: %v", err)
	}
}
