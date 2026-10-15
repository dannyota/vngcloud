package iam

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/testutil"
	"danny.vn/vngcloud/internal/transport"
)

func TestGetUserFixtures(t *testing.T) {
	for _, tc := range []struct {
		fixture string
		lastUse int64
	}{
		{"get_user.json", 0}, {"get_user_mfa.json", 1760000100000},
	} {
		t.Run(tc.fixture, func(t *testing.T) {
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/accounts-api/v1/iam-users/00000000-0000-4000-8000-000000000001" {
					t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
				}
				testutil.WriteFixture(t, w, "../testdata/iam/"+tc.fixture)
			}))
			out, err := c.GetUser(context.Background(), &GetUserInput{UserID: "00000000-0000-4000-8000-000000000001"})
			if err != nil {
				t.Fatal(err)
			}
			u := out.User
			if u.ID != "00000000-0000-4000-8000-000000000001" || u.Username != "vngcloud-probe-0000aaaa" || u.CreatedAt != 1760000000000 || u.LastUse != tc.lastUse || !u.Enabled || u.From != "iam" {
				t.Fatalf("unexpected user: %+v", u)
			}
		})
	}
}

func TestUserReadValidation(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("unexpected request") }))
	for _, in := range []*GetUserInput{nil, {}, {UserID: "../bad"}, {UserID: "bad/id"}, {UserID: "bad?query"}} {
		if out, err := c.GetUser(context.Background(), in); out != nil || !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Fatalf("GetUser = %v, %v", out, err)
		}
	}
	for _, name := range []string{"with space", "with\tcontrol", "\xff", strings.Repeat("a", 4097)} {
		if out, err := c.ListUsers(context.Background(), &ListUsersInput{Name: name}); out != nil || !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Fatalf("ListUsers = %v, %v", out, err)
		}
	}
}

func TestListUsersNameFixture(t *testing.T) {
	for _, name := range []string{"", "MiXeD+&?", "短"} {
		t.Run(name, func(t *testing.T) {
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				q := r.URL.Query()
				if q.Get("name") != name || q.Has("name") != (name != "") || q.Get("pageNumber") != "0" || q.Get("pageSize") != "10000" || len(q) != 2+boolInt(name != "") {
					t.Fatalf("query = %s", r.URL.RawQuery)
				}
				testutil.WriteFixture(t, w, "../testdata/iam/list_users_filtered.json")
			}))
			out, err := c.ListUsers(context.Background(), &ListUsersInput{Name: name, Page: -1, Size: -1})
			if err != nil {
				t.Fatal(err)
			}
			if len(out.Items) != 1 || out.Items[0].CreatedAt != "2026-01-01T00:00:00.000Z" {
				t.Fatalf("unexpected list: %+v", out)
			}
		})
	}
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func TestUserReadStatuses(t *testing.T) {
	for _, status := range []int{201, 204, 400, 401, 403, 404, 409, 429, 500} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(status)
				_, _ = w.Write([]byte(`{"message":"failed"}`))
			}))
			for _, call := range []func() error{
				func() error { _, err := c.GetUser(context.Background(), &GetUserInput{UserID: "user-1"}); return err },
				func() error { _, err := c.ListUsers(context.Background(), &ListUsersInput{Name: "name"}); return err },
			} {
				err := call()
				var api *vngcloud.APIError
				if !errors.As(err, &api) || api.StatusCode != status {
					t.Fatalf("error = %v, want status %d", err, status)
				}
				sentinel := map[int]error{401: vngcloud.ErrAuth, 403: vngcloud.ErrPermission, 404: vngcloud.ErrNotFound, 429: vngcloud.ErrRateLimited}[status]
				if sentinel != nil && !errors.Is(err, sentinel) {
					t.Fatalf("error = %v, want %v", err, sentinel)
				}
			}
		})
	}
}

func TestGetUserStrictAndPrivateDecode(t *testing.T) {
	for _, body := range []string{
		``, `null`, `[]`, `{} {}`, `{"id":"user-1","id":"user-2"}`, `{"id":"user-1","ID":"user-2"}`, `{"from":"iam","FROM":"idp"}`, `{"username":"one","USERNAME":"two"}`,
		`{"createdAt":{"$numberLong":"decode-canary"}}`, `{"id":17}`, `{"from":false}`, `{"twoFactorAuth":{"sms":{"enabled":false,"enabled":true}}}`,
	} {
		t.Run(fmt.Sprint(len(body), body[:min(len(body), 16)]), func(t *testing.T) {
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) }))
			out, err := c.GetUser(context.Background(), &GetUserInput{UserID: "user-1"})
			if out != nil || err == nil || strings.Contains(fmt.Sprintf("%+v", err), "decode-canary") {
				t.Fatalf("GetUser = %v, %v", out, err)
			}
		})
	}
	// This synthetic variant restores the unknown null factor key omitted from the fixture.
	raw, err := os.ReadFile("../testdata/iam/get_user.json")
	if err != nil {
		t.Fatal(err)
	}
	body := strings.Replace(string(raw), `"sms": {`, `"sms": {"xyz": null,`, 1)
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) }))
	if _, err := c.GetUser(context.Background(), &GetUserInput{UserID: "user-1"}); err != nil {
		t.Fatal(err)
	}
}

func TestUserReadCaps(t *testing.T) {
	for _, tc := range []struct {
		name string
		cap  int
		call func(*Client) error
	}{
		{"get", 256 << 10, func(c *Client) error {
			_, err := c.GetUser(context.Background(), &GetUserInput{UserID: "user-1"})
			return err
		}},
		{"list", 4 << 20, func(c *Client) error { _, err := c.ListUsers(context.Background(), nil); return err }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, extra := range []int{0, 1} {
				body := `{"unknown":"` + strings.Repeat("x", tc.cap-len(`{"unknown":""}`)+extra) + `"}`
				c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) }))
				err := tc.call(c)
				if (err != nil) != (extra != 0) {
					t.Fatalf("extra %d: %v", extra, err)
				}
			}
		})
	}
}

func TestGetUserTimestampForms(t *testing.T) {
	for _, body := range []string{
		`{"createdAt":1760000000000,"lastUse":1760000100000}`,
		`{"createdAt":"2025-10-09T08:53:20Z","lastUse":"2025-10-09T08:55:00Z"}`,
	} {
		c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) }))
		out, err := c.GetUser(context.Background(), &GetUserInput{UserID: "user-1"})
		if err != nil || out.User.CreatedAt != 1760000000000 || out.User.LastUse != 1760000100000 {
			t.Fatalf("get=%+v, %v", out, err)
		}
	}
}

func TestListUsersPublicTolerance(t *testing.T) {
	for _, body := range []string{`{}`, `{"data":null}`, `{"data":[],"totalItems":null,"pageSize":-1}`, `{"data":[],"totalPages":17}`} {
		c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) }))
		out, err := c.ListUsers(context.Background(), &ListUsersInput{Name: "test"})
		if err != nil || len(out.Items) != 0 {
			t.Fatalf("list=%+v, %v", out, err)
		}
	}
}

func TestGetUserSuppressesCapture(t *testing.T) {
	captures := 0
	c := New(testutil.NewConfigWithCapture(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		testutil.WriteFixture(t, w, "../testdata/iam/get_user.json")
	}), func(transport.Capture) { captures++ }))
	if _, err := c.GetUser(context.Background(), &GetUserInput{UserID: "user-1"}); err != nil {
		t.Fatal(err)
	}
	if captures != 0 {
		t.Fatalf("captures=%d", captures)
	}
}
