package network

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/testutil"
)

func decodeBody(t *testing.T, r *http.Request) map[string]any {
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

// withInstantSleep replaces c's sleep and now with fakes that never really
// wait, so a test exercising the group wait's full bound runs in
// milliseconds rather than the real pollBound. The fake clock advances by
// exactly the duration each sleep call is asked to wait, so the wait's
// bound is still reached after the same number of iterations a real clock
// would take. It still reports ctx's own error from sleep, so a
// canceled-context test still behaves correctly.
func withInstantSleep(c *Client) *Client {
	clock := time.Now()
	c.now = func() time.Time { return clock }
	c.sleep = func(ctx context.Context, d time.Duration) error {
		clock = clock.Add(d)
		return ctx.Err()
	}
	return c
}

// groupBody builds a {"data": {...}} security group envelope for group
// "secg-1", matching what GetSecurityGroup decodes.
func groupBody(name, description, status string, system bool) string {
	var fixture struct {
		Data struct {
			ID          string `json:"id"`
			Name        string `json:"name"`
			Description string `json:"description"`
			Status      string `json:"status"`
			System      bool   `json:"system"`
		} `json:"data"`
	}
	fixture.Data.ID = "secg-1"
	fixture.Data.Name = name
	fixture.Data.Description = description
	fixture.Data.Status = status
	fixture.Data.System = system
	b, err := json.Marshal(fixture)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// --- CreateSecurityGroup ---

func TestCreateSecurityGroupRequestBody(t *testing.T) {
	cases := []struct {
		name string
		in   *CreateSecurityGroupInput
		want map[string]any
	}{
		{
			name: "with description",
			in:   &CreateSecurityGroupInput{Name: "web", Description: "web tier", NoWait: true},
			want: map[string]any{"name": "web", "description": "web tier"},
		},
		{
			name: "without description",
			in:   &CreateSecurityGroupInput{Name: "web", NoWait: true},
			want: map[string]any{"name": "web", "description": ""},
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/v2/project-1/secgroups" {
					t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
				}
				body := decodeBody(t, r)
				if body["name"] != tt.want["name"] || body["description"] != tt.want["description"] {
					t.Fatalf("body = %+v, want %+v", body, tt.want)
				}
				w.WriteHeader(http.StatusCreated)
				_, _ = w.Write([]byte(`{"data":{"id":101,"uuid":"secg-1","secgroupName":"web"}}`))
			}))

			out, err := c.CreateSecurityGroup(context.Background(), tt.in)
			if err != nil {
				t.Fatalf("CreateSecurityGroup() error = %v", err)
			}
			if out.SecurityGroup.ID != "secg-1" {
				t.Fatalf("ID = %q, want secg-1", out.SecurityGroup.ID)
			}
		})
	}
}

func TestCreateSecurityGroupDecodesFixture(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected method %s", r.Method)
		}
		w.WriteHeader(http.StatusCreated)
		testutil.WriteFixture(t, w, "../testdata/network/create_security_group.json")
	}))

	out, err := c.CreateSecurityGroup(context.Background(), &CreateSecurityGroupInput{Name: "<name>", Description: "<description>", NoWait: true})
	if err != nil {
		t.Fatalf("CreateSecurityGroup() error = %v", err)
	}
	if out.SecurityGroup.ID != "secg-1" {
		t.Fatalf("ID = %q, want secg-1", out.SecurityGroup.ID)
	}
	if out.SecurityGroup.Name != "<name>" {
		t.Fatalf("Name = %q, want <name>", out.SecurityGroup.Name)
	}
	if out.SecurityGroup.Description != "<description>" {
		t.Fatalf("Description = %q, want <description>", out.SecurityGroup.Description)
	}
}

