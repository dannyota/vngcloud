package compute

import (
	"context"
	"net/http"
	"testing"

	"danny.vn/vngcloud/internal/testutil"
)

func TestComputeListServers(t *testing.T) {
	service := newTestService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/project-1/servers" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("page") != "2" || r.URL.Query().Get("size") != "10" {
			t.Fatalf("unexpected query: %s", r.URL.RawQuery)
		}
		testutil.WriteFixture(t, w, "../../testdata/compute/list_servers.json")
	}))

	servers, err := service.ListServers(context.Background(), &ListServersOptions{Page: 2, Size: 10})
	if err != nil {
		t.Fatalf("ListServers() error = %v", err)
	}
	if len(servers.Items) != 1 || servers.Items[0].UUID != "server-1" {
		t.Fatalf("unexpected servers: %+v", servers)
	}
	if !servers.Items[0].IsRunning() || !servers.Items[0].CanDelete() {
		t.Fatalf("unexpected server helpers: %+v", servers.Items[0])
	}
}

func TestComputeListServersDefaultPageSize(t *testing.T) {
	service := newTestService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page") != "1" || r.URL.Query().Get("size") != "10000" {
			t.Fatalf("unexpected query: %s", r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`{"listData":[],"page":1,"pageSize":10000,"totalPage":0,"totalItem":0}`))
	}))

	if _, err := service.ListServers(context.Background(), nil); err != nil {
		t.Fatalf("ListServers() error = %v", err)
	}
}

func TestComputeGetServer(t *testing.T) {
	service := newTestService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/project-1/servers/server-1" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		testutil.WriteFixture(t, w, "../../testdata/compute/get_server.json")
	}))

	server, err := service.GetServer(context.Background(), "server-1")
	if err != nil {
		t.Fatalf("GetServer() error = %v", err)
	}
	if server.UUID != "server-1" || server.Name != "<name>" {
		t.Fatalf("unexpected server: %+v", server)
	}
}

func TestComputeListSSHKeys(t *testing.T) {
	service := newTestService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/project-1/sshKeys" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("name") != "main" || r.URL.Query().Get("page") != "2" || r.URL.Query().Get("size") != "10" {
			t.Fatalf("unexpected query: %s", r.URL.RawQuery)
		}
		testutil.WriteFixture(t, w, "../../testdata/compute/list_ssh_keys.json")
	}))

	keys, err := service.ListSSHKeys(context.Background(), &ListSSHKeysOptions{Name: "main", Page: 2, Size: 10})
	if err != nil {
		t.Fatalf("ListSSHKeys() error = %v", err)
	}
	if len(keys.Items) != 1 || keys.Items[0].ID != "key-1" || keys.Items[0].Name != "<name>" {
		t.Fatalf("unexpected ssh keys: %+v", keys)
	}
}

func TestComputeListServerGroups(t *testing.T) {
	service := newTestService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/project-1/serverGroups" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("name") != "anti-affinity" || r.URL.Query().Get("offset") != "2" || r.URL.Query().Get("limit") != "10" {
			t.Fatalf("unexpected query: %s", r.URL.RawQuery)
		}
		testutil.WriteFixture(t, w, "../../testdata/compute/list_server_groups.json")
	}))

	groups, err := service.ListServerGroups(context.Background(), &ListServerGroupsOptions{Name: "anti-affinity", Page: 2, Size: 10})
	if err != nil {
		t.Fatalf("ListServerGroups() error = %v", err)
	}
	if len(groups.Items) != 1 || groups.Items[0].UUID != "group-1" || len(groups.Items[0].Servers) != 1 {
		t.Fatalf("unexpected server groups: %+v", groups)
	}
}

func TestComputeDerivedServerGroupMembers(t *testing.T) {
	service := newTestService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/project-1/serverGroups" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		testutil.WriteFixture(t, w, "../../testdata/compute/list_server_groups.json")
	}))

	members, err := service.ListServerGroupMembers(context.Background())
	if err != nil {
		t.Fatalf("ListServerGroupMembers() error = %v", err)
	}
	if len(members) != 1 || members[0].ServerGroupID != "group-1" || members[0].UUID != "server-1" {
		t.Fatalf("unexpected server group members: %+v", members)
	}
}

func TestComputeDerivedServerSecurityGroups(t *testing.T) {
	service := newTestService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/project-1/servers" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		testutil.WriteFixture(t, w, "../../testdata/compute/list_servers.json")
	}))

	groups, err := service.ListServerSecurityGroups(context.Background())
	if err != nil {
		t.Fatalf("ListServerSecurityGroups() error = %v", err)
	}
	if len(groups) != 1 || groups[0].ServerID != "server-1" || groups[0].UUID != "secgroup-1" {
		t.Fatalf("unexpected server security groups: %+v", groups)
	}
}

func TestComputeListServerGroupPolicies(t *testing.T) {
	service := newTestService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/project-1/serverGroups/policies" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		testutil.WriteFixture(t, w, "../../testdata/compute/list_server_group_policies.json")
	}))

	policies, err := service.ListServerGroupPolicies(context.Background())
	if err != nil {
		t.Fatalf("ListServerGroupPolicies() error = %v", err)
	}
	if len(policies) != 1 || policies[0].UUID != "policy-1" || policies[0].Descriptions["en"] == "" {
		t.Fatalf("unexpected policies: %+v", policies)
	}
}

func TestComputeListOSImages(t *testing.T) {
	service := newTestService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/project-1/images/os" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("zoneId") != "zone-a" {
			t.Fatalf("unexpected query: %s", r.URL.RawQuery)
		}
		testutil.WriteFixture(t, w, "../../testdata/compute/list_os_images.json")
	}))

	images, err := service.ListOSImages(context.Background(), &ListOSImagesOptions{ZoneID: "zone-a"})
	if err != nil {
		t.Fatalf("ListOSImages() error = %v", err)
	}
	if len(images) != 1 || images[0].ID != "image-1" {
		t.Fatalf("unexpected os images: %+v", images)
	}
}

func TestComputeListGPUImages(t *testing.T) {
	service := newTestService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/project-1/images/gpu" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		testutil.WriteFixture(t, w, "../../testdata/compute/list_gpu_images.json")
	}))

	images, err := service.ListGPUImages(context.Background())
	if err != nil {
		t.Fatalf("ListGPUImages() error = %v", err)
	}
	if len(images) != 1 || images[0].ID != "gpu-image-1" {
		t.Fatalf("unexpected gpu images: %+v", images)
	}
}

func TestComputeListUserImages(t *testing.T) {
	service := newTestService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/project-1/user-images" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		testutil.WriteFixture(t, w, "../../testdata/compute/list_user_images.json")
	}))

	images, err := service.ListUserImages(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListUserImages() error = %v", err)
	}
	if len(images.Items) != 1 || images.Items[0].UUID != "user-image-1" {
		t.Fatalf("unexpected user images: %+v", images)
	}
}

func newTestService(t *testing.T, handler http.Handler) *Service {
	t.Helper()

	client := testutil.NewCoreClient(t, handler)
	return New(client)
}
