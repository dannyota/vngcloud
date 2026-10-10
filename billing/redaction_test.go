package billing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/endpoints"
	"danny.vn/vngcloud/internal/transport"
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

type rotatingEnvelopeTokens struct{ count atomic.Int64 }

func (s *rotatingEnvelopeTokens) Token(context.Context) (transport.Token, error) {
	return transport.Token{AccessToken: fmt.Sprintf("synthetic-token-%d", s.count.Add(1)), ExpiresAt: time.Now().Add(time.Hour)}, nil
}
func (*rotatingEnvelopeTokens) Invalidate(string) {}

func TestEnvelopeRedactsSentTokenAfterConcurrentRefresh(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if r.URL.Path == "/refresh" {
			if token == "synthetic-token-1" {
				w.WriteHeader(401)
			}
			_, _ = w.Write([]byte(`{}`))
			return
		}
		close(started)
		<-release
		_, _ = fmt.Fprintf(w, `{"success":false,"code":%q,"message":%q}`, token, "bad "+token)
	}))
	defer server.Close()
	tc := transport.New(transport.Config{HTTPClient: server.Client(), TokenSource: &rotatingEnvelopeTokens{}})
	cfg := core.NewTestConfig("hcm-3", "", endpoints.Set{Region: "hcm-3", Billing: server.URL + "/"}, tc)
	result := make(chan error, 1)
	go func() { _, err := New(cfg).ListBudgets(context.Background(), nil); result <- err }()
	select {
	case <-started:
	case err := <-result:
		close(release)
		t.Fatalf("request did not reach server: %v", err)
	}
	refreshErr := tc.DoJSON(context.Background(), transport.Request{URL: server.URL + "/refresh"}, nil)
	close(release)
	if refreshErr != nil {
		t.Fatal(refreshErr)
	}
	err := <-result
	var ae *core.APIError
	if !errors.As(err, &ae) {
		t.Fatalf("error = %v", err)
	}
	for _, text := range []string{ae.Message, ae.Code, ae.Error()} {
		if strings.Contains(text, "synthetic-token-1") {
			t.Error("sent token leaked after refresh")
		}
	}
}

func TestEnvelopeRejectsStructuredCodes(t *testing.T) {
	c := New(core.NewTestConfig("hcm-3", "", endpoints.Set{}, transport.New(transport.Config{})))
	for _, status := range []int{200, 400} {
		for _, code := range []string{`{"value":"\u0073ynthetic-token-1"}`, `["\u0073ynthetic-token-1"]`, `true`} {
			err := c.finishEnvelope(transport.Request{}, status, envelope{Code: json.RawMessage(code), Message: "bad"}, nil)
			var ae *core.APIError
			if !errors.As(err, &ae) {
				t.Fatalf("error = %v", err)
			}
			if ae.Code != core.ResolvedCode(status, "") {
				t.Errorf("unsupported code = %q", ae.Code)
			}
		}
	}
}
