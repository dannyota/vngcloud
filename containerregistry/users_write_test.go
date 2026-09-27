package containerregistry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
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

// --- CreateUser ---

func TestCreateUserRequestBodyAndLookup(t *testing.T) {
	var permissionsCalls, createCalls, listCalls int32
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/user/permissions":
			atomic.AddInt32(&permissionsCalls, 1)
			testutil.WriteFixture(t, w, "../testdata/containerregistry/list_permissions.json")
		case r.Method == http.MethodPost && r.URL.Path == "/v1/user":
			atomic.AddInt32(&createCalls, 1)
			body := decodeBody(t, r)
			if body["name"] != "app-ci" {
				t.Fatalf("body name = %v, want app-ci", body["name"])
			}
			if _, hasDuration := body["duration"]; hasDuration {
				t.Fatalf("body = %+v, want no duration key when DurationDays is nil", body)
			}
			perms, _ := body["permissionRequestList"].([]any)
			if len(perms) != 1 {
				t.Fatalf("permissionRequestList = %+v, want 1 entry", perms)
			}
			entry, _ := perms[0].(map[string]any)
			if entry["repoId"] != "repo-1" {
				t.Fatalf("repoId = %v, want repo-1", entry["repoId"])
			}
			ids, _ := entry["policyIdList"].([]any)
			if len(ids) != 1 || ids[0] != "policy-1" {
				t.Fatalf("policyIdList = %+v, want [policy-1]", ids)
			}
			w.WriteHeader(http.StatusOK)
			testutil.WriteFixture(t, w, "../testdata/containerregistry/create_user.json")
		case r.Method == http.MethodGet && r.URL.Path == "/v1/user":
			atomic.AddInt32(&listCalls, 1)
			if r.URL.Query().Get("name") != "app-ci" {
				t.Fatalf("list name query = %q, want app-ci", r.URL.Query().Get("name"))
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[{"uuid":"ra-1","name":"app-ci"}],"page":1,"pageSize":10000,"totalPage":1,"totalItem":1}`))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))

	out, err := c.CreateUser(context.Background(), &CreateUserInput{
		Name:        "app-ci",
		Permissions: []UserPermission{{RepositoryID: "repo-1", Actions: []string{"PULL"}}},
	})
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}
	if out.User.ID != "ra-1" || out.User.Name != "app-ci" {
		t.Fatalf("unexpected user: %+v", out.User)
	}
	if out.SecretKey.Reveal() != "<secret>" {
		t.Fatalf("SecretKey.Reveal() = %q, want the fixture value", out.SecretKey.Reveal())
	}
	if permissionsCalls != 1 || createCalls != 1 || listCalls != 1 {
		t.Fatalf("calls: permissions=%d create=%d list=%d, want 1 each", permissionsCalls, createCalls, listCalls)
	}
}

// TestCreateUserLookupNumericUserID checks that the post-create lookup
// decodes a live-shaped row, whose userId arrives as a JSON number, without
// error, and finds it by its exact name.
func TestCreateUserLookupNumericUserID(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/user/permissions":
			testutil.WriteFixture(t, w, "../testdata/containerregistry/list_permissions.json")
		case r.Method == http.MethodPost && r.URL.Path == "/v1/user":
			w.WriteHeader(http.StatusOK)
			testutil.WriteFixture(t, w, "../testdata/containerregistry/create_user.json")
		case r.Method == http.MethodGet && r.URL.Path == "/v1/user":
			if r.URL.Query().Get("name") != "app-ci" {
				t.Fatalf("list name query = %q, want app-ci", r.URL.Query().Get("name"))
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[{"uuid":"ra-1","name":"app-ci","userId":20260101}],"page":1,"pageSize":10000,"totalPage":1,"totalItem":1}`))
		}
	}))

	out, err := c.CreateUser(context.Background(), &CreateUserInput{
		Name:        "app-ci",
		Permissions: []UserPermission{{RepositoryID: "repo-1", Actions: []string{"PULL"}}},
	})
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}
	if out.User.UserID != "20260101" {
		t.Fatalf("User.UserID = %q, want %q", out.User.UserID, "20260101")
	}
}

