package compute

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/testutil"
)

func decodeServerGroupBody(t *testing.T, r *http.Request) map[string]any {
	t.Helper()
	data, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if len(data) == 0 {
		return nil
	}
	var body map[string]any
	if err := json.Unmarshal(data, &body); err != nil {
		t.Fatalf("decode body: %v, raw = %s", err, data)
	}
	return body
}

// serverGroupBody builds a {"data": {...}} server group envelope for group
// "server-group-1", matching what GetServerGroup decodes.
func serverGroupBody(name, description string) string {
	var fixture struct {
		Data struct {
			UUID        string `json:"uuid"`
			Name        string `json:"name"`
			Description string `json:"description"`
			PolicyID    string `json:"policyId"`
		} `json:"data"`
	}
	fixture.Data.UUID = "server-group-1"
	fixture.Data.Name = name
	fixture.Data.Description = description
	fixture.Data.PolicyID = "policy-1"
	b, err := json.Marshal(fixture)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// --- GetServerGroup ---

func TestComputeGetServerGroup(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/project-1/serverGroups/server-group-1" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		testutil.WriteFixture(t, w, "../testdata/compute/get_server_group.json")
	}))

	out, err := c.GetServerGroup(context.Background(), &GetServerGroupInput{ServerGroupID: "server-group-1"})
	if err != nil {
		t.Fatalf("GetServerGroup() error = %v", err)
	}
	if out.ServerGroup.UUID != "server-group-1" || out.ServerGroup.Name != "<name>" || out.ServerGroup.PolicyID != "policy-1" {
		t.Fatalf("unexpected server group: %+v", out.ServerGroup)
	}
	if out.ServerGroup.ServerGroupID == nil {
		t.Fatal("ServerGroupID did not decode")
	}
}

// TestComputeGetServerGroupDeletedReturnsNotFound checks that a 200 response
// with "data" null, which the server sends for a group just deleted instead
// of a 404, maps to the SDK's ordinary not-found sentinel rather than an
// empty ServerGroup with no error.
func TestComputeGetServerGroupDeletedReturnsNotFound(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"data":null}`))
	}))

	out, err := c.GetServerGroup(context.Background(), &GetServerGroupInput{ServerGroupID: "server-group-1"})
	if !vngcloud.IsNotFound(err) {
		t.Fatalf("GetServerGroup() err = %v, want NotFound", err)
	}
	if out != nil {
		t.Fatalf("GetServerGroup() out = %+v, want nil", out)
	}
}

func TestComputeGetServerGroupNotFound(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))

	_, err := c.GetServerGroup(context.Background(), &GetServerGroupInput{ServerGroupID: "server-group-1"})
	if !vngcloud.IsNotFound(err) {
		t.Fatalf("GetServerGroup() err = %v, want NotFound", err)
	}
}

// --- CreateServerGroup ---

func TestComputeCreateServerGroupRequestBody(t *testing.T) {
	cases := []struct {
		name string
		in   *CreateServerGroupInput
		want map[string]any
	}{
		{
			name: "with description",
			in:   &CreateServerGroupInput{Name: "web", PolicyID: "policy-1", Description: "anti-affinity"},
			want: map[string]any{"name": "web", "policyId": "policy-1", "description": "anti-affinity"},
		},
		{
			name: "without description",
			in:   &CreateServerGroupInput{Name: "web", PolicyID: "policy-1"},
			want: map[string]any{"name": "web", "policyId": "policy-1", "description": ""},
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/v2/project-1/serverGroups" {
					t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
				}
				body := decodeServerGroupBody(t, r)
				if body["name"] != tt.want["name"] || body["policyId"] != tt.want["policyId"] || body["description"] != tt.want["description"] {
					t.Fatalf("body = %+v, want %+v", body, tt.want)
				}
				w.WriteHeader(http.StatusCreated)
				testutil.WriteFixture(t, w, "../testdata/compute/create_server_group.json")
			}))

			out, err := c.CreateServerGroup(context.Background(), tt.in)
			if err != nil {
				t.Fatalf("CreateServerGroup() error = %v", err)
			}
			if out.ServerGroup.UUID != "server-group-1" {
				t.Fatalf("UUID = %q, want server-group-1", out.ServerGroup.UUID)
			}
		})
	}
}

func TestComputeCreateServerGroupDecodesFixture(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected method %s", r.Method)
		}
		w.WriteHeader(http.StatusCreated)
		testutil.WriteFixture(t, w, "../testdata/compute/create_server_group.json")
	}))

	out, err := c.CreateServerGroup(context.Background(), &CreateServerGroupInput{Name: "<name>", PolicyID: "policy-1", Description: "<description>"})
	if err != nil {
		t.Fatalf("CreateServerGroup() error = %v", err)
	}
	if out.ServerGroup.UUID != "server-group-1" || out.ServerGroup.Name != "<name>" ||
		out.ServerGroup.Description != "<description>" || out.ServerGroup.PolicyID != "policy-1" {
		t.Fatalf("unexpected server group: %+v", out.ServerGroup)
	}
	if out.ServerGroup.ServerGroupID == nil {
		t.Fatal("ServerGroupID did not decode")
	}
	if out.ServerGroup.CreatedAt == "" {
		t.Fatal("CreatedAt did not decode")
	}
}

func TestComputeCreateServerGroupRequiredInput(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("no request expected")
	}))
	if _, err := c.CreateServerGroup(context.Background(), &CreateServerGroupInput{}); !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
	if _, err := c.CreateServerGroup(context.Background(), &CreateServerGroupInput{Name: "web"}); !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("missing PolicyID err = %v, want ErrInvalidInput", err)
	}
	if _, err := c.CreateServerGroup(context.Background(), nil); !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("nil input err = %v, want ErrInvalidInput", err)
	}
}

func TestComputeCreateServerGroupPolicyIDShapeRejection(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("no request expected")
	}))
	for _, id := range []string{"..", ".", "a/b", "a?b"} {
		if _, err := c.CreateServerGroup(context.Background(), &CreateServerGroupInput{Name: "web", PolicyID: id}); !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Fatalf("PolicyID = %q: err = %v, want ErrInvalidInput", id, err)
		}
	}
}

func TestComputeCreateServerGroupNoIDFails(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"data":{"name":"web","policyId":"policy-1"}}`))
	}))

	_, err := c.CreateServerGroup(context.Background(), &CreateServerGroupInput{Name: "web", PolicyID: "policy-1"})
	var apiErr *core.APIError
	if !errors.As(err, &apiErr) || !strings.Contains(apiErr.Message, "list server groups") {
		t.Fatalf("err = %v, want an APIError naming list server groups before creating again", err)
	}
}