func TestCreateSecurityGroupNoIDFails(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"data":{"id":101,"secgroupName":"web"}}`))
	}))

	_, err := c.CreateSecurityGroup(context.Background(), &CreateSecurityGroupInput{Name: "web", NoWait: true})
	var apiErr *core.APIError
	if !errors.As(err, &apiErr) || apiErr.Message != "create response had no id" {
		t.Fatalf("err = %v, want an APIError saying the create response had no id", err)
	}
}

func TestCreateSecurityGroupNoRetryAfter502(t *testing.T) {
	var calls atomic.Int64
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"message":"upstream error"}`))
	}))

	_, err := c.CreateSecurityGroup(context.Background(), &CreateSecurityGroupInput{Name: "web", NoWait: true})
	if err == nil {
		t.Fatal("err = nil, want an error")
	}
	if calls.Load() != 1 {
		t.Fatalf("POST calls = %d, want 1: a create must never be retried after a 5xx", calls.Load())
	}
	if !strings.Contains(err.Error(), "list") {
		t.Fatalf("err = %v, want a hint to list security groups before creating again", err)
	}
}

func TestCreateSecurityGroupRequiredInput(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("no request expected")
	}))
	if _, err := c.CreateSecurityGroup(context.Background(), &CreateSecurityGroupInput{}); !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
	if _, err := c.CreateSecurityGroup(context.Background(), nil); !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("nil input err = %v, want ErrInvalidInput", err)
	}
}

// --- CreateSecurityGroup's post-create wait ---

func TestCreateSecurityGroupWaitSettlesToActive(t *testing.T) {
	var getCalls atomic.Int64
	c := withInstantSleep(newTestClient(t, scriptedSecurityGroupGets(t, []string{
		groupBody("web", "d", "CREATING", false),
		groupBody("web", "d", "CREATING", false),
		groupBody("web", "d", "ACTIVE", false),
	}, func(w http.ResponseWriter, r *http.Request) {
		getCalls.Add(1)
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"data":{"id":101,"uuid":"secg-1","secgroupName":"web"}}`))
		}
	})))

	out, err := c.CreateSecurityGroup(context.Background(), &CreateSecurityGroupInput{Name: "web", Description: "d"})
	if err != nil {
		t.Fatalf("CreateSecurityGroup() error = %v", err)
	}
	if out.SecurityGroup.Status != "ACTIVE" {
		t.Fatalf("Status = %q, want ACTIVE", out.SecurityGroup.Status)
	}
}

func TestCreateSecurityGroupWaitErrFailed(t *testing.T) {
	c := withInstantSleep(newTestClient(t, scriptedSecurityGroupGets(t, []string{
		groupBody("web", "d", "CREATING", false),
		groupBody("web", "d", "ERROR", false),
	}, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"data":{"id":101,"uuid":"secg-1","secgroupName":"web"}}`))
		}
	})))

	out, err := c.CreateSecurityGroup(context.Background(), &CreateSecurityGroupInput{Name: "web"})
	if !errors.Is(err, ErrFailed) {
		t.Fatalf("err = %v, want ErrFailed", err)
	}
	if out == nil || out.SecurityGroup.Status != "ERROR" {
		t.Fatalf("out = %+v, want a non-nil Output holding the ERROR group", out)
	}
}

func TestCreateSecurityGroupWaitTolerates404(t *testing.T) {
	var getCalls atomic.Int64
	c := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"data":{"id":101,"uuid":"secg-1","secgroupName":"web"}}`))
		case http.MethodGet:
			n := getCalls.Add(1)
			if n == 1 {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"message":"not found"}`))
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(groupBody("web", "d", "ACTIVE", false)))
		default:
			t.Fatalf("unexpected method %s", r.Method)
		}
	})))

	out, err := c.CreateSecurityGroup(context.Background(), &CreateSecurityGroupInput{Name: "web"})
	if err != nil {
		t.Fatalf("CreateSecurityGroup() error = %v", err)
	}
	if out.SecurityGroup.Status != "ACTIVE" {
		t.Fatalf("Status = %q, want ACTIVE", out.SecurityGroup.Status)
	}
	if getCalls.Load() < 2 {
		t.Fatalf("GET calls = %d, want at least 2: a 404 during the wait must keep polling", getCalls.Load())
	}
}

