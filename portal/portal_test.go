package portal

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/testutil"
)

func TestPortalRoutes(t *testing.T) {
	tests := []struct {
		name string
		path string
		body string
		call func(*Client) error
	}{
		{
			name: "user info",
			path: "/v1/users/info",
			body: testutil.FixtureBody(t, "../testdata/portal/get_user_info.json"),
			call: func(c *Client) error {
				out, err := c.GetUserInfo(context.Background(), nil)
				if err == nil && out.UserInfo["userId"] == nil {
					t.Fatalf("unexpected user info: %+v", out.UserInfo)
				}
				return err
			},
		},
		{
			name: "zones",
			path: "/v1/project-1/zones",
			body: testutil.FixtureBody(t, "../testdata/portal/list_zones.json"),
			call: func(c *Client) error {
				out, err := c.ListZones(context.Background(), nil)
				if err == nil && (len(out.Items) != 1 || out.Items[0]["id"] != "zone-1") {
					t.Fatalf("unexpected zones: %+v", out)
				}
				return err
			},
		},
		{
			name: "quota used",
			path: "/v2/project-1/quotas/quotaUsed",
			body: testutil.FixtureBody(t, "../testdata/portal/list_quota_used.json"),
			call: func(c *Client) error {
				out, err := c.ListQuotaUsed(context.Background(), nil)
				if err == nil && (len(out.Items) != 1 || out.Items[0]["name"] != "<name>") {
					t.Fatalf("unexpected quotas: %+v", out)
				}
				return err
			},
		},
		{
			name: "quota by name",
			path: "/v2/project-1/quotas/quotaUsed",
			body: testutil.FixtureBody(t, "../testdata/portal/list_quota_used.json"),
			call: func(c *Client) error {
				out, err := c.GetQuota(context.Background(), &GetQuotaInput{Name: "<name>"})
				if err == nil && out.Quota["name"] != "<name>" {
					t.Fatalf("unexpected quota: %+v", out.Quota)
				}
				return err
			},
		},
		{
			name: "tag quota",
			path: "/v2/project-1/tag/quota",
			body: testutil.FixtureBody(t, "../testdata/portal/get_tag_quota.json"),
			call: func(c *Client) error {
				out, err := c.GetTagQuota(context.Background(), nil)
				if err == nil && out.TagQuota["used"] == nil {
					t.Fatalf("unexpected tag quota: %+v", out.TagQuota)
				}
				return err
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != tt.path {
					t.Fatalf("unexpected path: %s", r.URL.Path)
				}
				_, _ = w.Write([]byte(tt.body))
			}))
			if err := tt.call(c); err != nil {
				t.Fatalf("call error = %v", err)
			}
		})
	}
}

func TestPortalRequiredInput(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("no request expected")
	}))
	_, err := c.GetQuota(context.Background(), &GetQuotaInput{})
	if !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("err = %v", err)
	}
	if _, err := c.GetQuota(context.Background(), nil); !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("nil input err = %v", err)
	}
}

func TestPortalZeroConfig(t *testing.T) {
	c := New(vngcloud.Config{})
	if _, err := c.ListZones(context.Background(), nil); !errors.Is(err, vngcloud.ErrInvalidConfig) {
		t.Fatalf("ListZones() err = %v, want ErrInvalidConfig", err)
	}
}

func newTestClient(t *testing.T, handler http.Handler) *Client {
	t.Helper()

	return New(testutil.NewConfig(t, handler))
}
