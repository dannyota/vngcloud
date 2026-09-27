package containerregistry

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/testutil"
)

func TestContainerRegistryZeroConfig(t *testing.T) {
	c := New(vngcloud.Config{})
	if _, err := c.ListRepositories(context.Background(), nil); !errors.Is(err, vngcloud.ErrInvalidConfig) {
		t.Fatalf("ListRepositories() err = %v, want ErrInvalidConfig", err)
	}
}

func TestContainerRegistryListRepositories(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/repository" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("accessLevel") != "ALL" {
			t.Fatalf("unexpected query: %s", r.URL.RawQuery)
		}
		testutil.WriteFixture(t, w, "../testdata/containerregistry/list_repositories.json")
	}))

	out, err := c.ListRepositories(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListRepositories() error = %v", err)
	}
	if len(out.Items) != 1 || out.Items[0].ID != "repo-1" || out.Items[0].Name != "app" || out.PageSize != 25 {
		t.Fatalf("unexpected repositories: %+v", out)
	}
}

func TestContainerRegistryListUsers(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/user" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("name") != "alice" || r.URL.Query().Get("page") != "2" || r.URL.Query().Get("size") != "10" {
			t.Fatalf("unexpected query: %s", r.URL.RawQuery)
		}
		testutil.WriteFixture(t, w, "../testdata/containerregistry/list_users.json")
	}))

	out, err := c.ListUsers(context.Background(), &ListUsersInput{Name: "alice", Page: 2, Size: 10})
	if err != nil {
		t.Fatalf("ListUsers() error = %v", err)
	}
	if len(out.Items) != 1 || out.TotalPage != 3 {
		t.Fatalf("unexpected users: %+v", out)
	}
	u := out.Items[0]
	if u.ID != "ra-1" || u.Name != "app-ci" || u.UserID != "<account>" || u.Disabled || u.NumberOfRepositories != 2 {
		t.Fatalf("unexpected user: %+v", u)
	}
	if len(u.Repositories) != 2 {
		t.Fatalf("Repositories = %d, want 2", len(u.Repositories))
	}
	first := u.Repositories[0]
	if first.RepositoryID != "repo-1" || first.RepositoryName != "app" || len(first.Policies) != 1 || first.Policies[0].Action != "PULL" {
		t.Fatalf("unexpected first repository permission: %+v", first)
	}
}

// TestContainerRegistryListUsersNumericUserID checks that ListUsers decodes
// a live-shaped row: a live capture shows userId arriving as a JSON number,
// which User exposes as a string, and description arriving null.
func TestContainerRegistryListUsersNumericUserID(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		testutil.WriteFixture(t, w, "../testdata/containerregistry/list_users_numeric_userid.json")
	}))

	out, err := c.ListUsers(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListUsers() error = %v", err)
	}
	if len(out.Items) != 1 {
		t.Fatalf("Items = %d, want 1", len(out.Items))
	}
	u := out.Items[0]
	if u.UserID != "20260101" {
		t.Fatalf("UserID = %q, want %q", u.UserID, "20260101")
	}
	if u.Description != "" {
		t.Fatalf("Description = %q, want empty for a null value", u.Description)
	}
	if u.ID != "ra-2" || u.Name != "vcu-live" {
		t.Fatalf("unexpected user: %+v", u)
	}
}

func TestContainerRegistryListRepositoryUsers(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/repository/repo-1/user" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		testutil.WriteFixture(t, w, "../testdata/containerregistry/list_users.json")
	}))

	out, err := c.ListRepositoryUsers(context.Background(), &ListRepositoryUsersInput{RepositoryID: "repo-1"})
	if err != nil {
		t.Fatalf("ListRepositoryUsers() error = %v", err)
	}
	if len(out.Items) != 1 || out.Items[0].ID != "ra-1" {
		t.Fatalf("unexpected users: %+v", out)
	}
}

func TestContainerRegistryListRepositoryUsersRequiredInput(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("no request expected")
	}))
	if _, err := c.ListRepositoryUsers(context.Background(), &ListRepositoryUsersInput{}); !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
}

func TestContainerRegistryListRepositoryUsersPathIDRejection(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("no request expected")
	}))
	for _, id := range []string{"..", ".", "a/b", "a?b", ""} {
		if _, err := c.ListRepositoryUsers(context.Background(), &ListRepositoryUsersInput{RepositoryID: id}); !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Errorf("RepositoryID %q: err = %v, want ErrInvalidInput", id, err)
		}
	}
}

func TestContainerRegistryListPermissions(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/user/permissions" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		testutil.WriteFixture(t, w, "../testdata/containerregistry/list_permissions.json")
	}))

	out, err := c.ListPermissions(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListPermissions() error = %v", err)
	}
	if len(out.Items) != 2 || out.Items[0].ID != "policy-1" || out.Items[0].Action != "PULL" || out.Items[1].Action != "PUSH_PULL" {
		t.Fatalf("unexpected permissions: %+v", out.Items)
	}
}

func TestContainerRegistryListUsersDefaultPageSize(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page") != "1" || r.URL.Query().Get("size") != "10000" {
			t.Fatalf("unexpected query: %s", r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`{"listData":[],"page":1,"pageSize":10000}`))
	}))

	if _, err := c.ListUsers(context.Background(), nil); err != nil {
		t.Fatalf("ListUsers() error = %v", err)
	}
}

func newTestClient(t *testing.T, handler http.Handler) *Client {
	t.Helper()

	return New(testutil.NewConfig(t, handler))
}
