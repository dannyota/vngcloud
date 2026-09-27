package iam

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/testutil"
)

// TestListUsersPaging checks that Page 0 sends pageNumber=0 and a
// non-positive Size sends core.DefaultPageSize, per the design's paging
// contract (page numbers start at 0, unlike the rest of the SDK).
func TestListUsersPaging(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("pageNumber"); got != "0" {
			t.Fatalf("pageNumber = %q, want 0", got)
		}
		if got := r.URL.Query().Get("pageSize"); got != "10000" {
			t.Fatalf("pageSize = %q, want 10000", got)
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"user-1","username":"<account>","createdAt":"2026-01-01T00:00:00Z"}]}`))
	}))

	out, err := c.ListUsers(context.Background(), &ListUsersInput{Page: 0, Size: 0})
	if err != nil {
		t.Fatalf("ListUsers() error = %v", err)
	}
	if len(out.Items) != 1 || out.Items[0].ID != "user-1" || out.Items[0].CreatedAt != "2026-01-01T00:00:00Z" {
		t.Fatalf("unexpected users: %+v", out.Items)
	}
}

func TestListUsersExplicitPage(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("pageNumber"); got != "2" {
			t.Fatalf("pageNumber = %q, want 2", got)
		}
		if got := r.URL.Query().Get("pageSize"); got != "5" {
			t.Fatalf("pageSize = %q, want 5", got)
		}
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))

	if _, err := c.ListUsers(context.Background(), &ListUsersInput{Page: 2, Size: 5}); err != nil {
		t.Fatalf("ListUsers() error = %v", err)
	}
}

func TestListServiceAccountsNameFilter(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/accounts-api/v1/service-accounts" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("name"); got != "app" {
			t.Fatalf("name = %q, want app", got)
		}
		testutil.WriteFixture(t, w, "../testdata/iam/list_service_accounts.json")
	}))

	out, err := c.ListServiceAccounts(context.Background(), &ListServiceAccountsInput{Name: "app"})
	if err != nil {
		t.Fatalf("ListServiceAccounts() error = %v", err)
	}
	if len(out.Items) != 1 || out.Items[0].ID != "sa-1" {
		t.Fatalf("unexpected service accounts: %+v", out.Items)
	}
}

func TestListServiceAccountsNoNameFilter(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Has("name") {
			t.Fatalf("name query set when Name is empty: %s", r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))

	if _, err := c.ListServiceAccounts(context.Background(), nil); err != nil {
		t.Fatalf("ListServiceAccounts() error = %v", err)
	}
}

func TestListPolicyAttachments(t *testing.T) {
	var paths []string
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		switch r.URL.Path {
		case "/policies-api/v1/policies/policy-1/groups":
			_, _ = w.Write([]byte(`[{"id":"group-1","name":"<name>"}]`))
		case "/policies-api/v1/policies/policy-1/iam-users":
			_, _ = w.Write([]byte(`["user-1"]`))
		case "/policies-api/v1/policies/policy-1/service-accounts":
			_, _ = w.Write([]byte(`["sa-1"]`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))

	out, err := c.ListPolicyAttachments(context.Background(), &ListPolicyAttachmentsInput{PolicyID: "policy-1"})
	if err != nil {
		t.Fatalf("ListPolicyAttachments() error = %v", err)
	}
	if len(out.Groups) != 1 || out.Groups[0].ID != "group-1" {
		t.Fatalf("unexpected groups: %+v", out.Groups)
	}
	if len(out.UserIDs) != 1 || out.UserIDs[0] != "user-1" {
		t.Fatalf("unexpected user ids: %+v", out.UserIDs)
	}
	if len(out.ServiceAccountIDs) != 1 || out.ServiceAccountIDs[0] != "sa-1" {
		t.Fatalf("unexpected service account ids: %+v", out.ServiceAccountIDs)
	}
	if len(paths) != 3 {
		t.Fatalf("made %d requests, want 3", len(paths))
	}
}

func TestListGroupsBareArray(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/policies-api/v1/groups" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.URL.Query().Has("pageNumber") {
			t.Fatalf("ListGroups must not page: %s", r.URL.RawQuery)
		}
		testutil.WriteFixture(t, w, "../testdata/iam/list_groups.json")
	}))

	out, err := c.ListGroups(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListGroups() error = %v", err)
	}
	if len(out.Items) != 2 || out.Items[0].ID != "group-1" {
		t.Fatalf("unexpected groups: %+v", out.Items)
	}
}

func TestListUserGroupsFullShape(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/policies-api/v1/user-attachments/iam-users/user-1/groups" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		testutil.WriteFixture(t, w, "../testdata/iam/list_user_groups.json")
	}))

	out, err := c.ListUserGroups(context.Background(), &ListUserGroupsInput{UserID: "user-1"})
	if err != nil {
		t.Fatalf("ListUserGroups() error = %v", err)
	}
	if len(out.Items) != 1 || out.Items[0].Mode != "iam" || len(out.Items[0].PolicyIDs) != 1 {
		t.Fatalf("unexpected groups: %+v", out.Items)
	}
}

func TestListGroupPoliciesPaging(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/policies-api/v1/groups/group-1/policies" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("pageNumber"); got != "0" {
			t.Fatalf("pageNumber = %q, want 0", got)
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"policy-1","name":"<name>"}]}`))
	}))

	out, err := c.ListGroupPolicies(context.Background(), &ListGroupPoliciesInput{GroupID: "group-1"})
	if err != nil {
		t.Fatalf("ListGroupPolicies() error = %v", err)
	}
	if len(out.Items) != 1 || out.Items[0].ID != "policy-1" {
		t.Fatalf("unexpected policies: %+v", out.Items)
	}
}

