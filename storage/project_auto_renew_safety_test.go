package storage

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/testutil"
)

type autoRenewHTTP func(*http.Request) (*http.Response, error)

func (f autoRenewHTTP) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestProjectAutoRenewFailedDialAndCancellation(t *testing.T) {
	for _, dial := range []bool{true, false} {
		s := &autoRenewServer{}
		server := httptest.NewServer(s.handler(t))
		defer server.Close()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		attempts := 0
		cfg, err := vngcloud.NewConfig(vngcloud.WithRegion("hcm-3"), vngcloud.WithStaticToken("synthetic-token"), vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{Storage: server.URL, Billing: server.URL}), vngcloud.WithRetry(3, time.Millisecond), vngcloud.WithTransport(autoRenewHTTP(func(r *http.Request) (*http.Response, error) {
			if r.Method == http.MethodPut {
				attempts++
				if dial {
					return nil, &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("synthetic dial error")}
				}
				cancel()
				return nil, context.Canceled
			}
			return server.Client().Transport.RoundTrip(r)
		})))
		if err != nil {
			t.Fatal(err)
		}
		c := New(cfg)
		c.now = func() time.Time { return time.UnixMilli(1790000000000) }
		out, err := c.PutProjectAutoRenew(ctx, &PutProjectAutoRenewInput{ProjectID: "new-1", Enabled: vngcloud.Ptr(true), MaxPrice: 30000})
		if err == nil || attempts != 1 || out == nil || out.Changed || errors.Is(err, ErrNotSettled) == dial {
			t.Fatalf("dial %t error %v attempts %d", dial, err, attempts)
		}
		if !dial && !errors.Is(err, context.Canceled) {
			t.Fatal("cancellation dropped")
		}
	}
}

func TestProjectAutoRenewPrivacyAndEndpointOverride(t *testing.T) {
	const token = "synthetic-token-private"
	for _, body := range []string{`{"code":"creator-account-data","message":"12345 synthetic-token-private"}`, `{"code":"12345 synthetic-token-private","message":"12345 synthetic-token-private"}`} {
		s := &autoRenewServer{putStatus: 403, overrides: map[string]string{"/gateway/api/v1/resources/autoRenew": body}}
		storageServer := httptest.NewServer(s.handler(t))
		defer storageServer.Close()
		billingCalls := 0
		billingServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			billingCalls++
			if r.Header.Get("Authorization") != "Bearer "+token {
				t.Error("billing bearer token missing")
			}
			s.handler(t).ServeHTTP(w, r)
		}))
		defer billingServer.Close()
		var logs bytes.Buffer
		captures := 0
		cfg, err := vngcloud.NewConfig(vngcloud.WithRegion("hcm-3"), vngcloud.WithStaticToken(token), vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{Storage: storageServer.URL, Billing: billingServer.URL}), vngcloud.WithLogger(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))), vngcloud.WithResponseCapture(func(capture vngcloud.ResponseCapture) {
			if strings.HasPrefix(capture.Operation, "billing") || capture.Method == http.MethodPut {
				captures++
			}
		}))
		if err != nil {
			t.Fatal(err)
		}
		c := New(cfg)
		c.now = func() time.Time { return time.UnixMilli(1790000000000) }
		_, err = c.PutProjectAutoRenew(context.Background(), &PutProjectAutoRenewInput{ProjectID: "new-1", Enabled: vngcloud.Ptr(true), MaxPrice: 30000})
		var api *core.APIError
		if !errors.As(err, &api) || captures != 0 || billingCalls != 3 {
			t.Fatal("billing endpoint or private capture policy")
		}
		for _, secret := range []string{token, "12345", "creator-account-data", "portal-user-id"} {
			if strings.Contains(err.Error()+api.Code+logs.String(), secret) {
				t.Fatalf("private value exposed: %s", secret)
			}
		}
	}
}

