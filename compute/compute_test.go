package compute

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/testutil"
)

func TestComputeListServers(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/project-1/servers" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("page") != "2" || r.URL.Query().Get("size") != "10" {
			t.Fatalf("unexpected query: %s", r.URL.RawQuery)
		}
		testutil.WriteFixture(t, w, "../testdata/compute/list_servers.json")
	}))

	out, err := c.ListServers(context.Background(), &ListServersInput{Page: 2, Size: 10})
	if err != nil {
		t.Fatalf("ListServers() error = %v", err)
	}
	if len(out.Items) != 1 || out.Items[0].UUID != "server-1" {
		t.Fatalf("unexpected servers: %+v", out)
	}
	if !out.Items[0].IsRunning() || !out.Items[0].CanDelete() {
		t.Fatalf("unexpected server helpers: %+v", out.Items[0])
	}
}

func TestComputeListServersDefaultPageSize(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page") != "1" || r.URL.Query().Get("size") != "10000" {
			t.Fatalf("unexpected query: %s", r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`{"listData":[],"page":1,"pageSize":10000,"totalPage":0,"totalItem":0}`))
	}))

	if _, err := c.ListServers(context.Background(), nil); err != nil {
		t.Fatalf("ListServers() error = %v", err)
	}
}

func TestComputeGetServer(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/project-1/servers/server-1" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		testutil.WriteFixture(t, w, "../testdata/compute/get_server.json")
	}))

	out, err := c.GetServer(context.Background(), &GetServerInput{ServerID: "server-1"})
	if err != nil {
		t.Fatalf("GetServer() error = %v", err)
	}
	if out.Server.UUID != "server-1" || out.Server.Name != "<name>" {
		t.Fatalf("unexpected server: %+v", out.Server)
	}
}

func TestComputeListSSHKeys(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/project-1/sshKeys" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("name") != "main" || r.URL.Query().Get("page") != "2" || r.URL.Query().Get("size") != "10" {
			t.Fatalf("unexpected query: %s", r.URL.RawQuery)
		}
		testutil.WriteFixture(t, w, "../testdata/compute/list_ssh_keys.json")
	}))

	out, err := c.ListSSHKeys(context.Background(), &ListSSHKeysInput{Name: "main", Page: 2, Size: 10})
	if err != nil {
		t.Fatalf("ListSSHKeys() error = %v", err)
	}
	if len(out.Items) != 1 || out.Items[0].ID != "key-1" || out.Items[0].Name != "<name>" {
		t.Fatalf("unexpected ssh keys: %+v", out)
	}
}

func TestComputeListServerGroups(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/project-1/serverGroups" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("name") != "anti-affinity" || r.URL.Query().Get("offset") != "2" || r.URL.Query().Get("limit") != "10" {
			t.Fatalf("unexpected query: %s", r.URL.RawQuery)
		}
		testutil.WriteFixture(t, w, "../testdata/compute/list_server_groups.json")
	}))

	out, err := c.ListServerGroups(context.Background(), &ListServerGroupsInput{Name: "anti-affinity", Page: 2, Size: 10})
	if err != nil {
		t.Fatalf("ListServerGroups() error = %v", err)
	}
	if len(out.Items) != 1 || out.Items[0].UUID != "group-1" || len(out.Items[0].Servers) != 1 {
		t.Fatalf("unexpected server groups: %+v", out)
	}
}

func TestComputeDerivedServerGroupMembers(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/project-1/serverGroups" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		testutil.WriteFixture(t, w, "../testdata/compute/list_server_groups.json")
	}))

	out, err := c.ListServerGroupMembers(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListServerGroupMembers() error = %v", err)
	}
	if len(out.Items) != 1 || out.Items[0].ServerGroupID != "group-1" || out.Items[0].UUID != "server-1" {
		t.Fatalf("unexpected server group members: %+v", out)
	}
}

func TestComputeDerivedServerSecurityGroups(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/project-1/servers" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		testutil.WriteFixture(t, w, "../testdata/compute/list_servers.json")
	}))

	out, err := c.ListServerSecurityGroups(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListServerSecurityGroups() error = %v", err)
	}
	if len(out.Items) != 1 || out.Items[0].ServerID != "server-1" || out.Items[0].UUID != "secgroup-1" {
		t.Fatalf("unexpected server security groups: %+v", out)
	}
}

func TestComputeListServerGroupPolicies(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/project-1/serverGroups/policies" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		testutil.WriteFixture(t, w, "../testdata/compute/list_server_group_policies.json")
	}))

	out, err := c.ListServerGroupPolicies(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListServerGroupPolicies() error = %v", err)
	}
	if len(out.Items) != 1 || out.Items[0].UUID != "policy-1" || out.Items[0].Descriptions["en"] == "" {
		t.Fatalf("unexpected policies: %+v", out)
	}
}

func TestComputeListOSImages(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/project-1/images/os" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("zoneId") != "zone-a" {
			t.Fatalf("unexpected query: %s", r.URL.RawQuery)
		}
		testutil.WriteFixture(t, w, "../testdata/compute/list_os_images.json")
	}))

	out, err := c.ListOSImages(context.Background(), &ListOSImagesInput{ZoneID: "zone-a"})
	if err != nil {
		t.Fatalf("ListOSImages() error = %v", err)
	}
	if len(out.Items) != 1 || out.Items[0].ID != "image-1" {
		t.Fatalf("unexpected os images: %+v", out)
	}
}

func TestComputeListGPUImages(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/project-1/images/gpu" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		testutil.WriteFixture(t, w, "../testdata/compute/list_gpu_images.json")
	}))

	out, err := c.ListGPUImages(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListGPUImages() error = %v", err)
	}
	if len(out.Items) != 1 || out.Items[0].ID != "gpu-image-1" {
		t.Fatalf("unexpected gpu images: %+v", out)
	}
}

func TestComputeListUserImages(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/project-1/user-images" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		testutil.WriteFixture(t, w, "../testdata/compute/list_user_images.json")
	}))

	out, err := c.ListUserImages(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListUserImages() error = %v", err)
	}
	if len(out.Items) != 1 || out.Items[0].UUID != "user-image-1" {
		t.Fatalf("unexpected user images: %+v", out)
	}
}

func TestComputeRequiredInput(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("no request expected")
	}))
	_, err := c.GetServer(context.Background(), &GetServerInput{})
	if !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("err = %v", err)
	}
	if _, err := c.GetServer(context.Background(), nil); !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("nil input err = %v", err)
	}
}

func TestComputeZeroConfig(t *testing.T) {
	c := New(vngcloud.Config{})
	if _, err := c.ListServers(context.Background(), nil); !errors.Is(err, vngcloud.ErrInvalidConfig) {
		t.Fatalf("ListServers() err = %v, want ErrInvalidConfig", err)
	}
}

func newTestClient(t *testing.T, handler http.Handler) *Client {
	t.Helper()

	return New(testutil.NewConfig(t, handler))
}
