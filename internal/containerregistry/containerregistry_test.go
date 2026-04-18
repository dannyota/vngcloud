package containerregistry

import (
	"context"
	"net/http"
	"testing"

	"danny.vn/vngcloud/internal/testutil"
)

func TestContainerRegistryListRepositories(t *testing.T) {
	service := newTestService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/repository" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("accessLevel") != "ALL" {
			t.Fatalf("unexpected query: %s", r.URL.RawQuery)
		}
		testutil.WriteFixture(t, w, "../../testdata/containerregistry/list_repositories.json")
	}))

	repositories, err := service.ListRepositories(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListRepositories() error = %v", err)
	}
	if len(repositories.Items) != 1 || repositories.Items[0]["name"] != "<name>" || repositories.Page.PageSize != 25 {
		t.Fatalf("unexpected repositories: %+v", repositories)
	}
}

func TestContainerRegistryListUsers(t *testing.T) {
	service := newTestService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/user" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("name") != "alice" || r.URL.Query().Get("page") != "2" || r.URL.Query().Get("size") != "10" {
			t.Fatalf("unexpected query: %s", r.URL.RawQuery)
		}
		testutil.WriteFixture(t, w, "../../testdata/containerregistry/list_users.json")
	}))

	users, err := service.ListUsers(context.Background(), &ListContainerRegistryUsersOptions{Name: "alice", Page: 2, Size: 10})
	if err != nil {
		t.Fatalf("ListUsers() error = %v", err)
	}
	if len(users.Items) != 1 || users.Items[0]["username"] != "<account>" || users.Page.TotalPage != 3 {
		t.Fatalf("unexpected users: %+v", users)
	}
}

func TestContainerRegistryListUsersDefaultPageSize(t *testing.T) {
	service := newTestService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page") != "1" || r.URL.Query().Get("size") != "10000" {
			t.Fatalf("unexpected query: %s", r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`{"listData":[],"page":1,"pageSize":10000}`))
	}))

	if _, err := service.ListUsers(context.Background(), nil); err != nil {
		t.Fatalf("ListUsers() error = %v", err)
	}
}

func newTestService(t *testing.T, handler http.Handler) *Service {
	t.Helper()

	client := testutil.NewCoreClient(t, handler)
	return New(client)
}