// TestCreateUserLookupSuffixIsNotAMatch checks that a row whose name only
// ends with the input is not treated as a match: a live capture confirms
// the server applies no account prefix to a user's name, so only an exact
// name identifies the created user.
func TestCreateUserLookupSuffixIsNotAMatch(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/user/permissions":
			testutil.WriteFixture(t, w, "../testdata/containerregistry/list_permissions.json")
		case r.Method == http.MethodPost && r.URL.Path == "/v1/user":
			w.WriteHeader(http.StatusOK)
			testutil.WriteFixture(t, w, "../testdata/containerregistry/create_user.json")
		case r.Method == http.MethodGet && r.URL.Path == "/v1/user":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[{"uuid":"ra-1","name":"prefix-app-ci"}],"page":1,"pageSize":10000,"totalPage":1,"totalItem":1}`))
		}
	}))

	_, err := c.CreateUser(context.Background(), &CreateUserInput{
		Name:        "app-ci",
		Permissions: []UserPermission{{RepositoryID: "repo-1", Actions: []string{"PULL"}}},
	})
	if !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("err = %v, want ErrUserNotFound: prefix-app-ci only ends with app-ci, and the server applies no account prefix, so it is not a match", err)
	}
}

func TestCreateUserSendsDurationWhenSet(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/user/permissions":
			testutil.WriteFixture(t, w, "../testdata/containerregistry/list_permissions.json")
		case r.Method == http.MethodPost && r.URL.Path == "/v1/user":
			body := decodeBody(t, r)
			if body["duration"] != float64(90) {
				t.Fatalf("body duration = %v, want 90", body["duration"])
			}
			w.WriteHeader(http.StatusOK)
			testutil.WriteFixture(t, w, "../testdata/containerregistry/create_user.json")
		case r.Method == http.MethodGet && r.URL.Path == "/v1/user":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[{"uuid":"ra-1","name":"app-ci"}],"page":1,"pageSize":10000,"totalPage":1,"totalItem":1}`))
		}
	}))

	if _, err := c.CreateUser(context.Background(), &CreateUserInput{
		Name:         "app-ci",
		DurationDays: vngcloud.Ptr(90),
		Permissions:  []UserPermission{{RepositoryID: "repo-1", Actions: []string{"PULL"}}},
	}); err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}
}

func TestCreateUserShapeRefusals(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("no request expected")
	}))

	cases := []struct {
		name string
		in   *CreateUserInput
	}{
		{"empty name", &CreateUserInput{Permissions: []UserPermission{{RepositoryID: "repo-1", Actions: []string{"PULL"}}}}},
		{"nil permissions", &CreateUserInput{Name: "app"}},
		{"empty permission list", &CreateUserInput{Name: "app", Permissions: []UserPermission{}}},
		{"permission with no action", &CreateUserInput{Name: "app", Permissions: []UserPermission{{RepositoryID: "repo-1"}}}},
		{"bad repository id", &CreateUserInput{Name: "app", Permissions: []UserPermission{{RepositoryID: "../etc", Actions: []string{"PULL"}}}}},
		{"duration zero", &CreateUserInput{Name: "app", DurationDays: vngcloud.Ptr(0), Permissions: []UserPermission{{RepositoryID: "repo-1", Actions: []string{"PULL"}}}}},
	}
	for _, tc := range cases {
		if _, err := c.CreateUser(context.Background(), tc.in); !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Errorf("%s: err = %v, want ErrInvalidInput", tc.name, err)
		}
	}
}

// TestCreateUserUnknownActionAfterPermissionsRead covers an action absent
// from ListPermissions: the check happens only after that one read, since
// the server's own list is the authority, not a value the SDK can check
// offline.
func TestCreateUserUnknownActionAfterPermissionsRead(t *testing.T) {
	var permCalls int32
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/user/permissions":
			atomic.AddInt32(&permCalls, 1)
			testutil.WriteFixture(t, w, "../testdata/containerregistry/list_permissions.json")
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))

	_, err := c.CreateUser(context.Background(), &CreateUserInput{
		Name:        "app",
		Permissions: []UserPermission{{RepositoryID: "repo-1", Actions: []string{"DELETE_EVERYTHING"}}},
	})
	if !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
	if permCalls != 1 {
		t.Fatalf("permissions calls = %d, want 1: the unknown action must be checked against a live read", permCalls)
	}
	if !strings.Contains(err.Error(), "PULL") {
		t.Fatalf("err = %v, want it to name a known action", err)
	}
}

