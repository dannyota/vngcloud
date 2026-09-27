package iam

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/endpoints"
	"danny.vn/vngcloud/internal/testutil"
	"danny.vn/vngcloud/internal/transport"
)

func TestCreateGroupRequestBodyAndResponse(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			testutil.WriteFixture(t, w, "../testdata/iam/get_group.json")
			return
		}
		if r.URL.Path != "/policies-api/v1/groups" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		var raw map[string]any
		if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
			t.Fatal(err)
		}
		if raw["name"] != "app-readers" || raw["description"] != "read only" || raw["mode"] != "iam" {
			t.Fatalf("unexpected body: %+v", raw)
		}
		if _, ok := raw["iamUsers"]; ok {
			t.Fatalf("body sends iamUsers, want it omitted: %+v", raw)
		}
		if _, ok := raw["policies"]; ok {
			t.Fatalf("body sends policies, want it omitted: %+v", raw)
		}
		w.WriteHeader(http.StatusCreated)
		testutil.WriteFixture(t, w, "../testdata/iam/create_group.json")
	}))

	out, err := c.CreateGroup(context.Background(), &CreateGroupInput{Name: "app-readers", Description: "read only"})
	if err != nil {
		t.Fatalf("CreateGroup() error = %v", err)
	}
	if out.Group.ID != "group-1" {
		t.Fatalf("unexpected group: %+v", out.Group)
	}
}

func TestCreateGroupMissingID(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{}`))
	}))
	if _, err := c.CreateGroup(context.Background(), &CreateGroupInput{Name: "app"}); err == nil {
		t.Fatal("CreateGroup() error = nil, want an error for a response with no id")
	}
}

// TestCreateGroupReadFailureKeepsOutput checks that a failed confirm read
// after a landed create wraps ErrNotSettled and keeps a non-nil Output
// holding the create response's own id, mirroring
// TestCreatePolicyReadFailureKeepsOutput.
func TestCreateGroupReadFailureKeepsOutput(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":"group-1"}`))
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	out, err := c.CreateGroup(context.Background(), &CreateGroupInput{Name: "app"})
	if !errors.Is(err, ErrNotSettled) {
		t.Fatalf("CreateGroup() err = %v, want ErrNotSettled", err)
	}
	if out == nil {
		t.Fatal("Output = nil, want the create's id kept despite the failed read-back")
	}
	if out.Group.ID != "group-1" {
		t.Fatalf("Output.Group.ID = %q, want group-1", out.Group.ID)
	}
}

