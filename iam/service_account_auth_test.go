package iam

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/core"
)

type serviceAccountAuthTransport func(*http.Request) (*http.Response, error)

func (f serviceAccountAuthTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestServiceAccountAuthPreservesIAMGuards(t *testing.T) {
	body, err := os.ReadFile("../testdata/auth/service_account_token.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, attachedPolicy := range []bool{false, true} {
		g := unprivilegedPolicyFixture()
		g.caller = userInfoResponse{UserID: "caller-sa", UserType: callerTypeUserSA}
		if attachedPolicy {
			g.policyServiceAccountIDs = []string{"sa-other"}
		}
		writes, exchanges := 0, 0
		mux := g.mux(t, func(mux *http.ServeMux) {
			mux.HandleFunc("DELETE /accounts-api/v1/service-accounts/sa-other", func(http.ResponseWriter, *http.Request) { writes++ })
			mux.HandleFunc("PUT /policies-api/v1/policies/policy-1", func(http.ResponseWriter, *http.Request) { writes++ })
		})
		server := httptest.NewServer(mux)
		cfg, err := vngcloud.NewConfig(vngcloud.WithRegion("hcm-3"), vngcloud.WithServiceAccount(&vngcloud.ServiceAccountAuth{ClientID: "client", ClientSecret: "synthetic-secret"}), vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{IAM: server.URL + "/", Dashboard: server.URL + "/"}), vngcloud.WithHTTPClient(server.Client()), core.WithServiceAccountTestTransport(serviceAccountAuthTransport(func(*http.Request) (*http.Response, error) {
			exchanges++
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(body)))}, nil
		}), time.Now))
		if err != nil {
			server.Close()
			t.Fatal(err)
		}
		c := New(cfg)
		if attachedPolicy {
			_, err = c.UpdatePolicy(context.Background(), &UpdatePolicyInput{PolicyID: "policy-1", Description: vngcloud.Ptr("synthetic")})
		} else {
			_, err = c.DeleteServiceAccount(context.Background(), &DeleteServiceAccountInput{ServiceAccountID: "sa-other"})
		}
		server.Close()
		if !errors.Is(err, ErrSelfChange) || writes != 0 || exchanges != 1 {
			t.Fatalf("guard: %v writes %d exchanges %d", err, writes, exchanges)
		}
	}
}