func TestProjectAutoRenewDisableDuringPricingOutage(t *testing.T) {
	s := &autoRenewServer{enabled: true, months: 6, overrides: map[string]string{
		"/internal/v1/billing/project_types": `{"success":false}`,
	}}
	c := autoRenewClient(t, s)
	out, err := c.PutProjectAutoRenew(context.Background(), &PutProjectAutoRenewInput{ProjectID: "new-1", Enabled: vngcloud.Ptr(false)})
	if err != nil || !out.Changed || s.prices != 0 {
		t.Fatal("disable depended on quote")
	}
}

func TestProjectAutoRenewExactPUTBody(t *testing.T) {
	s := &autoRenewServer{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			raw, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatal(err)
			}
			want := `[{"product":"vstorage","artifactType":"object-storage","artifactId":"new-1","channel":0,"autoRenewInfo":{"isEnable":true,"period":129600}}]`
			if string(raw) != want {
				t.Fatalf("body %s", raw)
			}
			r.Body = io.NopCloser(bytes.NewReader(raw))
		}
		s.handler(t).ServeHTTP(w, r)
	}))
	defer server.Close()
	cfg, err := vngcloud.NewConfig(vngcloud.WithRegion("hcm-3"), vngcloud.WithStaticToken("synthetic-token"), vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{Storage: server.URL, Billing: server.URL}))
	if err != nil {
		t.Fatal(err)
	}
	c := New(cfg)
	c.now = func() time.Time { return time.UnixMilli(1790000000000) }
	out, err := c.PutProjectAutoRenew(context.Background(), &PutProjectAutoRenewInput{ProjectID: "new-1", Enabled: vngcloud.Ptr(true), PeriodMonths: vngcloud.Ptr(3), MaxPrice: 90000})
	if err != nil || !out.Changed {
		t.Fatal(err)
	}
}

func TestProjectAutoRenewQuoteCatalogIdentity(t *testing.T) {
	s := &autoRenewServer{}
	catalogs := 0
	// The quoted catalog must match the project, even when catalogs change.
	c := New(testutil.NewConfig(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/internal/v1/billing/project_types" {
			catalogs++
			changed := strings.ReplaceAll(pricingTypes, `"id":1`, `"id":2`)
			changed = strings.ReplaceAll(changed, `"projectTypeId":1`, `"projectTypeId":2`)
			_, _ = w.Write([]byte(changed))
			return
		}
		s.handler(t).ServeHTTP(w, r)
	})))
	c.now = func() time.Time { return time.UnixMilli(1790000000000) }
	out, err := c.PutProjectAutoRenew(context.Background(), &PutProjectAutoRenewInput{ProjectID: "new-1", Enabled: vngcloud.Ptr(true), MaxPrice: 30000})
	if err == nil || s.puts != 0 || s.prices != 0 || catalogs != 1 || out.State.PriceStatus != "Unavailable" {
		t.Fatalf("catalog mismatch quoted or wrote: %v", err)
	}
}

func TestProjectAutoRenewCatalogChangeBeforeQuote(t *testing.T) {
	s := &autoRenewServer{}
	catalogs := 0
	c := New(testutil.NewConfig(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/internal/v1/billing/project_types" {
			catalogs++
			body := pricingTypes
			if catalogs > 1 {
				body = strings.ReplaceAll(body, `"id":1`, `"id":2`)
				body = strings.ReplaceAll(body, `"projectTypeId":1`, `"projectTypeId":2`)
			}
			_, _ = w.Write([]byte(body))
			return
		}
		if r.URL.Path == "/billing-api/v2/price" {
			s.prices++
			_, _ = w.Write([]byte(pricingQuote))
			return
		}
		s.handler(t).ServeHTTP(w, r)
	})))
	c.now = func() time.Time { return time.UnixMilli(1790000000000) }
	out, err := c.PutProjectAutoRenew(context.Background(), &PutProjectAutoRenewInput{ProjectID: "new-1", Enabled: vngcloud.Ptr(true), MaxPrice: 30000})
	// One catalog avoids a second selection that can price another type.
	if catalogs > 1 || err != nil || !out.Changed || s.prices != 1 || s.puts != 1 {
		t.Fatal("renewal quote selected a catalog twice")
	}
}