func TestCreateUserLookupNoMatchReturnsUserNotFound(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/user/permissions":
			testutil.WriteFixture(t, w, "../testdata/containerregistry/list_permissions.json")
		case r.Method == http.MethodPost && r.URL.Path == "/v1/user":
			w.WriteHeader(http.StatusOK)
			testutil.WriteFixture(t, w, "../testdata/containerregistry/create_user.json")
		case r.Method == http.MethodGet && r.URL.Path == "/v1/user":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[],"page":1,"pageSize":10000,"totalPage":1,"totalItem":0}`))
		}
	}))

	out, err := c.CreateUser(context.Background(), &CreateUserInput{
		Name:        "app-ci",
		Permissions: []UserPermission{{RepositoryID: "repo-1", Actions: []string{"PULL"}}},
	})
	if !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("err = %v, want ErrUserNotFound", err)
	}
	if out == nil || out.SecretKey.Reveal() != "<secret>" {
		t.Fatalf("out = %+v, want the Output to still hold the secret", out)
	}
}

func TestCreateUserLookupTwoMatchesReturnsUserNotFound(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/user/permissions":
			testutil.WriteFixture(t, w, "../testdata/containerregistry/list_permissions.json")
		case r.Method == http.MethodPost && r.URL.Path == "/v1/user":
			w.WriteHeader(http.StatusOK)
			testutil.WriteFixture(t, w, "../testdata/containerregistry/create_user.json")
		case r.Method == http.MethodGet && r.URL.Path == "/v1/user":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[{"uuid":"ra-1","name":"app-ci"},{"uuid":"ra-2","name":"app-ci"}],"page":1,"pageSize":10000,"totalPage":1,"totalItem":2}`))
		}
	}))

	out, err := c.CreateUser(context.Background(), &CreateUserInput{
		Name:        "app-ci",
		Permissions: []UserPermission{{RepositoryID: "repo-1", Actions: []string{"PULL"}}},
	})
	if !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("err = %v, want ErrUserNotFound", err)
	}
	if out == nil || out.SecretKey.Reveal() != "<secret>" {
		t.Fatalf("out = %+v, want the Output to still hold the secret", out)
	}
}

// TestCreateUserLookupSubstringOnlyIsNotAMatch covers a row whose name
// contains the input only in the middle: only an exact name counts as a
// match.
func TestCreateUserLookupSubstringOnlyIsNotAMatch(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/user/permissions":
			testutil.WriteFixture(t, w, "../testdata/containerregistry/list_permissions.json")
		case r.Method == http.MethodPost && r.URL.Path == "/v1/user":
			w.WriteHeader(http.StatusOK)
			testutil.WriteFixture(t, w, "../testdata/containerregistry/create_user.json")
		case r.Method == http.MethodGet && r.URL.Path == "/v1/user":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[{"uuid":"ra-1","name":"app-ci-2"}],"page":1,"pageSize":10000,"totalPage":1,"totalItem":1}`))
		}
	}))

	_, err := c.CreateUser(context.Background(), &CreateUserInput{
		Name:        "app-ci",
		Permissions: []UserPermission{{RepositoryID: "repo-1", Actions: []string{"PULL"}}},
	})
	if !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("err = %v, want ErrUserNotFound: app-ci-2 only contains app-ci as a substring, not a suffix", err)
	}
}

func TestCreateUserEmptySecretKeyFails(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/user/permissions":
			testutil.WriteFixture(t, w, "../testdata/containerregistry/list_permissions.json")
		case r.Method == http.MethodPost && r.URL.Path == "/v1/user":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"secretKey":""}`))
		default:
			t.Fatal("no list expected: the create itself failed, there is nothing to look up")
		}
	}))

	_, err := c.CreateUser(context.Background(), &CreateUserInput{
		Name:        "app-ci",
		Permissions: []UserPermission{{RepositoryID: "repo-1", Actions: []string{"PULL"}}},
	})
	var apiErr *core.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v, want *core.APIError", err)
	}
	if !strings.Contains(err.Error(), "list-users") {
		t.Fatalf("err = %v, want it to name list-users", err)
	}
}

func TestCreateUserStatusErrors(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusNotFound, http.StatusConflict, http.StatusInternalServerError} {
		c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.Method == http.MethodGet && r.URL.Path == "/v1/user/permissions":
				testutil.WriteFixture(t, w, "../testdata/containerregistry/list_permissions.json")
			case r.Method == http.MethodPost && r.URL.Path == "/v1/user":
				w.WriteHeader(status)
				_, _ = w.Write([]byte(`{"message":"failed"}`))
			}
		}))

		_, err := c.CreateUser(context.Background(), &CreateUserInput{
			Name:        "app-ci",
			Permissions: []UserPermission{{RepositoryID: "repo-1", Actions: []string{"PULL"}}},
		})
		var apiErr *core.APIError
		if !errors.As(err, &apiErr) || apiErr.StatusCode != status {
			t.Fatalf("status %d: err = %v, want a %d *core.APIError", status, err, status)
		}
	}
}