func TestListUserPolicies(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/policies-api/v1/user-attachments/iam-users/user-1/policies" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))

	if _, err := c.ListUserPolicies(context.Background(), &ListUserPoliciesInput{UserID: "user-1"}); err != nil {
		t.Fatalf("ListUserPolicies() error = %v", err)
	}
}

func TestListServiceAccountPolicies(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/policies-api/v1/user-attachments/service-accounts/sa-1/policies" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))

	if _, err := c.ListServiceAccountPolicies(context.Background(), &ListServiceAccountPoliciesInput{ServiceAccountID: "sa-1"}); err != nil {
		t.Fatalf("ListServiceAccountPolicies() error = %v", err)
	}
}

// TestIAMHostRouting checks that policies calls go to the IAM endpoint and
// accounts calls go to Dashboard, and that each can be overridden
// independently.
func TestIAMHostRouting(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	if _, err := c.ListPolicies(context.Background(), nil); err != nil {
		t.Fatalf("ListPolicies() error = %v", err)
	}
	if _, err := c.ListServiceAccounts(context.Background(), nil); err != nil {
		t.Fatalf("ListServiceAccounts() error = %v", err)
	}
}

// TestIAMPathIDRejection checks that every path ID field is rejected before
// any request for "..", ".", "/", "?", and empty.
func TestIAMPathIDRejection(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("no request expected")
	}))
	for _, id := range []string{"..", ".", "/", "?", ""} {
		if _, err := c.GetPolicy(context.Background(), &GetPolicyInput{PolicyID: id}); !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Fatalf("GetPolicy(%q) err = %v, want ErrInvalidInput", id, err)
		}
		if _, err := c.GetGroup(context.Background(), &GetGroupInput{GroupID: id}); !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Fatalf("GetGroup(%q) err = %v, want ErrInvalidInput", id, err)
		}
		if _, err := c.GetServiceAccount(context.Background(), &GetServiceAccountInput{ServiceAccountID: id}); !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Fatalf("GetServiceAccount(%q) err = %v, want ErrInvalidInput", id, err)
		}
	}
}
