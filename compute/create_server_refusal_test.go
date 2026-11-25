package compute

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/core"
)

const testDiskHint = "a 400 can mean a disk type is not offered in ZoneID; list the zone's types with volume.ListVolumeTypes and ZoneID"

func TestCreateServerPostRefusalHint(t *testing.T) {
	for _, status := range []int{400, 401, 403, 404, 409, 422, 429, 500, 502} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				routeServerWriteRequest(t, w, r, emptyListServersPage, func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(status)
					_, _ = w.Write([]byte(`{"code":"SyntheticCode","message":"provider refusal"}`))
				}, nil)
			}))
			in := validCreateServerInput()
			in.MaxPrice = 347800
			_, err := c.CreateServer(context.Background(), in)
			var api *core.APIError
			if !errors.As(err, &api) {
				t.Fatalf("expected APIError, got %v", err)
			}
			if api.StatusCode != status || api.Code != "SyntheticCode" || api.Operation != "compute.CreateServer" || api.Retryable != (status == 429) {
				t.Errorf("API metadata = %+v", api)
			}
			want := 0
			if status == 400 {
				want = 1
			}
			if n := strings.Count(err.Error(), testDiskHint); n != want {
				t.Errorf("hint count = %d, want %d: %v", n, want, err)
			}
			sentinel := map[int]error{401: core.ErrAuth, 403: core.ErrPermission, 404: core.ErrNotFound, 429: core.ErrRateLimited}[status]
			if sentinel != nil && !errors.Is(err, sentinel) {
				t.Errorf("missing sentinel %v: %v", sentinel, err)
			}
		})
	}
}

func TestCreateServerPostRefusalHintWithholdsUserData(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		routeServerWriteRequest(t, w, r, emptyListServersPage, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(400)
			_, _ = w.Write([]byte(`{"message":"response-secret"}`))
		}, nil)
	}))
	in := validCreateServerInput()
	in.MaxPrice = 347800
	in.SSHKeyID = ""
	in.UserData = "userdata-secret"
	_, err := c.CreateServer(context.Background(), in)
	if err == nil || !strings.Contains(err.Error(), createServerWithheldMessage) || !strings.Contains(err.Error(), testDiskHint) || strings.Contains(err.Error(), "response-secret") || strings.Contains(err.Error(), "userdata-secret") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCreateServerPostRefusalHintRedactsToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		routeServerWriteRequest(t, w, r, emptyListServersPage, func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer test-token" {
				t.Errorf("missing synthetic bearer token")
			}
			w.WriteHeader(400)
			_, _ = fmt.Fprintf(w, `{"message":%q}`, r.Header.Get("Authorization"))
		}, nil)
	}))
	defer server.Close()
	cfg, err := vngcloud.NewConfig(vngcloud.WithRegion("hcm-3"), vngcloud.WithProjectID("project-1"), vngcloud.WithStaticToken("test-token"), vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{VServer: server.URL, Portal: server.URL}), vngcloud.WithRetry(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	c := New(cfg)
	in := validCreateServerInput()
	in.MaxPrice = 347800
	_, err = c.CreateServer(context.Background(), in)
	if err == nil || !strings.Contains(err.Error(), testDiskHint) || strings.Contains(err.Error(), "test-token") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestQuoteCreateServer400HasNoRefusalHint(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(400) }))
	_, err := c.QuoteCreateServer(context.Background(), validCreateServerInput())
	if err == nil || strings.Contains(err.Error(), testDiskHint) {
		t.Fatalf("unexpected error: %v", err)
	}
}