func TestCreateUserNoRetryAfter502(t *testing.T) {
	var createCalls atomic.Int64
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/user/permissions":
			testutil.WriteFixture(t, w, "../testdata/containerregistry/list_permissions.json")
		case r.Method == http.MethodPost && r.URL.Path == "/v1/user":
			createCalls.Add(1)
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte(`{"message":"upstream error"}`))
		}
	}))

	_, err := c.CreateUser(context.Background(), &CreateUserInput{
		Name:        "app-ci",
		Permissions: []UserPermission{{RepositoryID: "repo-1", Actions: []string{"PULL"}}},
	})
	if err == nil {
		t.Fatal("err = nil, want an error")
	}
	if createCalls.Load() != 1 {
		t.Fatalf("POST calls = %d, want 1: a create must never be retried after a 5xx", createCalls.Load())
	}
	if !strings.Contains(err.Error(), "list-users") {
		t.Fatalf("err = %v, want a hint to list-users before creating again", err)
	}
}

// TestCreateUserRejectionNotWrapped checks that an outright 4xx rejection,
// which never reached the server as a create, does not get the ambiguous
// list-and-check hint.
func TestCreateUserRejectionNotWrapped(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/user/permissions":
			testutil.WriteFixture(t, w, "../testdata/containerregistry/list_permissions.json")
		case r.Method == http.MethodPost && r.URL.Path == "/v1/user":
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"message":"name already exists"}`))
		}
	}))

	_, err := c.CreateUser(context.Background(), &CreateUserInput{
		Name:        "app-ci",
		Permissions: []UserPermission{{RepositoryID: "repo-1", Actions: []string{"PULL"}}},
	})
	if err == nil {
		t.Fatal("err = nil, want an error")
	}
	if strings.Contains(err.Error(), "list-users") {
		t.Fatalf("a 4xx rejection should not get the ambiguous-create hint: %v", err)
	}
}

// TestCreateUserSecretRedaction checks that CreateUserOutput's SecretKey
// never leaks the fixture secret through fmt verbs, slog, or json.Marshal,
// and that "[redacted]" appears in its place.
func TestCreateUserSecretRedaction(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/user/permissions":
			testutil.WriteFixture(t, w, "../testdata/containerregistry/list_permissions.json")
		case r.Method == http.MethodPost && r.URL.Path == "/v1/user":
			w.WriteHeader(http.StatusOK)
			testutil.WriteFixture(t, w, "../testdata/containerregistry/create_user.json")
		case r.Method == http.MethodGet && r.URL.Path == "/v1/user":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[{"uuid":"ra-1","name":"app-ci"}],"page":1,"pageSize":10000,"totalPage":1,"totalItem":1}`))
		}
	}))

	out, err := c.CreateUser(context.Background(), &CreateUserInput{
		Name:        "app-ci",
		Permissions: []UserPermission{{RepositoryID: "repo-1", Actions: []string{"PULL"}}},
	})
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}

	forms := map[string]string{
		"%v on Output":     fmt.Sprintf("%v", out),
		"%+v on Output":    fmt.Sprintf("%+v", out),
		"%#v on Output":    fmt.Sprintf("%#v", out),
		"%s on SecretKey":  fmt.Sprintf("%s", out.SecretKey),
		"%v on SecretKey":  fmt.Sprintf("%v", out.SecretKey),
		"%#v on SecretKey": fmt.Sprintf("%#v", out.SecretKey),
	}
	for name, got := range forms {
		if strings.Contains(got, "<secret>") {
			t.Fatalf("%s = %q, holds the fixture secret", name, got)
		}
		if !strings.Contains(got, "[redacted]") {
			t.Fatalf("%s = %q, want it to contain [redacted]", name, got)
		}
	}

	data, err := json.Marshal(out) //nolint:gosec // G117: SecretKey is a vngcloud.Secret; its MarshalJSON redacts it, which this test itself verifies below
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	if strings.Contains(string(data), "<secret>") {
		t.Fatalf("json.Marshal() = %s, holds the fixture secret", data)
	}
	if !strings.Contains(string(data), "[redacted]") {
		t.Fatalf("json.Marshal() = %s, want it to contain [redacted]", data)
	}

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	logger.Info("created user", "secret_key", out.SecretKey)
	if strings.Contains(buf.String(), "<secret>") {
		t.Fatalf("slog output = %q, holds the fixture secret", buf.String())
	}
	if !strings.Contains(buf.String(), "[redacted]") {
		t.Fatalf("slog output = %q, want it to contain [redacted]", buf.String())
	}
}