func TestCreateSecurityGroupWaitBoundErrNotSettled(t *testing.T) {
	c := withInstantSleep(newTestClient(t, scriptedSecurityGroupGets(t, []string{
		groupBody("web", "d", "CREATING", false),
	}, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"data":{"id":101,"uuid":"secg-1","secgroupName":"web"}}`))
		}
	})))

	out, err := c.CreateSecurityGroup(context.Background(), &CreateSecurityGroupInput{Name: "web"})
	if !errors.Is(err, ErrNotSettled) {
		t.Fatalf("err = %v, want ErrNotSettled", err)
	}
	if out == nil || out.SecurityGroup.ID != "secg-1" {
		t.Fatalf("out = %+v, want a non-nil Output carrying the group id", out)
	}
}

func TestCreateSecurityGroupWaitPollSpacing(t *testing.T) {
	var sleeps []time.Duration
	c := newTestClient(t, scriptedSecurityGroupGets(t, []string{
		groupBody("web", "d", "CREATING", false),
		groupBody("web", "d", "CREATING", false),
		groupBody("web", "d", "ACTIVE", false),
	}, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"data":{"id":101,"uuid":"secg-1","secgroupName":"web"}}`))
		}
	}))
	clock := time.Now()
	c.now = func() time.Time { return clock }
	c.sleep = func(ctx context.Context, d time.Duration) error {
		sleeps = append(sleeps, d)
		clock = clock.Add(d)
		return ctx.Err()
	}

	if _, err := c.CreateSecurityGroup(context.Background(), &CreateSecurityGroupInput{Name: "web"}); err != nil {
		t.Fatalf("CreateSecurityGroup() error = %v", err)
	}
	for _, d := range sleeps {
		if d != pollInterval {
			t.Fatalf("sleep duration = %s, want %s", d, pollInterval)
		}
	}
	if len(sleeps) == 0 {
		t.Fatal("no sleep calls recorded")
	}
}

func TestCreateSecurityGroupWaitCancelDuringSleep(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"data":{"id":101,"uuid":"secg-1","secgroupName":"web"}}`))
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(groupBody("web", "d", "CREATING", false)))
		default:
			t.Fatalf("unexpected method %s", r.Method)
		}
	}))
	c.sleep = func(ctx context.Context, _ time.Duration) error {
		cancel()
		return ctx.Err()
	}

	out, err := c.CreateSecurityGroup(ctx, &CreateSecurityGroupInput{Name: "web"})
	if !errors.Is(err, ErrNotSettled) {
		t.Fatalf("err = %v, want ErrNotSettled", err)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if out == nil || out.SecurityGroup.ID != "secg-1" {
		t.Fatalf("out = %+v, want a non-nil Output carrying the group id", out)
	}
}

func TestCreateSecurityGroupNoWaitSkipsPoll(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected method %s: NoWait must not poll", r.Method)
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"data":{"id":101,"uuid":"secg-1","secgroupName":"web"}}`))
	}))

	out, err := c.CreateSecurityGroup(context.Background(), &CreateSecurityGroupInput{Name: "web", NoWait: true})
	if err != nil {
		t.Fatalf("CreateSecurityGroup() error = %v", err)
	}
	if out.SecurityGroup.ID != "secg-1" || out.SecurityGroup.Name != "web" {
		t.Fatalf("unexpected group: %+v", out.SecurityGroup)
	}
}

// scriptedSecurityGroupGets serves the strings in bodies, each a 200
// response, to successive GET requests in order, repeating the last one
// once they run out, and delegates every other method to other.
func scriptedSecurityGroupGets(t *testing.T, bodies []string, other http.HandlerFunc) http.Handler {
	t.Helper()
	var n atomic.Int64
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			if other == nil {
				t.Fatalf("unexpected %s request", r.Method)
			}
			other(w, r)
			return
		}
		i := int(n.Add(1)) - 1
		if i >= len(bodies) {
			i = len(bodies) - 1
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(bodies[i]))
	})
}

// --- UpdateSecurityGroup ---