func TestComputeCreateServerGroupNoRetryAfter502(t *testing.T) {
	var calls atomic.Int64
	c := New(testutil.NewRetryConfig(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadGateway)
	})))

	_, err := c.CreateServerGroup(context.Background(), &CreateServerGroupInput{Name: "web", PolicyID: "policy-1"})
	if err == nil {
		t.Fatal("err = nil, want an error")
	}
	if calls.Load() != 1 {
		t.Fatalf("POST calls = %d, want 1: a create must never be retried after a 5xx", calls.Load())
	}
	if !strings.Contains(err.Error(), "list server groups") {
		t.Fatalf("err = %v, want a hint to list server groups before creating again", err)
	}
}

func TestComputeCreateServerGroupDuplicateNameNotWrapped(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"message":"name must be unique"}`))
	}))

	_, err := c.CreateServerGroup(context.Background(), &CreateServerGroupInput{Name: "web", PolicyID: "policy-1"})
	var apiErr *core.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v, want *core.APIError", err)
	}
	if strings.Contains(err.Error(), "list server groups") {
		t.Fatalf("a 4xx error should not get the ambiguous-create hint: %v", err)
	}
}

// --- UpdateServerGroup ---

func TestComputeUpdateServerGroupResendsUnsetField(t *testing.T) {
	cases := []struct {
		name string
		in   *UpdateServerGroupInput
		want map[string]any
	}{
		{
			name: "name only resends current description",
			in:   &UpdateServerGroupInput{ServerGroupID: "server-group-1", Name: vngcloud.Ptr("new-name")},
			want: map[string]any{"name": "new-name", "description": "current description"},
		},
		{
			name: "description only resends current name",
			in:   &UpdateServerGroupInput{ServerGroupID: "server-group-1", Description: vngcloud.Ptr("new description")},
			want: map[string]any{"name": "current-name", "description": "new description"},
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			var getCalls atomic.Int64
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.Method {
				case http.MethodGet:
					n := getCalls.Add(1)
					w.Header().Set("Content-Type", "application/json")
					if n == 1 {
						// The pre-write read: the group's current values.
						_, _ = w.Write([]byte(serverGroupBody("current-name", "current description")))
						return
					}
					// The post-write confirm read: the values the PUT sent.
					_, _ = w.Write([]byte(serverGroupBody(tt.want["name"].(string), tt.want["description"].(string))))
				case http.MethodPut:
					body := decodeServerGroupBody(t, r)
					if body["name"] != tt.want["name"] || body["description"] != tt.want["description"] {
						t.Fatalf("body = %+v, want %+v", body, tt.want)
					}
					if body["serverGroupId"] != "server-group-1" {
						t.Fatalf("body = %+v, want serverGroupId set to the path id server-group-1", body)
					}
					w.WriteHeader(http.StatusOK)
				default:
					t.Fatalf("unexpected method %s", r.Method)
				}
			}))

			out, err := c.UpdateServerGroup(context.Background(), tt.in)
			if err != nil {
				t.Fatalf("UpdateServerGroup() error = %v", err)
			}
			if out.ServerGroup.Name != tt.want["name"] || out.ServerGroup.Description != tt.want["description"] {
				t.Fatalf("unexpected server group: %+v", out.ServerGroup)
			}
			if getCalls.Load() != 2 {
				t.Fatalf("GET calls = %d, want 2 (pre-read and post-write confirm)", getCalls.Load())
			}
		})
	}
}

func TestComputeUpdateServerGroupEmptyInputFails(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("no request expected")
	}))
	_, err := c.UpdateServerGroup(context.Background(), &UpdateServerGroupInput{ServerGroupID: "server-group-1"})
	if !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
}

// TestComputeUpdateServerGroupEmptyNameFails checks that a Name set to the
// empty string is refused before any request, the same as leaving both
// fields nil: the API would otherwise happily rename the group to nothing.
func TestComputeUpdateServerGroupEmptyNameFails(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("no request expected")
	}))
	_, err := c.UpdateServerGroup(context.Background(), &UpdateServerGroupInput{
		ServerGroupID: "server-group-1",
		Name:          vngcloud.Ptr(""),
	})
	if !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
}

// TestComputeUpdateServerGroupPUTBadRequestSurfacesAPIError checks that a
// 400 from the PUT itself, such as a rename to a name another group
// already holds, comes back as the server's own *core.APIError rather than
// being folded into ErrNotSettled or some other sentinel.
func TestComputeUpdateServerGroupPUTBadRequestSurfacesAPIError(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(serverGroupBody("old-name", "d")))
		case http.MethodPut:
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"message":"name must be unique"}`))
		default:
			t.Fatalf("unexpected method %s", r.Method)
		}
	}))

	_, err := c.UpdateServerGroup(context.Background(), &UpdateServerGroupInput{
		ServerGroupID: "server-group-1",
		Name:          vngcloud.Ptr("duplicate-name"),
	})
	var apiErr *core.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v, want *core.APIError", err)
	}
	if apiErr.StatusCode != http.StatusBadRequest {
		t.Fatalf("StatusCode = %d, want 400", apiErr.StatusCode)
	}
	if errors.Is(err, ErrNotSettled) {
		t.Fatalf("err = %v, must not wrap ErrNotSettled: the PUT itself was refused", err)
	}
}

