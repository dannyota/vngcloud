package portal

import (
	"context"
	"net/http"
	"testing"

	"danny.vn/vngcloud/internal/testutil"
)

func TestPortalRoutes(t *testing.T) {
	tests := []struct {
		name string
		path string
		body string
		call func(*Service) error
	}{
		{
			name: "user info",
			path: "/v1/users/info",
			body: testutil.FixtureBody(t, "../../testdata/portal/get_user_info.json"),
			call: func(s *Service) error {
				info, err := s.GetUserInfo(context.Background())
				if err == nil && info["userId"] == nil {
					t.Fatalf("unexpected user info: %+v", info)
				}
				return err
			},
		},
		{
			name: "zones",
			path: "/v1/project-1/zones",
			body: testutil.FixtureBody(t, "../../testdata/portal/list_zones.json"),
			call: func(s *Service) error {
				zones, err := s.ListZones(context.Background())
				if err == nil && (len(zones) != 1 || zones[0]["id"] != "zone-1") {
					t.Fatalf("unexpected zones: %+v", zones)
				}
				return err
			},
		},
		{
			name: "quota used",
			path: "/v2/project-1/quotas/quotaUsed",
			body: testutil.FixtureBody(t, "../../testdata/portal/list_quota_used.json"),
			call: func(s *Service) error {
				quotas, err := s.ListQuotaUsed(context.Background())
				if err == nil && (len(quotas) != 1 || quotas[0]["name"] != "<name>") {
					t.Fatalf("unexpected quotas: %+v", quotas)
				}
				return err
			},
		},
		{
			name: "quota by name",
			path: "/v2/project-1/quotas/quotaUsed",
			body: testutil.FixtureBody(t, "../../testdata/portal/list_quota_used.json"),
			call: func(s *Service) error {
				quota, err := s.GetQuota(context.Background(), "<name>")
				if err == nil && quota["name"] != "<name>" {
					t.Fatalf("unexpected quota: %+v", quota)
				}
				return err
			},
		},
		{
			name: "tag quota",
			path: "/v2/project-1/tag/quota",
			body: testutil.FixtureBody(t, "../../testdata/portal/get_tag_quota.json"),
			call: func(s *Service) error {
				quota, err := s.GetTagQuota(context.Background())
				if err == nil && quota["used"] == nil {
					t.Fatalf("unexpected tag quota: %+v", quota)
				}
				return err
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := newTestService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != tt.path {
					t.Fatalf("unexpected path: %s", r.URL.Path)
				}
				_, _ = w.Write([]byte(tt.body))
			}))
			if err := tt.call(service); err != nil {
				t.Fatalf("call error = %v", err)
			}
		})
	}
}

func newTestService(t *testing.T, handler http.Handler) *Service {
	t.Helper()

	client := testutil.NewCoreClient(t, handler)
	return New(client)
}
