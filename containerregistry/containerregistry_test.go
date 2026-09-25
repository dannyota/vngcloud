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
	if len(out.Items) != 1 || out.Items[0]["name"] != "<name>" || out.PageSize != 25 {
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
	if len(out.Items) != 1 || out.Items[0]["username"] != "<account>" || out.TotalPage != 3 {
		t.Fatalf("unexpected users: %+v", out)
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