// TestCreateUserNeverCaptured checks that CreateUser's Sensitive create
// request never reaches the configured response-capture hook, and that no
// captured response (from the ListPermissions or ListUsers reads CreateUser
// also makes) ever holds the fixture secret.
func TestCreateUserNeverCaptured(t *testing.T) {
	var captured []transport.Capture
	cfg := testutil.NewConfigWithCapture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/user/permissions":
			testutil.WriteFixture(t, w, "../testdata/containerregistry/list_permissions.json")
		case r.Method == http.MethodPost && r.URL.Path == "/v1/user":
			w.WriteHeader(http.StatusOK)
			testutil.WriteFixture(t, w, "../testdata/containerregistry/create_user.json")
		case r.Method == http.MethodGet && r.URL.Path == "/v1/user":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[{"uuid":"ra-1","name":"app-ci"}],"page":1,"pageSize":10000,"totalPage":1,"totalItem":1}`))
		}
	}), func(call transport.Capture) {
		captured = append(captured, call)
	})
	c := New(cfg)

	if _, err := c.CreateUser(context.Background(), &CreateUserInput{
		Name:        "app-ci",
		Permissions: []UserPermission{{RepositoryID: "repo-1", Actions: []string{"PULL"}}},
	}); err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}
	for _, call := range captured {
		if call.Method == http.MethodPost {
			t.Fatalf("the create POST was captured, want it withheld: %+v", call)
		}
		if strings.Contains(string(call.Body), "<secret>") {
			t.Fatalf("a captured response held the fixture secret: %+v", call)
		}
	}
}

// fakeUserTokenSource issues sequential tokens. Every other test in this
// file wires its Client through newTestClient, whose transport has no
// TokenSource at all, so transport.doAuthenticated's 401 branch never runs:
// EnsureToken is a no-op. This type lets TestCreateUserOnceNoResendAfter401
// build a Client with a real one, so a 401 on the create POST actually
// reaches that branch.
type fakeUserTokenSource struct {
	count atomic.Int64
}

func (s *fakeUserTokenSource) Token(context.Context) (transport.Token, error) {
	n := s.count.Add(1)
	return transport.Token{AccessToken: fmt.Sprintf("token-%d", n), ExpiresAt: time.Now().Add(time.Hour)}, nil
}

func (s *fakeUserTokenSource) Invalidate(string) {}

// TestCreateUserOnceNoResendAfter401 checks that the create POST carries
// Once (transport.Request.Once): with a real TokenSource, a 401 reaches
// transport's own invalidate-and-resend branch, but Once must still leave
// the POST sent exactly once, unlike an ordinary request.
func TestCreateUserOnceNoResendAfter401(t *testing.T) {
	var createCalls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/user/permissions":
			testutil.WriteFixture(t, w, "../testdata/containerregistry/list_permissions.json")
		case r.Method == http.MethodPost && r.URL.Path == "/v1/user":
			createCalls.Add(1)
			w.WriteHeader(http.StatusUnauthorized)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	tc := transport.New(transport.Config{HTTPClient: server.Client(), TokenSource: &fakeUserTokenSource{}})
	cfg := core.NewTestConfig("hcm-3", "", endpoints.Set{VCR: server.URL + "/"}, tc)
	c := New(cfg)

	_, err := c.CreateUser(context.Background(), &CreateUserInput{
		Name:        "app-ci",
		Permissions: []UserPermission{{RepositoryID: "repo-1", Actions: []string{"PULL"}}},
	})
	if !errors.Is(err, core.ErrAuth) {
		t.Fatalf("err = %v, want core.ErrAuth", err)
	}
	if createCalls.Load() != 1 {
		t.Fatalf("POST calls = %d, want 1: a Once request must never resend after a 401", createCalls.Load())
	}
}

// TestCreateUserOnceRefusesRedirect checks that the create POST carries
// Once (transport.Request.Once): net/http would otherwise replay a
// redirected POST's method and body at the Location it names, sending the
// create, and its secret, a second time.
func TestCreateUserOnceRefusesRedirect(t *testing.T) {
	var createCalls atomic.Int64
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/user/permissions":
			testutil.WriteFixture(t, w, "../testdata/containerregistry/list_permissions.json")
		case r.Method == http.MethodPost && r.URL.Path == "/v1/user":
			createCalls.Add(1)
			w.Header().Set("Location", "/v1/user/moved")
			w.WriteHeader(http.StatusTemporaryRedirect)
		default:
			t.Fatalf("unexpected request: %s %s: a Once request must never follow the redirect", r.Method, r.URL.Path)
		}
	}))

	_, err := c.CreateUser(context.Background(), &CreateUserInput{
		Name:        "app-ci",
		Permissions: []UserPermission{{RepositoryID: "repo-1", Actions: []string{"PULL"}}},
	})
	if err == nil {
		t.Fatal("err = nil, want an error for the 307")
	}
	if createCalls.Load() != 1 {
		t.Fatalf("POST calls = %d, want 1: a Once request must never follow a redirect", createCalls.Load())
	}
}

// --- DeleteUser ---

func TestDeleteUser(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete || r.URL.Path != "/v1/user/ra-1" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))

	if _, err := c.DeleteUser(context.Background(), &DeleteUserInput{UserID: "ra-1"}); err != nil {
		t.Fatalf("DeleteUser() error = %v", err)
	}
}

func TestDeleteUserPathIDRejection(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("no request expected")
	}))
	for _, id := range []string{"..", ".", "a/b", "a?b", ""} {
		if _, err := c.DeleteUser(context.Background(), &DeleteUserInput{UserID: id}); !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Errorf("UserID %q: err = %v, want ErrInvalidInput", id, err)
		}
	}
}

func TestDeleteUserBare404IsNotFoundWithNoListCall(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/user/ra-1" {
			t.Fatalf("unexpected request: %s %s: a plain 404 needs no list confirm", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"not found"}`))
	}))

	_, err := c.DeleteUser(context.Background(), &DeleteUserInput{UserID: "ra-1"})
	if !vngcloud.IsNotFound(err) {
		t.Fatalf("IsNotFound(err) = false, err = %v", err)
	}
}

