package portal

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"
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
				if err == nil && (len(out.Items) != 1 || (out.Items[0].ID == nil || *out.Items[0].ID != "zone-1")) {
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

func TestListZonesFixtures(t *testing.T) {
	for _, fixture := range []string{"list_zones.json", "discovery_zones.json"} {
		t.Run(fixture, func(t *testing.T) {
			body := testutil.FixtureBody(t, "../testdata/portal/"+fixture)
			var raw map[string][]map[string]any
			if err := json.Unmarshal([]byte(body), &raw); err != nil {
				t.Fatal(err)
			}
			rows := raw["zones"]
			if rows == nil {
				rows = raw["data"]
			}
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) }))
			out, err := c.ListZones(context.Background(), nil)
			if err != nil {
				t.Fatal(err)
			}
			if len(out.Items) != len(rows) {
				t.Fatalf("items = %d, want %d", len(out.Items), len(rows))
			}
			typ := reflect.TypeFor[Zone]()
			if typ.Kind() != reflect.Struct {
				t.Fatalf("Zone kind = %v, want struct", typ.Kind())
			}
			for i, zone := range out.Items {
				for field := range typ.Fields() {
					key := field.Tag.Get("json")
					if _, ok := rows[i][key]; !ok {
						rows[i][key] = nil
					}
				}
				encoded, err := json.Marshal(zone)
				if err != nil {
					t.Fatal(err)
				}
				var got map[string]any
				if err := json.Unmarshal(encoded, &got); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(got, rows[i]) {
					t.Fatalf("row %d = %#v, want %#v", i, got, rows[i])
				}
			}
		})
	}
}

func TestListZonesNullAndZeroFields(t *testing.T) {
	body := `{"data":[{"name":"nulls","id":null,"uuid":null,"zoneType":null,"isDefault":null,"description":null,"isEnabled":null,"volumeCount":null,"serverCount":null,"openstackZone":null,"ipRanges":null,"apiToken":"unknown-secret"},{"name":"zero","id":"","uuid":"","zoneType":"FUTURE","isDefault":false,"description":"","isEnabled":false,"volumeCount":0,"serverCount":0,"openstackZone":"","ipRanges":[]}]}`
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) }))
	out, err := c.ListZones(context.Background(), &ListZonesInput{})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Items) != 2 {
		t.Fatalf("items = %d", len(out.Items))
	}
	wantNull := Zone{Name: "nulls"}
	wantZero := Zone{Name: "zero", ID: vngcloud.Ptr(""), UUID: vngcloud.Ptr(""), ZoneType: vngcloud.Ptr("FUTURE"), IsDefault: vngcloud.Ptr(false), Description: vngcloud.Ptr(""), IsEnabled: vngcloud.Ptr(false), VolumeCount: vngcloud.Ptr(0), ServerCount: vngcloud.Ptr(0), OpenStackZone: vngcloud.Ptr(""), IPRanges: vngcloud.Ptr([]string{})}
	if !reflect.DeepEqual(out.Items, []Zone{wantNull, wantZero}) {
		t.Fatalf("zones = %#v", out.Items)
	}
	encoded, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "unknown-secret") || strings.Contains(string(encoded), "apiToken") {
		t.Fatalf("unknown key survived: %s", encoded)
	}
}

func TestListZonesWrongFieldTypes(t *testing.T) {
	for _, field := range []string{"id", "uuid", "name", "zoneType", "isDefault", "description", "isEnabled", "volumeCount", "serverCount", "openstackZone", "ipRanges"} {
		t.Run(field, func(t *testing.T) {
			body := `{"data":[{"name":"valid"},{"` + field + `":{"private":"decode-secret"}}]}`
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) }))
			out, err := c.ListZones(context.Background(), nil)
			var api *vngcloud.APIError
			if out != nil || !errors.As(err, &api) {
				t.Fatalf("output = %v, error = %v, want nil and APIError", out, err)
			}
			if strings.Contains(err.Error(), "decode-secret") {
				t.Fatalf("error leaked body: %v", err)
			}
		})
	}
}