func TestComputeUpdateServerGroupReReadFailureWrapsNotSettled(t *testing.T) {
	var getCalls atomic.Int64
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			n := getCalls.Add(1)
			if n == 1 {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(serverGroupBody("old", "d")))
				return
			}
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"message":"boom"}`))
		case http.MethodPut:
			w.WriteHeader(http.StatusOK)
		default:
			t.Fatalf("unexpected method %s", r.Method)
		}
	}))

	out, err := c.UpdateServerGroup(context.Background(), &UpdateServerGroupInput{
		ServerGroupID: "server-group-1",
		Name:          vngcloud.Ptr("new"),
	})
	if !errors.Is(err, ErrNotSettled) {
		t.Fatalf("err = %v, want ErrNotSettled", err)
	}
	if out == nil || out.ServerGroup.Name != "new" {
		t.Fatalf("out = %+v, want a fallback Output carrying the sent Name", out)
	}
}

func TestComputeUpdateServerGroupGetFails(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))

	_, err := c.UpdateServerGroup(context.Background(), &UpdateServerGroupInput{
		ServerGroupID: "server-group-1",
		Name:          vngcloud.Ptr("new"),
	})
	if !vngcloud.IsNotFound(err) {
		t.Fatalf("err = %v, want NotFound", err)
	}
}

// --- DeleteServerGroup ---

func TestComputeDeleteServerGroupInUseByServersRefused(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v2/project-1/serverGroups":
			_, _ = w.Write([]byte(`{"listData":[{"uuid":"server-group-1","name":"web","servers":[{"uuid":"server-1","name":"app"}]}],"page":1,"pageSize":10000,"totalPage":1,"totalItem":1}`))
		default:
			t.Fatalf("unexpected request: %s %s (delete must send no DELETE)", r.Method, r.URL.Path)
		}
	}))

	_, err := c.DeleteServerGroup(context.Background(), &DeleteServerGroupInput{ServerGroupID: "server-group-1"})
	if !errors.Is(err, ErrServerGroupInUse) {
		t.Fatalf("err = %v, want ErrServerGroupInUse", err)
	}
}

func TestComputeDeleteServerGroupServerRefusalWraps(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusConflict} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.Method {
				case http.MethodGet:
					_, _ = w.Write([]byte(`{"listData":[],"page":1,"pageSize":10000,"totalPage":1,"totalItem":0}`))
				case http.MethodDelete:
					w.WriteHeader(status)
					_, _ = w.Write([]byte(`{"message":"server group is in use"}`))
				default:
					t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
				}
			}))

			_, err := c.DeleteServerGroup(context.Background(), &DeleteServerGroupInput{ServerGroupID: "server-group-1"})
			if !errors.Is(err, ErrServerGroupInUse) {
				t.Fatalf("err = %v, want ErrServerGroupInUse", err)
			}
		})
	}
}

func TestComputeDeleteServerGroupSuccess(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`{"listData":[],"page":1,"pageSize":10000,"totalPage":1,"totalItem":0}`))
		case r.Method == http.MethodDelete && r.URL.Path == "/v2/project-1/serverGroups/server-group-1":
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))

	if _, err := c.DeleteServerGroup(context.Background(), &DeleteServerGroupInput{ServerGroupID: "server-group-1"}); err != nil {
		t.Fatalf("DeleteServerGroup() error = %v", err)
	}
}

func TestComputeDeleteServerGroupUnknownGroupReturnsNotFound(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			_, _ = w.Write([]byte(`{"listData":[],"page":1,"pageSize":10000,"totalPage":1,"totalItem":0}`))
		case http.MethodDelete:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"not found"}`))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))

	_, err := c.DeleteServerGroup(context.Background(), &DeleteServerGroupInput{ServerGroupID: "server-group-x"})
	if !vngcloud.IsNotFound(err) {
		t.Fatalf("IsNotFound(err) = false, err = %v", err)
	}
}

