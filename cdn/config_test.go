package cdn

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"danny.vn/vngcloud"
)

// The public options carry the key and the CDN endpoint override through to
// the request, and the IAM static token never replaces the key.
func TestVCDNThroughPublicConfig(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer "+testKey {
			t.Errorf("Authorization = %q, want the vCDN key", got)
		}
		if r.URL.Path != "/base/v1/apikey/list" {
			t.Errorf("path = %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(readFixture(t, "apikey-list.json")))
	}))
	defer server.Close()

	cfg, err := vngcloud.NewConfig(
		vngcloud.WithRegion("hcm-3"),
		vngcloud.WithStaticToken("iam-token"),
		vngcloud.WithCDNAPIKey(testKey),
		vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{CDN: server.URL + "/base"}),
		vngcloud.WithHTTPClient(server.Client()),
	)
	if err != nil {
		t.Fatal(err)
	}
	out, err := New(cfg).ListAPIKeys(context.Background(), nil)
	if err != nil || len(out.Items) != 2 {
		t.Fatalf("out = %+v, err = %v", out, err)
	}
}
