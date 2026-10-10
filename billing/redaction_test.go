package billing

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
		_, _ = fmt.Fprintf(w, `{"code":%q,"message":%q}`, sent, "bad "+sent)
	}))
	defer server.Close()
	cfg, err := vngcloud.NewConfig(vngcloud.WithRegion("hcm-3"), vngcloud.WithStaticToken("synthetic-bearer-token"), vngcloud.WithHTTPClient(server.Client()), vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{Billing: server.URL}))
	if err != nil {
		t.Fatal(err)
	}
	c := New(cfg)
	_, err = c.ListBudgets(context.Background(), nil)
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

func TestEnvelopeRedactionPreservesNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"code":404,"message":"Budget not found"}`))
	}))
	defer server.Close()
	cfg, err := vngcloud.NewConfig(vngcloud.WithRegion("hcm-3"), vngcloud.WithStaticToken("found"), vngcloud.WithHTTPClient(server.Client()), vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{Billing: server.URL}))
	if err != nil {
		t.Fatal(err)
	}
	_, err = New(cfg).ListBudgets(context.Background(), nil)
	if !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("not-found mapping lost: %v", err)
	}
	if strings.Contains(err.Error(), "found") {
		t.Fatal("credential leaked")
	}
}