// TestDeleteUser500AbsentFromListReturnsNotFound covers the not-found
// mapping for an ambiguous 5xx: a plain 404 needs no list confirm (see
// TestDeleteUserBare404IsNotFoundWithNoListCall), but the reference
// documents no 404 for this call, only 500, so a 5xx is confirmed against
// ListUsers, mirroring GetRepository's own list confirm.
func TestDeleteUser500AbsentFromListReturnsNotFound(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodDelete && r.URL.Path == "/v1/user/ra-1":
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"message":"internal error"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/user":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[],"page":1,"pageSize":10000,"totalPage":0,"totalItem":0}`))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))

	_, err := c.DeleteUser(context.Background(), &DeleteUserInput{UserID: "ra-1"})
	if !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("err = %v, want core.ErrNotFound", err)
	}
}

func TestDeleteUser500ListedReturnsOriginal500(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodDelete && r.URL.Path == "/v1/user/ra-1":
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"message":"internal error"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/user":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[{"uuid":"ra-1"}],"page":1,"pageSize":10000,"totalPage":1,"totalItem":1}`))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))

	_, err := c.DeleteUser(context.Background(), &DeleteUserInput{UserID: "ra-1"})
	if errors.Is(err, core.ErrNotFound) {
		t.Fatalf("err = %v, want the original 500, not NotFound: the user is still listed", err)
	}
	var apiErr *core.APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != 500 {
		t.Fatalf("err = %v, want a 500 *core.APIError", err)
	}
}

func TestDeleteUser500ListFailureReturnsOriginal500(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodDelete && r.URL.Path == "/v1/user/ra-1":
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"message":"internal error"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/user":
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"message":"list also failed"}`))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))

	_, err := c.DeleteUser(context.Background(), &DeleteUserInput{UserID: "ra-1"})
	if errors.Is(err, core.ErrNotFound) {
		t.Fatalf("err = %v, want the original 500, not NotFound: the list call itself failed", err)
	}
	var apiErr *core.APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != 500 {
		t.Fatalf("err = %v, want the original 500 *core.APIError", err)
	}
}
