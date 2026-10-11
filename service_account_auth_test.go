package vngcloud_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/network"
)

func TestServiceAccountPublicOption(t *testing.T) {
	auth := &vngcloud.ServiceAccountAuth{ClientID: "synthetic-client", ClientSecret: vngcloud.Secret("synthetic-secret")}
	option := vngcloud.WithServiceAccount(auth)
	original := *auth
	auth.ClientSecret = ""
	auth.ClientID = "invalid:changed"
	cfg, err := vngcloud.NewConfig(vngcloud.WithRegion("hcm-3"), option)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := cfg.Authenticate(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled authentication: %v", err)
	}
	for _, value := range []any{original, auth, option, cfg} {
		for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q"} {
			if strings.Contains(fmt.Sprintf(verb, value), "synthetic-secret") {
				t.Fatal("format leaked secret")
			}
		}
		b, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), "synthetic-secret") {
			t.Fatal("JSON leaked secret")
		}
	}
}

func TestServiceAccountPublicSelection(t *testing.T) {
	for _, auth := range []*vngcloud.ServiceAccountAuth{nil, {}, {ClientID: "synthetic"}, {ClientSecret: "synthetic-secret"}} {
		_, err := vngcloud.NewConfig(vngcloud.WithRegion("hcm-3"), vngcloud.WithServiceAccount(auth))
		if !errors.Is(err, vngcloud.ErrInvalidConfig) {
			t.Fatalf("invalid auth: %v", err)
		}
		if _, err := vngcloud.NewConfig(vngcloud.WithRegion("hcm-3"), vngcloud.WithServiceAccount(auth), vngcloud.WithStaticToken("synthetic")); err != nil {
			t.Fatal(err)
		}
	}
	var loginErr error = &vngcloud.ServiceAccountLoginError{Status: 401, Reason: "token endpoint rejected request", Err: vngcloud.ErrAuth}
	if !errors.Is(loginErr, vngcloud.ErrAuth) {
		t.Fatal("missing auth alias")
	}
	if vngcloud.ErrTokenCache.Error() != "vngcloud: token cache unavailable" {
		t.Fatal("cache alias")
	}
}

func TestServiceAccountDenialFixture(t *testing.T) {
	tokenBody, err := os.ReadFile("testdata/auth/service_account_token.json")
	if err != nil {
		t.Fatal(err)
	}
	var token struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(tokenBody, &token); err != nil {
		t.Fatal(err)
	}
	denied, err := os.ReadFile("testdata/auth/service_account_forbidden.json")
	if err != nil {
		t.Fatal(err)
	}
	sends := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sends++
		if r.Header.Get("Authorization") != "Bearer "+token.AccessToken {
			t.Error("wrong bearer token")
		}
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write(denied)
	}))
	defer server.Close()
	cfg, err := vngcloud.NewConfig(vngcloud.WithRegion("hcm-3"), vngcloud.WithProjectID("project-1"), vngcloud.WithStaticToken(token.AccessToken), vngcloud.WithTransport(serviceAccountFixtureTransport{origin: server.URL}), vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{VServer: server.URL, VNetwork: server.URL}))
	if err != nil {
		t.Fatal(err)
	}
	_, err = network.New(cfg).GetSecurityGroup(context.Background(), &network.GetSecurityGroupInput{SecurityGroupID: "group-1"})
	var api *vngcloud.APIError
	if !errors.Is(err, vngcloud.ErrPermission) || !errors.As(err, &api) || api.StatusCode != 403 || sends != 1 {
		t.Fatalf("denial: %v, sends %d", err, sends)
	}
}

type serviceAccountFixtureTransport struct{ origin string }

func (t serviceAccountFixtureTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if !strings.HasPrefix(r.URL.String(), t.origin+"/") {
		return nil, errors.New("non-fixture destination refused")
	}
	return http.DefaultTransport.RoundTrip(r)
}
