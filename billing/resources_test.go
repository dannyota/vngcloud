package billing

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/testutil"
)

func TestListResourcesFixture(t *testing.T) {
	c := New(testutil.NewConfig(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/gateway/api/v1/resources" || r.URL.RawQuery != "" || r.Header.Get("region") != "" || r.Header.Get("project-id") != "" {
			t.Error("resource route or headers")
		}
		testutil.WriteFixture(t, w, "../testdata/billing/ListResources.json")
	})))
	out, err := c.ListResources(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Items) != 3 || out.Items[0].RenewType != RenewTypeManual || out.Items[1].RenewType != RenewTypeAutoRenew || out.Items[2].RenewType != "FUTURE" || out.Items[0].Channel == nil || *out.Items[0].Channel != 0 || out.TotalAutoRenews == nil {
		t.Fatalf("invalid decoded resource list: %+v", out)
	}
	raw, err := json.Marshal(out)
	if err != nil || strings.Contains(string(raw), "tags") || strings.Contains(string(raw), "creator") {
		t.Fatal("resource tags exposed")
	}
}

func TestListResourcesStrict(t *testing.T) {
	for _, body := range []string{`{}`, `null`, `{"code":"200","data":{"data":[]}}`, `{"code":201,"data":{"data":[]}}`, `{"code":200,"data":null}`, `{"code":200,"data":{}}`, `{"code":200,"data":{"data":null}}`, `{"code":200,"data":{"data":{}}}`, `{"code":200,"data":{"data":[{"channel":"0"}]}}`, `{"code":200,"data":{"data":[{"isRenewing":0}]}}`, `{"code":200,"data":{"data":[],"totalAutoRenews":"1"}}`} {
		t.Run(body, func(t *testing.T) {
			c := New(testutil.NewConfig(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) })))
			_, err := c.ListResources(context.Background(), nil)
			var api *core.APIError
			if !errors.As(err, &api) {
				t.Fatalf("invalid envelope accepted: %v", err)
			}
		})
	}
}
