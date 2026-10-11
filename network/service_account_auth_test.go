package network

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/core"
)

type serviceAccountAuthTransport func(*http.Request) (*http.Response, error)

func (f serviceAccountAuthTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestServiceAccountNATWritesRefused(t *testing.T) {
	var exchanges, products atomic.Int64
	auth := vngcloud.WithServiceAccount(&vngcloud.ServiceAccountAuth{ClientID: "client", ClientSecret: "synthetic-secret"})
	for _, region := range []string{"han-1", "hcm-3"} {
		cfg, err := vngcloud.NewConfig(vngcloud.WithRegion(region), vngcloud.WithProjectID("project-1"), auth, core.WithServiceAccountTestTransport(serviceAccountAuthTransport(func(*http.Request) (*http.Response, error) {
			exchanges.Add(1)
			return nil, errors.New("unexpected exchange")
		}), time.Now), vngcloud.WithTransport(serviceAccountAuthTransport(func(*http.Request) (*http.Response, error) {
			products.Add(1)
			return nil, errors.New("unexpected product request")
		})))
		if err != nil {
			t.Fatal(err)
		}
		c := New(cfg)
		if _, err := c.CreateNATInstance(context.Background(), natTestInput()); !errors.Is(err, vngcloud.ErrInvalidConfig) {
			t.Fatal(err)
		}
		if _, err := c.DeleteNATInstance(context.Background(), &DeleteNATInstanceInput{ZoneID: natTestInput().ZoneID, VPCID: "vpc-1", NATID: "nat-1"}); !errors.Is(err, vngcloud.ErrInvalidConfig) {
			t.Fatal(err)
		}
	}
	if exchanges.Load() != 0 || products.Load() != 0 {
		t.Fatal("refused paid write sent requests")
	}
}

func TestServiceAccountNATQuoteRetainsReadAuth(t *testing.T) {
	body, err := os.ReadFile("../testdata/auth/service_account_token.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, region := range []string{"han-1", "hcm-3"} {
		exchanges := 0
		rt := serviceAccountAuthTransport(func(r *http.Request) (*http.Response, error) {
			exchanges++
			if r.URL.String() != "https://iamapis.vngcloud.vn/accounts-api/v2/auth/token" {
				t.Fatal("wrong private endpoint")
			}
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(body)))}, nil
		})
		s := new(natScenario)
		c := natWriteTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { s.serve(t, w, r) }), vngcloud.WithRegion(region), vngcloud.WithServiceAccount(&vngcloud.ServiceAccountAuth{ClientID: "client", ClientSecret: "synthetic-secret"}), core.WithServiceAccountTestTransport(rt, time.Now))
		out, err := c.QuoteCreateNATInstance(context.Background(), natTestInput())
		if err != nil || out == nil || out.TotalPrice != 100 || s.orders != 0 || s.puts != 0 || exchanges != 1 {
			t.Fatalf("quote: %v, exchanges %d", err, exchanges)
		}
	}
}
