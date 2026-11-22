package portal

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"danny.vn/vngcloud"
)

func TestPortalRejectsMalformedObjects(t *testing.T) {
	for _, body := range []string{`[]`, `1`, `"text"`, `null`, `{"data":null}`, `{"data":[]}`, `{"data":{},"quota":{}}`, `{"data":{"used":0},"DATA":{"used":1}}`, `{"used":0,"used":1}`} {
		t.Run(body, func(t *testing.T) {
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) }))
			out, err := c.GetTagQuota(context.Background(), nil)
			var api *vngcloud.APIError
			if out != nil || !errors.As(err, &api) || api.Code != "InvalidResponse" {
				t.Fatalf("out=%+v err=%v", out, err)
			}
		})
	}
}

func TestPortalRejectsReflectedCredentials(t *testing.T) {
	for _, operation := range []string{"user", "quota", "zones", "tags"} {
		t.Run(operation, func(t *testing.T) {
			captures := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				credential := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
				var data any = map[string]any{"name": credential}
				if operation == "quota" || operation == "zones" {
					data = []any{data}
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
			}))
			t.Cleanup(srv.Close)
			cfg, configErr := vngcloud.NewConfig(vngcloud.WithRegion("hcm-3"), vngcloud.WithProjectID("project-1"), vngcloud.WithCredentialsProvider(portalTestCredentials{}), vngcloud.WithHTTPClient(srv.Client()), vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{VServer: srv.URL, Portal: srv.URL}), vngcloud.WithResponseCapture(func(vngcloud.ResponseCapture) { captures++ }))
			if configErr != nil {
				t.Fatal(configErr)
			}
			c := New(cfg)
			var err error
			switch operation {
			case "user":
				_, err = c.GetUserInfo(context.Background(), nil)
			case "quota":
				_, err = c.ListQuotaUsed(context.Background(), nil)
			case "zones":
				_, err = c.ListZones(context.Background(), nil)
			case "tags":
				_, err = c.GetTagQuota(context.Background(), nil)
			}
			if err == nil || captures != 0 {
				t.Fatalf("err=%v captures=%d", err, captures)
			}
			if strings.Contains(err.Error(), "synthetic-portal-token") {
				t.Fatal("credential escaped")
			}
		})
	}
}

type portalTestCredentials struct{}

func (portalTestCredentials) Token(context.Context) (vngcloud.Token, error) {
	return vngcloud.Token{AccessToken: "synthetic-portal-token"}, nil
}
func (portalTestCredentials) Invalidate(string) {}

func TestPortalPreservesErrorIdentities(t *testing.T) {
	for _, tt := range []struct {
		status   int
		identity error
	}{{401, vngcloud.ErrAuth}, {403, vngcloud.ErrPermission}, {404, vngcloud.ErrNotFound}, {429, vngcloud.ErrRateLimited}} {
		c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(tt.status) }))
		if _, err := c.GetUserInfo(context.Background(), nil); !errors.Is(err, tt.identity) {
			t.Fatalf("status=%d err=%v lost=%v", tt.status, err, tt.identity)
		}
	}
}

func TestPortalListRejectsConflictingEvidence(t *testing.T) {
	for _, body := range []string{`{"data":[{"name":"a","Name":"b"}]}`, `{"data":[],"zones":[]}`, `{"Data":[]}`, `{"data":null}`, `{}`} {
		c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) }))
		if out, err := c.ListZones(context.Background(), nil); err == nil || out != nil {
			t.Fatalf("out=%+v err=%v", out, err)
		}
	}
}
