package iam

import (
	"context"
	"net/http"
	"testing"

	"danny.vn/vngcloud/internal/testutil"
)

// TestListPoliciesCreatedAtForms checks that a policy list row's createdAt
// decodes from all three live forms: a plain epoch-milliseconds number, a
// {"$numberLong": "<digits>"} object, and an absent key, which decodes to 0.
func TestListPoliciesCreatedAtForms(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("pageNumber") != "0" {
			t.Fatalf("pageNumber = %q, want 0", r.URL.Query().Get("pageNumber"))
		}
		testutil.WriteFixture(t, w, "../testdata/iam/list_policies.json")
	}))

	out, err := c.ListPolicies(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListPolicies() error = %v", err)
	}
	if len(out.Items) != 3 {
		t.Fatalf("len(Items) = %d, want 3", len(out.Items))
	}
	if out.Items[0].CreatedAt != 1750000000000 {
		t.Fatalf("Items[0].CreatedAt = %d, want the numeric form's value", out.Items[0].CreatedAt)
	}
	if out.Items[1].CreatedAt != 1750000000000 {
		t.Fatalf("Items[1].CreatedAt = %d, want the $numberLong form's value", out.Items[1].CreatedAt)
	}
	if out.Items[2].CreatedAt != 0 {
		t.Fatalf("Items[2].CreatedAt = %d, want 0 for a missing key", out.Items[2].CreatedAt)
	}
}

func TestGetPolicyManaged(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/policies-api/v1/policies/policy-managed-1" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		testutil.WriteFixture(t, w, "../testdata/iam/get_policy_managed.json")
	}))

	out, err := c.GetPolicy(context.Background(), &GetPolicyInput{PolicyID: "policy-managed-1"})
	if err != nil {
		t.Fatalf("GetPolicy() error = %v", err)
	}
	if !out.Policy.Managed() {
		t.Fatalf("Managed() = false for a VNG CLOUD policy, want true")
	}
	if out.Policy.Root != "" {
		t.Fatalf("Root = %q, want empty for a managed policy", out.Policy.Root)
	}
}

func TestGetPolicyCustomer(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		testutil.WriteFixture(t, w, "../testdata/iam/get_policy_customer.json")
	}))

	out, err := c.GetPolicy(context.Background(), &GetPolicyInput{PolicyID: "policy-1"})
	if err != nil {
		t.Fatalf("GetPolicy() error = %v", err)
	}
	if out.Policy.Managed() {
		t.Fatalf("Managed() = true for a user policy, want false")
	}
	if out.Policy.Root != "<account>" {
		t.Fatalf("Root = %q, want the fixture value", out.Policy.Root)
	}
	if out.Policy.CreatedAt != 1750000000000 {
		t.Fatalf("CreatedAt = %d, want the $numberLong form's value", out.Policy.CreatedAt)
	}
}

func TestGetGroup(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/policies-api/v1/groups/group-1" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		testutil.WriteFixture(t, w, "../testdata/iam/get_group.json")
	}))

	out, err := c.GetGroup(context.Background(), &GetGroupInput{GroupID: "group-1"})
	if err != nil {
		t.Fatalf("GetGroup() error = %v", err)
	}
	if out.Group.Mode != "iam" || len(out.Group.UserIDs) != 1 || out.Group.UserIDs[0] != "user-1" {
		t.Fatalf("unexpected group: %+v", out.Group)
	}
	if out.Group.CreatedAt != 1750000000000 {
		t.Fatalf("CreatedAt = %d, want the fixture value", out.Group.CreatedAt)
	}
}

func TestGetServiceAccount(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/accounts-api/v1/service-accounts/sa-1" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		testutil.WriteFixture(t, w, "../testdata/iam/get_service_account.json")
	}))

	out, err := c.GetServiceAccount(context.Background(), &GetServiceAccountInput{ServiceAccountID: "sa-1"})
	if err != nil {
		t.Fatalf("GetServiceAccount() error = %v", err)
	}
	sa := out.ServiceAccount
	if sa.ClientID != "<client-id>" || sa.AccessTokenLifeSpan != 3600 || !sa.Enabled {
		t.Fatalf("unexpected service account: %+v", sa)
	}
	if sa.CreatedAt != 1750000000000 {
		t.Fatalf("CreatedAt = %d, want the numeric form's value", sa.CreatedAt)
	}
	if sa.LastUse != 1750000100000 {
		t.Fatalf("LastUse = %d, want the $numberLong form's value", sa.LastUse)
	}
}

func TestGetCallerIdentity(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/accounts-api/v1/auth/userinfo" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		testutil.WriteFixture(t, w, "../testdata/iam/userinfo.json")
	}))

	out, err := c.GetCallerIdentity(context.Background(), nil)
	if err != nil {
		t.Fatalf("GetCallerIdentity() error = %v", err)
	}
	if out.UserID != "user-1" || out.UserType != "iam-user" || out.AccountID != 700000000123 {
		t.Fatalf("unexpected caller identity: %+v", out)
	}
}

func TestListActionsAllLabels(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("product") != "iam" {
			t.Fatalf("product = %q, want iam", r.URL.Query().Get("product"))
		}
		testutil.WriteFixture(t, w, "../testdata/iam/list_actions.json")
	}))

	out, err := c.ListActions(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListActions() error = %v", err)
	}
	if len(out.Items) != 4 {
		t.Fatalf("len(Items) = %d, want 4", len(out.Items))
	}
	labels := map[string]bool{}
	for _, a := range out.Items {
		labels[a.Label] = true
	}
	for _, want := range []string{"List", "Read", "Write", "Tagging"} {
		if !labels[want] {
			t.Fatalf("labels = %v, missing %q", labels, want)
		}
	}
}