func TestCreateGroupDuplicateNameNotWrapped(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"message":"name already exists"}`))
	}))
	_, err := c.CreateGroup(context.Background(), &CreateGroupInput{Name: "app"})
	var apiErr *vngcloud.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v, want *vngcloud.APIError", err)
	}
	if strings.Contains(err.Error(), "list-groups") {
		t.Fatalf("a 4xx error should not get the ambiguous-create hint: %v", err)
	}
}

func TestCreateGroupWriteStatuses(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusForbidden, http.StatusConflict, http.StatusInternalServerError} {
		c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(status)
		}))
		if _, err := c.CreateGroup(context.Background(), &CreateGroupInput{Name: "app"}); err == nil {
			t.Fatalf("status %d: CreateGroup() error = nil, want an error", status)
		}
	}
}

func TestCreateGroupNoRetryOn502(t *testing.T) {
	var calls int32
	c := New(testutil.NewRetryConfig(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusBadGateway)
	})))
	_, err := c.CreateGroup(context.Background(), &CreateGroupInput{Name: "app"})
	if err == nil {
		t.Fatal("CreateGroup() error = nil, want an error")
	}
	if calls != 1 {
		t.Fatalf("server received %d request(s), want 1 (no retry after 502)", calls)
	}
	if !strings.Contains(err.Error(), "list-groups") {
		t.Fatalf("error does not name list-groups: %v", err)
	}
}

// TestCreateGroupNoResendAfter401 checks that Once keeps the create POST
// from being resent after a 401, which would otherwise create a second
// group.
func TestCreateGroupNoResendAfter401(t *testing.T) {
	var calls atomic.Int32
	c := newOnceTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	if _, err := c.CreateGroup(context.Background(), &CreateGroupInput{Name: "app"}); err == nil {
		t.Fatal("CreateGroup() error = nil, want an error")
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("calls = %d, want 1 (no resend after 401)", got)
	}
}

// TestCreateGroupNoFollowRedirect307 checks that Once keeps the create POST
// from following a 307, which would otherwise create a second group.
func TestCreateGroupNoFollowRedirect307(t *testing.T) {
	var calls atomic.Int32
	c := newOnceTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Location", r.URL.String())
		w.WriteHeader(http.StatusTemporaryRedirect)
	}))
	if _, err := c.CreateGroup(context.Background(), &CreateGroupInput{Name: "app"}); err == nil {
		t.Fatal("CreateGroup() error = nil, want an error")
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("calls = %d, want 1 (no redirect followed)", got)
	}
}

// TestUpdateGroupFillsNameFromReadWhenNil checks that a nil Name reads the
// group first and sends its current name, because the PATCH requires one.
func TestUpdateGroupFillsNameFromReadWhenNil(t *testing.T) {
	var getCalled bool
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			getCalled = true
			testutil.WriteFixture(t, w, "../testdata/iam/get_group.json")
		case http.MethodPatch:
			var body updateGroupBody
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body.Name != "<name>" {
				t.Fatalf("Name = %q, want it filled from the read", body.Name)
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected method: %s", r.Method)
		}
	}))
	if _, err := c.UpdateGroup(context.Background(), &UpdateGroupInput{GroupID: "group-1", Description: vngcloud.Ptr("x")}); err != nil {
		t.Fatalf("UpdateGroup() error = %v", err)
	}
	if !getCalled {
		t.Fatal("GetGroup was never called to fill in the nil Name")
	}
}

// TestUpdateGroupSendsGivenNameWithoutRead checks that a non-nil Name skips
// the read entirely.
func TestUpdateGroupSendsGivenNameWithoutRead(t *testing.T) {
	var gets int32
	// The confirm read after the PATCH still runs, so exactly one GET is
	// expected; a second would mean the pre-write read that fills in a nil
	// Name ran even though Name was given.
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPatch {
			var body updateGroupBody
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body.Name != "renamed" {
				t.Fatalf("Name = %q, want renamed", body.Name)
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		atomic.AddInt32(&gets, 1)
		testutil.WriteFixture(t, w, "../testdata/iam/get_group.json")
	}))
	if _, err := c.UpdateGroup(context.Background(), &UpdateGroupInput{GroupID: "group-1", Name: vngcloud.Ptr("renamed")}); err != nil {
		t.Fatalf("UpdateGroup() error = %v", err)
	}
	if gets != 1 {
		t.Fatalf("GetGroup was called %d times, want 1 (only the confirm read after PATCH)", gets)
	}
}

// TestUpdateGroupDescriptionOmittedWhenNil checks that a nil Description is
// left out of the PATCH body entirely, rather than sent as an empty string.
func TestUpdateGroupDescriptionOmittedWhenNil(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPatch {
			var raw map[string]any
			if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
				t.Fatal(err)
			}
			if _, ok := raw["description"]; ok {
				t.Fatalf("body sends description, want it omitted: %+v", raw)
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		testutil.WriteFixture(t, w, "../testdata/iam/get_group.json")
	}))
	if _, err := c.UpdateGroup(context.Background(), &UpdateGroupInput{GroupID: "group-1", Name: vngcloud.Ptr("app")}); err != nil {
		t.Fatalf("UpdateGroup() error = %v", err)
	}
}

func TestUpdateGroupWriteStatuses(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusForbidden, http.StatusNotFound, http.StatusConflict, http.StatusInternalServerError} {
		c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(status)
		}))
		if _, err := c.UpdateGroup(context.Background(), &UpdateGroupInput{GroupID: "group-1", Name: vngcloud.Ptr("app")}); err == nil {
			t.Fatalf("status %d: UpdateGroup() error = nil, want an error", status)
		}
	}
}

// TestUpdateGroupReadFailureKeepsOutput checks that a failed confirm read
// after a landed PATCH wraps ErrNotSettled and keeps a non-nil Output
// holding the target group's own id.
func TestUpdateGroupReadFailureKeepsOutput(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPatch {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	out, err := c.UpdateGroup(context.Background(), &UpdateGroupInput{GroupID: "group-1", Name: vngcloud.Ptr("app")})
	if !errors.Is(err, ErrNotSettled) {
		t.Fatalf("UpdateGroup() err = %v, want ErrNotSettled", err)
	}
	if out == nil {
		t.Fatal("Output = nil, want the group's id kept despite the failed confirm read")
	}
	if out.Group.ID != "group-1" {
		t.Fatalf("Output.Group.ID = %q, want group-1", out.Group.ID)
	}
}

// TestUpdateGroupRetriesOn502 checks that the PATCH, idempotent regardless
// of any request field, is retried by the transport after a 502.
func TestUpdateGroupRetriesOn502(t *testing.T) {
	var patchCalls int32
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			testutil.WriteFixture(t, w, "../testdata/iam/get_group.json")
			return
		}
		if atomic.AddInt32(&patchCalls, 1) == 1 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	server := httptest.NewServer(handler)
	defer server.Close()
	tc := transport.New(transport.Config{HTTPClient: server.Client(), RetryCount: 3, RetryInterval: time.Millisecond})
	cfg := core.NewTestConfig("hcm-3", "project-1", endpoints.Set{Region: "hcm-3", Dashboard: server.URL + "/", IAM: server.URL + "/"}, tc)
	c := New(cfg)

	if _, err := c.UpdateGroup(context.Background(), &UpdateGroupInput{GroupID: "group-1", Name: vngcloud.Ptr("app")}); err != nil {
		t.Fatalf("UpdateGroup() error = %v", err)
	}
	if patchCalls != 2 {
		t.Fatalf("server received %d PATCH request(s), want 2 (retried once after 502)", patchCalls)
	}
}

func TestDeleteGroup(t *testing.T) {
	g := unprivilegedGuardFixture()
	g.groups = map[string]Group{"group-1": {ID: "group-1"}}
	c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
		mux.HandleFunc("DELETE /policies-api/v1/groups/group-1", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		})
	})
	if _, err := c.DeleteGroup(context.Background(), &DeleteGroupInput{GroupID: "group-1"}); err != nil {
		t.Fatalf("DeleteGroup() error = %v", err)
	}
}

func TestDeleteGroupRefusedWithMember(t *testing.T) {
	g := unprivilegedGuardFixture()
	g.groups = map[string]Group{"group-1": {ID: "group-1", UserIDs: []string{"user-x"}}}
	c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
		mux.HandleFunc("DELETE /policies-api/v1/groups/group-1", func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("no write request expected")
		})
	})
	_, err := c.DeleteGroup(context.Background(), &DeleteGroupInput{GroupID: "group-1"})
	if !errors.Is(err, ErrInUse) {
		t.Fatalf("DeleteGroup() err = %v, want ErrInUse", err)
	}
}

func TestDeleteGroupRefusedWithPolicy(t *testing.T) {
	g := unprivilegedGuardFixture()
	g.groups = map[string]Group{"group-1": {ID: "group-1", PolicyIDs: []string{"policy-1"}}}
	c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
		mux.HandleFunc("DELETE /policies-api/v1/groups/group-1", func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("no write request expected")
		})
	})
	_, err := c.DeleteGroup(context.Background(), &DeleteGroupInput{GroupID: "group-1"})
	if !errors.Is(err, ErrInUse) {
		t.Fatalf("DeleteGroup() err = %v, want ErrInUse", err)
	}
}

// TestDeleteGroupRefusesWhenGetGroupAttachmentsFails checks that a group
// response missing its own "policies" or "iamUsers" field refuses the
// delete instead of reading it as empty.
func TestDeleteGroupRefusesWhenGetGroupAttachmentsFails(t *testing.T) {
	g := unprivilegedGuardFixture()
	g.groupHandler = func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"id":"group-1","iamUsers":[]}`))
	}
	c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
		mux.HandleFunc("DELETE /policies-api/v1/groups/group-1", func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("no write request expected")
		})
	})
	if _, err := c.DeleteGroup(context.Background(), &DeleteGroupInput{GroupID: "group-1"}); err == nil {
		t.Fatal("DeleteGroup() error = nil, want a refusal for a missing policies field")
	}
}

