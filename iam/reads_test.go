package iam

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/endpoints"
	"danny.vn/vngcloud/internal/testutil"
	"danny.vn/vngcloud/internal/transport"
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
// accounts calls go to Dashboard, independently: Dashboard and IAM are two
// separate servers here, so a call reaching the wrong one fails the test
// instead of passing by coincidence. Building the Set this way is exactly
// what an endpoint override produces (the two fields are set independently
// of each other), so this also proves that overriding one host's URL never
// moves the other's calls.
func TestIAMHostRouting(t *testing.T) {
	var dashboardHits, iamHits int
	dashboard := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		dashboardHits++
		if r.URL.Path != "/accounts-api/v1/service-accounts" {
			t.Fatalf("dashboard server received unexpected path: %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer dashboard.Close()

	iamHost := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		iamHits++
		if r.URL.Path != "/policies-api/v1/policies" {
			t.Fatalf("IAM server received unexpected path: %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer iamHost.Close()

	tc := transport.New(transport.Config{HTTPClient: &http.Client{}})
	cfg := core.NewTestConfig("hcm-3", "project-1", endpoints.Set{
		Region:    "hcm-3",
		Dashboard: dashboard.URL + "/",
		IAM:       iamHost.URL + "/",
	}, tc)
	c := New(cfg)

	if _, err := c.ListPolicies(context.Background(), nil); err != nil {
		t.Fatalf("ListPolicies() error = %v", err)
	}
	if _, err := c.ListServiceAccounts(context.Background(), nil); err != nil {
		t.Fatalf("ListServiceAccounts() error = %v", err)
	}
	if dashboardHits != 1 {
		t.Fatalf("dashboard server hits = %d, want 1", dashboardHits)
	}
	if iamHits != 1 {
		t.Fatalf("IAM server hits = %d, want 1", iamHits)
	}
}

// TestIAMPathIDRejection checks that every call taking a path ID field
// rejects "..", ".", "/", "?", and empty before any request.
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
		if _, err := c.ListPolicyAttachments(context.Background(), &ListPolicyAttachmentsInput{PolicyID: id}); !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Fatalf("ListPolicyAttachments(%q) err = %v, want ErrInvalidInput", id, err)
		}
		if _, err := c.ListGroupPolicies(context.Background(), &ListGroupPoliciesInput{GroupID: id}); !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Fatalf("ListGroupPolicies(%q) err = %v, want ErrInvalidInput", id, err)
		}
		if _, err := c.ListUserPolicies(context.Background(), &ListUserPoliciesInput{UserID: id}); !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Fatalf("ListUserPolicies(%q) err = %v, want ErrInvalidInput", id, err)
		}
		if _, err := c.ListUserGroups(context.Background(), &ListUserGroupsInput{UserID: id}); !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Fatalf("ListUserGroups(%q) err = %v, want ErrInvalidInput", id, err)
		}
		if _, err := c.ListServiceAccountPolicies(context.Background(), &ListServiceAccountPoliciesInput{ServiceAccountID: id}); !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Fatalf("ListServiceAccountPolicies(%q) err = %v, want ErrInvalidInput", id, err)
		}
		if _, err := c.UpdateServiceAccount(context.Background(), &UpdateServiceAccountInput{ServiceAccountID: id}); !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Fatalf("UpdateServiceAccount(%q) err = %v, want ErrInvalidInput", id, err)
		}
		if _, err := c.DeleteServiceAccount(context.Background(), &DeleteServiceAccountInput{ServiceAccountID: id}); !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Fatalf("DeleteServiceAccount(%q) err = %v, want ErrInvalidInput", id, err)
		}
		if _, err := c.ResetServiceAccountSecret(context.Background(), &ResetServiceAccountSecretInput{ServiceAccountID: id}); !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Fatalf("ResetServiceAccountSecret(%q) err = %v, want ErrInvalidInput", id, err)
		}
		if _, err := c.UpdatePolicy(context.Background(), &UpdatePolicyInput{PolicyID: id}); !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Fatalf("UpdatePolicy(%q) err = %v, want ErrInvalidInput", id, err)
		}
		if _, err := c.DeletePolicy(context.Background(), &DeletePolicyInput{PolicyID: id}); !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Fatalf("DeletePolicy(%q) err = %v, want ErrInvalidInput", id, err)
		}
		if _, err := c.UpdateGroup(context.Background(), &UpdateGroupInput{GroupID: id}); !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Fatalf("UpdateGroup(%q) err = %v, want ErrInvalidInput", id, err)
		}
		if _, err := c.DeleteGroup(context.Background(), &DeleteGroupInput{GroupID: id}); !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Fatalf("DeleteGroup(%q) err = %v, want ErrInvalidInput", id, err)
		}
		if _, err := c.AttachServiceAccountPolicy(context.Background(), &AttachServiceAccountPolicyInput{PolicyID: id, ServiceAccountID: "sa-1"}); !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Fatalf("AttachServiceAccountPolicy(PolicyID=%q) err = %v, want ErrInvalidInput", id, err)
		}
		if _, err := c.DetachServiceAccountPolicy(context.Background(), &DetachServiceAccountPolicyInput{PolicyID: id, ServiceAccountID: "sa-1"}); !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Fatalf("DetachServiceAccountPolicy(PolicyID=%q) err = %v, want ErrInvalidInput", id, err)
		}
		if _, err := c.AttachGroupPolicy(context.Background(), &AttachGroupPolicyInput{PolicyID: id, GroupID: "group-1"}); !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Fatalf("AttachGroupPolicy(PolicyID=%q) err = %v, want ErrInvalidInput", id, err)
		}
		if _, err := c.DetachGroupPolicy(context.Background(), &DetachGroupPolicyInput{PolicyID: id, GroupID: "group-1"}); !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Fatalf("DetachGroupPolicy(PolicyID=%q) err = %v, want ErrInvalidInput", id, err)
		}
		if _, err := c.AttachUserPolicy(context.Background(), &AttachUserPolicyInput{PolicyID: id, UserID: "user-1"}); !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Fatalf("AttachUserPolicy(PolicyID=%q) err = %v, want ErrInvalidInput", id, err)
		}
		if _, err := c.DetachUserPolicy(context.Background(), &DetachUserPolicyInput{PolicyID: id, UserID: "user-1"}); !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Fatalf("DetachUserPolicy(PolicyID=%q) err = %v, want ErrInvalidInput", id, err)
		}
		if _, err := c.AddUserToGroup(context.Background(), &AddUserToGroupInput{GroupID: id, UserID: "user-1"}); !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Fatalf("AddUserToGroup(GroupID=%q) err = %v, want ErrInvalidInput", id, err)
		}
		if _, err := c.RemoveUserFromGroup(context.Background(), &RemoveUserFromGroupInput{GroupID: id, UserID: "user-1"}); !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Fatalf("RemoveUserFromGroup(GroupID=%q) err = %v, want ErrInvalidInput", id, err)
		}
		if id == "" {
			// CheckRequired already refuses an empty second field before
			// CheckPathID's own message would name it; the first-field cases
			// above already cover the empty path for every dual-ID call.
			continue
		}
		if _, err := c.AttachServiceAccountPolicy(context.Background(), &AttachServiceAccountPolicyInput{PolicyID: "policy-1", ServiceAccountID: id}); !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Fatalf("AttachServiceAccountPolicy(ServiceAccountID=%q) err = %v, want ErrInvalidInput", id, err)
		}
		if _, err := c.DetachServiceAccountPolicy(context.Background(), &DetachServiceAccountPolicyInput{PolicyID: "policy-1", ServiceAccountID: id}); !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Fatalf("DetachServiceAccountPolicy(ServiceAccountID=%q) err = %v, want ErrInvalidInput", id, err)
		}
		if _, err := c.AttachGroupPolicy(context.Background(), &AttachGroupPolicyInput{PolicyID: "policy-1", GroupID: id}); !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Fatalf("AttachGroupPolicy(GroupID=%q) err = %v, want ErrInvalidInput", id, err)
		}
		if _, err := c.DetachGroupPolicy(context.Background(), &DetachGroupPolicyInput{PolicyID: "policy-1", GroupID: id}); !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Fatalf("DetachGroupPolicy(GroupID=%q) err = %v, want ErrInvalidInput", id, err)
		}
		if _, err := c.AttachUserPolicy(context.Background(), &AttachUserPolicyInput{PolicyID: "policy-1", UserID: id}); !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Fatalf("AttachUserPolicy(UserID=%q) err = %v, want ErrInvalidInput", id, err)
		}
		if _, err := c.DetachUserPolicy(context.Background(), &DetachUserPolicyInput{PolicyID: "policy-1", UserID: id}); !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Fatalf("DetachUserPolicy(UserID=%q) err = %v, want ErrInvalidInput", id, err)
		}
		if _, err := c.AddUserToGroup(context.Background(), &AddUserToGroupInput{GroupID: "group-1", UserID: id}); !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Fatalf("AddUserToGroup(UserID=%q) err = %v, want ErrInvalidInput", id, err)
		}
		if _, err := c.RemoveUserFromGroup(context.Background(), &RemoveUserFromGroupInput{GroupID: "group-1", UserID: id}); !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Fatalf("RemoveUserFromGroup(UserID=%q) err = %v, want ErrInvalidInput", id, err)
		}
	}
}