func TestComputeDeleteServerGroupListFails(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))

	_, err := c.DeleteServerGroup(context.Background(), &DeleteServerGroupInput{ServerGroupID: "server-group-1"})
	if err == nil {
		t.Fatal("err = nil, want an error")
	}
}

// --- Path ID checks ---

func TestServerGroupPathIDRejection(t *testing.T) {
	badIDs := []string{"..", ".", "/", "?", ""}

	for _, id := range badIDs {
		t.Run("id="+id, func(t *testing.T) {
			c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				t.Fatal("no request expected for a malformed path ID")
			}))

			if _, err := c.GetServerGroup(context.Background(), &GetServerGroupInput{ServerGroupID: id}); !errors.Is(err, vngcloud.ErrInvalidInput) {
				t.Errorf("GetServerGroup() err = %v, want ErrInvalidInput", err)
			}
			if _, err := c.UpdateServerGroup(context.Background(), &UpdateServerGroupInput{ServerGroupID: id, Name: vngcloud.Ptr("x")}); !errors.Is(err, vngcloud.ErrInvalidInput) {
				t.Errorf("UpdateServerGroup() err = %v, want ErrInvalidInput", err)
			}
			if _, err := c.DeleteServerGroup(context.Background(), &DeleteServerGroupInput{ServerGroupID: id}); !errors.Is(err, vngcloud.ErrInvalidInput) {
				t.Errorf("DeleteServerGroup() err = %v, want ErrInvalidInput", err)
			}
		})
	}
}