// TestDeleteGroupRefusesUnknownCallerType checks that DeleteGroup, a guarded
// write, refuses for a caller whose type cannot be classified.
func TestDeleteGroupRefusesUnknownCallerType(t *testing.T) {
	g := guardFixture{caller: userInfoResponse{UserID: "user-1", UserType: callerTypeRoot}}
	c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
		mux.HandleFunc("DELETE /policies-api/v1/groups/group-1", func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("no write request expected")
		})
	})
	if _, err := c.DeleteGroup(context.Background(), &DeleteGroupInput{GroupID: "group-1"}); !errors.Is(err, ErrPrivilegedChange) {
		t.Fatalf("DeleteGroup() err = %v, want ErrPrivilegedChange", err)
	}
}

func TestDeleteGroupWriteStatuses(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusForbidden, http.StatusNotFound, http.StatusConflict, http.StatusInternalServerError} {
		g := unprivilegedGuardFixture()
		g.groups = map[string]Group{"group-1": {ID: "group-1"}}
		c := newGuardTestClient(t, g, func(mux *http.ServeMux) {
			mux.HandleFunc("DELETE /policies-api/v1/groups/group-1", func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(status)
			})
		})
		if _, err := c.DeleteGroup(context.Background(), &DeleteGroupInput{GroupID: "group-1"}); err == nil {
			t.Fatalf("status %d: DeleteGroup() error = nil, want an error", status)
		}
	}
}