func TestUpdateSecurityGroupResendsUnsetField(t *testing.T) {
	cases := []struct {
		name string
		in   *UpdateSecurityGroupInput
		want map[string]any
	}{
		{
			name: "name only resends current description",
			in:   &UpdateSecurityGroupInput{SecurityGroupID: "secg-1", Name: vngcloud.Ptr("new-name")},
			want: map[string]any{"name": "new-name", "description": "current description"},
		},
		{
			name: "description only resends current name",
			in:   &UpdateSecurityGroupInput{SecurityGroupID: "secg-1", Description: vngcloud.Ptr("new description")},
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
						_, _ = w.Write([]byte(groupBody("current-name", "current description", "ACTIVE", false)))
						return
					}
					// The post-write confirm read: the values the PUT sent.
					_, _ = w.Write([]byte(groupBody(tt.want["name"].(string), tt.want["description"].(string), "ACTIVE", false)))
				case http.MethodPut:
					body := decodeBody(t, r)
					if body["name"] != tt.want["name"] || body["description"] != tt.want["description"] {
						t.Fatalf("body = %+v, want %+v", body, tt.want)
					}
					w.WriteHeader(http.StatusOK)
				default:
					t.Fatalf("unexpected method %s", r.Method)
				}
			}))

			out, err := c.UpdateSecurityGroup(context.Background(), tt.in)
			if err != nil {
				t.Fatalf("UpdateSecurityGroup() error = %v", err)
			}
			if out.SecurityGroup.Name != tt.want["name"] || out.SecurityGroup.Description != tt.want["description"] {
				t.Fatalf("unexpected group: %+v", out.SecurityGroup)
			}
			if getCalls.Load() != 2 {
				t.Fatalf("GET calls = %d, want 2 (pre-read and post-write confirm)", getCalls.Load())
			}
		})
	}
}

func TestUpdateSecurityGroupEmptyInputFails(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("no request expected")
	}))
	_, err := c.UpdateSecurityGroup(context.Background(), &UpdateSecurityGroupInput{SecurityGroupID: "secg-1"})
	if !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
}

func TestUpdateSecurityGroupSystemGroupRefused(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("unexpected method %s: a system group update must send no PUT", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(groupBody("default", "d", "ACTIVE", true)))
	}))

	_, err := c.UpdateSecurityGroup(context.Background(), &UpdateSecurityGroupInput{
		SecurityGroupID: "secg-1",
		Name:            vngcloud.Ptr("renamed"),
	})
	if !errors.Is(err, ErrSystemGroup) {
		t.Fatalf("err = %v, want ErrSystemGroup", err)
	}
}

func TestUpdateSecurityGroupReReadFailureWrapsNotSettled(t *testing.T) {
	var getCalls atomic.Int64
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			n := getCalls.Add(1)
			if n == 1 {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(groupBody("old", "d", "ACTIVE", false)))
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

	out, err := c.UpdateSecurityGroup(context.Background(), &UpdateSecurityGroupInput{
		SecurityGroupID: "secg-1",
		Name:            vngcloud.Ptr("new"),
	})
	if !errors.Is(err, ErrNotSettled) {
		t.Fatalf("err = %v, want ErrNotSettled", err)
	}
	if out == nil || out.SecurityGroup.Name != "new" {
		t.Fatalf("out = %+v, want a fallback Output carrying the sent Name", out)
	}
}

// --- DeleteSecurityGroup ---

func TestDeleteSecurityGroupSystemGroupRefused(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("unexpected method %s: a system group delete must send no DELETE", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(groupBody("default", "d", "ACTIVE", true)))
	}))

	_, err := c.DeleteSecurityGroup(context.Background(), &DeleteSecurityGroupInput{SecurityGroupID: "secg-1"})
	if !errors.Is(err, ErrSystemGroup) {
		t.Fatalf("err = %v, want ErrSystemGroup", err)
	}
}