func TestGuardCallerPrivacyCapture(t *testing.T) {
	captures := 0
	c := New(testutil.NewConfigWithCapture(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"userId":"caller","userType":"iam-user"}`))
	}), func(transport.Capture) { captures++ }))
	if _, err := c.guardCallerIdentity(context.Background(), userReadOptions{WithholdMessage: userWriteWithheld, ClassifyError: userWriteErrorCode}); err != nil {
		t.Fatal(err)
	}
	if captures != 0 {
		t.Fatal("private caller read captured")
	}
}

type privateGuardRead struct {
	name, path, body string
	read             func(context.Context, *Client, userReadOptions) error
}

func privateGuardReads() []privateGuardRead {
	return []privateGuardRead{
		{"caller", "/accounts-api/v1/auth/userinfo", `{"userId":"caller","userType":"iam-user"}`, func(ctx context.Context, c *Client, o userReadOptions) error {
			_, err := c.guardCallerIdentity(ctx, o)
			return err
		}},
		{"target", "/accounts-api/v1/iam-users/user-1", `{"id":"user-1","from":"iam"}`, func(ctx context.Context, c *Client, o userReadOptions) error {
			return c.guardUserWrite(ctx, "iam.privateGuard", "user-1", o)
		}},
		{"actions", "/policies-api/v1/actions", `[{"action":"CreatePolicy","label":"Write"}]`, func(ctx context.Context, c *Client, o userReadOptions) error {
			_, err := c.guardWriteActionNames(ctx, o)
			return err
		}},
		{"policy", "/policies-api/v1/policies/policy-1", `{"id":"policy-1","statements":[{"effect":"allow","actions":["vstorage:ListBuckets"]}]}`, func(ctx context.Context, c *Client, o userReadOptions) error {
			_, err := c.anyPolicyIDPrivileged(ctx, []string{"policy-1"}, []string{"iam:createpolicy"}, map[string]*Policy{}, o)
			return err
		}},
		{"page", "/policies-api/v1/user-attachments/iam-users/user-1/policies", `{"data":[],"totalItems":0,"totalPages":1}`, func(ctx context.Context, c *Client, o userReadOptions) error {
			_, err := c.guardUserPolicySummaries(ctx, "iam.privateGuard", "user-1", o)
			return err
		}},
		{"groups", "/policies-api/v1/user-attachments/iam-users/user-1/groups", `[{"id":"group-1","policies":[]}]`, func(ctx context.Context, c *Client, o userReadOptions) error {
			_, err := c.guardUserGroups(ctx, "iam.privateGuard", "user-1", o)
			return err
		}},
		{"group-attachments", "/policies-api/v1/groups/group-1", `{"mode":"iam","policies":[],"iamUsers":[]}`, func(ctx context.Context, c *Client, o userReadOptions) error {
			_, _, err := c.guardGetGroupAttachments(ctx, "iam.privateGuard", "group-1", o)
			return err
		}},
		{"policy-groups", "/policies-api/v1/policies/policy-1/groups", `[]`, privatePolicyAttachments},
		{"policy-users", "/policies-api/v1/policies/policy-1/iam-users", `[]`, privatePolicyAttachments},
		{"policy-service-accounts", "/policies-api/v1/policies/policy-1/service-accounts", `[]`, privatePolicyAttachments},
	}
}

func privatePolicyAttachments(ctx context.Context, c *Client, o userReadOptions) error {
	_, err := c.guardPolicyAttachments(ctx, "iam.privateGuard", "policy-1", o)
	return err
}

func privateGuardBody(path string) string {
	if path == "/policies-api/v1/policies" {
		return `{"data":[]}`
	}
	for _, edge := range privateGuardReads() {
		if edge.path == path {
			return edge.body
		}
	}
	return `[]`
}

func TestGuardReadPrivacyEdges(t *testing.T) {
	for _, edge := range privateGuardReads() {
		for _, status := range []int{200, 403, 404, 409, 429, 502} {
			t.Run(edge.name+"/"+strconv.Itoa(status), func(t *testing.T) {
				var captures atomic.Int32
				var logs lockedGuardLog
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path != edge.path {
						_, _ = io.WriteString(w, privateGuardBody(r.URL.Path))
						return
					}
					w.WriteHeader(status)
					if status == 200 {
						_, _ = io.WriteString(w, edge.body)
					} else {
						_, _ = io.WriteString(w, `{"code":"server-field","message":"{\"password\":\"password-canary\"}","error":"bearer-canary"}`)
					}
				}))
				t.Cleanup(server.Close)
				c := privateGuardClient(server, &logs, &captures)
				err := edge.read(context.Background(), c, userReadOptions{Redact: []string{"password-canary"}})
				if status == 200 {
					if err != nil {
						t.Fatal(err)
					}
				} else {
					var api *vngcloud.APIError
					if !errors.As(err, &api) || api.StatusCode != status || api.Message != userWriteWithheld || api.Code != core.ResolvedCode(status, "") {
						t.Fatalf("status or fixed error lost: %v", err)
					}
					assertUserErrorPrivate(t, err, "password-canary", "bearer-canary", "server-field")
				}
				if captures.Load() != 0 || strings.Contains(logs.String(), "password-canary") || strings.Contains(logs.String(), "bearer-canary") {
					t.Fatal("private read leaked to capture or debug")
				}
			})
		}
	}
}

type lockedGuardLog struct {
	sync.Mutex
	bytes.Buffer
}

func (b *lockedGuardLog) Write(p []byte) (int, error) {
	b.Lock()
	defer b.Unlock()
	return b.Buffer.Write(p)
}

func (b *lockedGuardLog) String() string {
	b.Lock()
	defer b.Unlock()
	return b.Buffer.String()
}

func privateGuardClient(server *httptest.Server, logs io.Writer, captures *atomic.Int32) *Client {
	return New(core.NewTestConfig("hcm-3", "", endpoints.Set{Dashboard: server.URL + "/", IAM: server.URL + "/"}, transport.New(transport.Config{
		HTTPClient: server.Client(), TokenSource: privateGuardToken{},
		Logger:  slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
		Capture: func(transport.Capture) { captures.Add(1) },
	})))
}

func TestGuardReadPrivacyMalformedEdges(t *testing.T) {
	for _, edge := range privateGuardReads() {
		for _, body := range []string{`{"password-canary":`, edge.body + ` {"password-canary":true}`} {
			t.Run(edge.name+"/"+strconv.Itoa(len(body)), func(t *testing.T) {
				c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					response := privateGuardBody(r.URL.Path)
					if r.URL.Path == edge.path {
						response = body
					}
					_, _ = io.WriteString(w, response)
				}))
				err := edge.read(context.Background(), c, userReadOptions{Redact: []string{"password-canary"}})
				if err == nil {
					t.Fatal("malformed guard response accepted")
				}
				assertUserErrorPrivate(t, err, "password-canary", "1e999")
			})
		}
	}
}

type privateGuardToken struct{}

func (privateGuardToken) Token(context.Context) (transport.Token, error) {
	return transport.Token{AccessToken: "bearer-canary"}, nil
}
func (privateGuardToken) Invalidate(string) {}

func TestGuardReadPrivacyConcurrentEdges(t *testing.T) {
	for _, edge := range privateGuardReads() {
		t.Run(edge.name, func(t *testing.T) {
			var logs lockedGuardLog
			var captures atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == edge.path {
					w.WriteHeader(403)
					_, _ = io.WriteString(w, `{"code":"first-password","message":"second-password"}`)
					return
				}
				_, _ = io.WriteString(w, privateGuardBody(r.URL.Path))
			}))
			t.Cleanup(server.Close)
			c := privateGuardClient(server, &logs, &captures)
			var wg sync.WaitGroup
			for _, password := range []string{"first-password", "second-password"} {
				wg.Go(func() {
					var sent []string
					err := edge.read(context.Background(), c, userReadOptions{Redact: []string{password}, SentCredentials: &sent})
					if !errors.Is(err, vngcloud.ErrPermission) {
						t.Errorf("read identity lost: %v", err)
					}
					assertUserErrorPrivate(t, err, "first-password", "second-password")
				})
			}
			wg.Wait()
			if captures.Load() != 0 || strings.Contains(logs.String(), "password") {
				t.Fatal("concurrent private read leaked")
			}
			if _, err := c.ListPolicies(context.Background(), nil); err != nil {
				t.Fatal(err)
			}
			if captures.Load() != 1 {
				t.Fatal("privacy escaped to a normal read")
			}
		})
	}
}

func TestGuardReadPrivacyConcurrentURLRedaction(t *testing.T) {
	var logs lockedGuardLog
	var captures atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(403)
	}))
	defer server.Close()
	c := privateGuardClient(server, &logs, &captures)
	var wg sync.WaitGroup
	for _, password := range []string{"first-password", "second-password"} {
		wg.Go(func() {
			_, err := c.guardPagedPolicySummaries(context.Background(), "iam.privateGuard", []string{"policies", password}, userReadOptions{Redact: []string{password}})
			if !errors.Is(err, vngcloud.ErrPermission) {
				t.Errorf("error identity lost: %v", err)
			}
			assertUserErrorPrivate(t, err, "first-password", "second-password")
		})
	}
	wg.Wait()
	if strings.Contains(logs.String(), "first-password") || strings.Contains(logs.String(), "second-password") || captures.Load() != 0 {
		t.Fatal("request-local URL redaction failed")
	}
}

func TestGuardReadPrivacyContextIdentity(t *testing.T) {
	for _, edge := range privateGuardReads() {
		t.Run(edge.name, func(t *testing.T) {
			c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("unexpected request") }))
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if err := edge.read(ctx, c, userReadOptions{}); !errors.Is(err, context.Canceled) {
				t.Fatalf("context identity lost: %v", err)
			}
		})
	}
}

func TestGuardReadPrivacyCredentialResponses(t *testing.T) {
	for _, edge := range privateGuardReads() {
		for _, credential := range []string{"password-canary", "bearer-canary", "PASSWORD-CANARY", "BEARER-CANARY"} {
			t.Run(edge.name+"/"+strconv.Itoa(len(credential)), func(t *testing.T) {
				var logs lockedGuardLog
				var captures atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					body := privateGuardBody(r.URL.Path)
					if r.URL.Path == edge.path {
						switch edge.name {
						case "caller":
							body = `{"userId":"` + credential + `","userType":"iam-user"}`
						case "target":
							body = `{"id":"user-1","from":"iam","username":"` + credential + `"}`
						case "actions":
							body = `[{"action":"` + credential + `","label":"Write"}]`
						case "policy":
							body = `{"id":"policy-1","description":"` + credential + `"}`
						case "page":
							body = `{"data":[{"id":"` + credential + `"}],"totalItems":1}`
						case "groups":
							body = `[{"id":"group-1","policies":["` + credential + `"]}]`
						case "group-attachments":
							body = `{"mode":"iam","policies":["` + credential + `"],"iamUsers":[]}`
						case "policy-groups":
							body = `[{"id":"` + credential + `"}]`
						default:
							body = `["` + credential + `"]`
						}
					}
					_, _ = io.WriteString(w, body)
				}))
				t.Cleanup(server.Close)
				c := privateGuardClient(server, &logs, &captures)
				err := edge.read(context.Background(), c, userReadOptions{Redact: []string{"password-canary"}})
				if err == nil {
					t.Fatal("credential-bearing response accepted")
				}
				assertUserErrorPrivate(t, err, "password-canary", "bearer-canary")
				if captures.Load() != 0 || strings.Contains(logs.String(), credential) {
					t.Fatal("credential-bearing response leaked")
				}
				if c.guardCaller != nil || len(c.guardWriteActions) != 0 {
					if edge.name == "caller" || edge.name == "actions" {
						t.Fatal("unsafe response cached")
					}
				}
			})
		}
	}
}

func TestGuardReadPrivacyLocalDecodeIdentity(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"mode":"iam","policies":[1e999],"iamUsers":[]}`)
	}))
	_, _, err := c.guardGetGroupAttachments(context.Background(), "iam.privateGuard", "group-1", userReadOptions{})
	var mismatch *json.UnmarshalTypeError
	if !errors.As(err, &mismatch) {
		t.Fatalf("decode identity lost: %v", err)
	}
	assertUserErrorPrivate(t, err, "1e999")
}
