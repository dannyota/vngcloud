package storage

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/core"
)

func TestEnvelopeRedactsBearerToken(t *testing.T) {
	var sent string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sent = strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		_, _ = fmt.Fprintf(w, `{"success":false,"code":%q,"errorMsg":%q}`, sent, "bad "+sent)
	}))
	defer server.Close()
	cfg, err := vngcloud.NewConfig(vngcloud.WithRegion("hcm-3"), vngcloud.WithStaticToken("synthetic-bearer-token"), vngcloud.WithHTTPClient(server.Client()), vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{Storage: server.URL}))
	if err != nil {
		t.Fatal(err)
	}
	c := New(cfg)
	_, err = c.ListRegions(context.Background(), nil)
	var ae *core.APIError
	if sent == "" || !errors.As(err, &ae) {
		t.Fatalf("error = %v", err)
	}
	for _, value := range []string{ae.Message, ae.Code, ae.Error()} {
		if strings.Contains(value, sent) {
			t.Error("envelope leaked bearer token")
		}
	}
}