func TestDeleteSecurityGroupInUseByServersRefused(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v2/project-1/secgroups/secg-1":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(groupBody("web", "d", "ACTIVE", false)))
		case r.Method == http.MethodGet && r.URL.Path == "/v2/project-1/secgroups/secg-1/servers":
			_, _ = w.Write([]byte(`{"data":[{"uuid":"server-1","status":"ACTIVE"}]}`))
		default:
			t.Fatalf("unexpected request: %s %s (delete must send no DELETE)", r.Method, r.URL.Path)
		}
	}))

	_, err := c.DeleteSecurityGroup(context.Background(), &DeleteSecurityGroupInput{SecurityGroupID: "secg-1"})
	if !errors.Is(err, ErrSecurityGroupInUse) {
		t.Fatalf("err = %v, want ErrSecurityGroupInUse", err)
	}
}

func TestDeleteSecurityGroupServerRefusalWraps(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusConflict} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/secg-1"):
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(groupBody("web", "d", "ACTIVE", false)))
				case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/servers"):
					_, _ = w.Write([]byte(`{"data":[]}`))
				case r.Method == http.MethodDelete:
					w.WriteHeader(status)
					_, _ = w.Write([]byte(`{"message":"SecurityGroupInUse: group is attached to a resource"}`))
				default:
					t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
				}
			}))

			_, err := c.DeleteSecurityGroup(context.Background(), &DeleteSecurityGroupInput{SecurityGroupID: "secg-1"})
			if !errors.Is(err, ErrSecurityGroupInUse) {
				t.Fatalf("err = %v, want ErrSecurityGroupInUse", err)
			}
		})
	}
}

func TestDeleteSecurityGroupSuccess(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/secg-1"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(groupBody("web", "d", "ACTIVE", false)))
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/servers"):
			_, _ = w.Write([]byte(`{"data":[]}`))
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))

	if _, err := c.DeleteSecurityGroup(context.Background(), &DeleteSecurityGroupInput{SecurityGroupID: "secg-1"}); err != nil {
		t.Fatalf("DeleteSecurityGroup() error = %v", err)
	}
}

func TestDeleteSecurityGroupUnknownGroupReturnsNotFound(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"not found"}`))
	}))

	_, err := c.DeleteSecurityGroup(context.Background(), &DeleteSecurityGroupInput{SecurityGroupID: "secg-x"})
	if !vngcloud.IsNotFound(err) {
		t.Fatalf("IsNotFound(err) = false, err = %v", err)
	}
}

// --- Path ID checks ---

func TestSecurityGroupPathIDRejection(t *testing.T) {
	badIDs := []string{"..", ".", "a/b", "a?b", ""}

	for _, id := range badIDs {
		t.Run("id="+id, func(t *testing.T) {
			c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				t.Fatal("no request expected for a malformed path ID")
			}))

			if _, err := c.GetSecurityGroup(context.Background(), &GetSecurityGroupInput{SecurityGroupID: id}); !errors.Is(err, vngcloud.ErrInvalidInput) {
				t.Errorf("GetSecurityGroup() err = %v, want ErrInvalidInput", err)
			}
			if _, err := c.ListServersBySecurityGroup(context.Background(), &ListServersBySecurityGroupInput{SecurityGroupID: id}); !errors.Is(err, vngcloud.ErrInvalidInput) {
				t.Errorf("ListServersBySecurityGroup() err = %v, want ErrInvalidInput", err)
			}
			if _, err := c.ListSecurityGroupRules(context.Background(), &ListSecurityGroupRulesInput{SecurityGroupID: id}); !errors.Is(err, vngcloud.ErrInvalidInput) {
				t.Errorf("ListSecurityGroupRules() err = %v, want ErrInvalidInput", err)
			}
			if _, err := c.UpdateSecurityGroup(context.Background(), &UpdateSecurityGroupInput{SecurityGroupID: id, Name: vngcloud.Ptr("x")}); !errors.Is(err, vngcloud.ErrInvalidInput) {
				t.Errorf("UpdateSecurityGroup() err = %v, want ErrInvalidInput", err)
			}
			if _, err := c.DeleteSecurityGroup(context.Background(), &DeleteSecurityGroupInput{SecurityGroupID: id}); !errors.Is(err, vngcloud.ErrInvalidInput) {
				t.Errorf("DeleteSecurityGroup() err = %v, want ErrInvalidInput", err)
			}
		})
	}
}
