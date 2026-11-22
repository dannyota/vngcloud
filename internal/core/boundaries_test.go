package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"danny.vn/vngcloud/internal/endpoints"
	"danny.vn/vngcloud/internal/tokencache"
	"danny.vn/vngcloud/internal/transport"
)

func TestUnsafeRegionsSendNothing(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	defer server.Close()
	for _, region := range []string{"evil.test/", "hcm-3@evil.test", "../hcm-3", "hcm-3:443", "-hcm", "hcm-", "HCM-3", "a_b", strings.Repeat("a", 64), "hcm\n3"} {
		t.Run(region, func(t *testing.T) {
			_, err := NewConfig(WithRegion(region), WithStaticToken("synthetic"), WithEndpointOverrides(EndpointOverrides{VServer: server.URL}))
			if !errors.Is(err, ErrInvalidConfig) {
				t.Fatal("unsafe region accepted")
			}
		})
	}
	if calls.Load() != 0 {
		t.Fatal("invalid region sent a request")
	}
}

func TestProjectInventoryRejectsMissingAndDuplicateEvidence(t *testing.T) {
	for _, body := range []string{`{}`, `{"projects":null}`, `{"projects":[],"Projects":[{"projectId":"other"}]}`, `{"projects":[{"projectId":"one","ProjectId":"two"}]}`, `{"projects":[{"projectId":"one"},{"projectId":"one"}]}`, `{"projects":[{}]}`} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) }))
		client := NewTestClient("hcm-3", "", endpoints.Set{VServer: server.URL + "/"}, transport.New(transport.Config{HTTPClient: server.Client()}))
		_, err := client.ListProjects(context.Background(), nil)
		server.Close()
		if err == nil {
			t.Error("invalid project inventory accepted")
		}
	}
}

func TestInvalidIAMTokenWaitsBeforeNextLogin(t *testing.T) {
	setClock := withFakeClock(t)
	var codes, exchanges atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/token":
			exchanges.Add(1)
			_, _ = w.Write([]byte(`{"accessToken":"synthetic","expiresIn":0}`))
		case r.Method == "GET":
			_, _ = w.Write([]byte(`<input name="_csrf" value="csrf">`))
		case strings.Contains(r.URL.Path, "/google"):
			http.Redirect(w, r, "/callback?code=synthetic", http.StatusFound)
		default:
			http.Redirect(w, r, "/ap/auth/iam/google", http.StatusFound)
		}
	}))
	defer server.Close()
	for _, disk := range []bool{false, true} {
		var steps []int64
		auth := &IAMUserAuth{RootEmail: "root", Username: "user", Password: "password", SigninBaseURL: server.URL, TokenURL: server.URL + "/token", TOTP: TOTPFunc(func(context.Context) (string, error) {
			codes.Add(1)
			steps = append(steps, timeNow().Unix()/30)
			return "123456", nil
		})}
		auth.waitTOTP = func(_ context.Context, delay time.Duration) error { setClock(timeNow().Add(delay)); return nil }
		source := &iamTokenSource{auth: auth}
		if disk {
			source.cache = tokencache.New(filepath.Join(t.TempDir(), "cache"), timeNow)
		}
		before := codes.Load()
		for range 2 {
			if _, err := source.Token(context.Background()); !errors.Is(err, ErrAuth) {
				t.Fatal("invalid lifespan did not fail authentication")
			}
		}
		if codes.Load()-before != 2 || len(steps) != 2 || steps[1] <= steps[0] {
			t.Fatalf("expected distinct increasing TOTP steps, got %v", steps)
		}
	}
	if exchanges.Load() != 4 {
		t.Error("unexpected exchange count")
	}
}

func TestServiceAccountRejectsCaseVariantTokenKeys(t *testing.T) {
	now := time.Now()
	for _, body := range []string{`{"access_token":"first","ACCESS_TOKEN":"second","expires_in":3600}`, `{"access_token":"first","expires_in":3600,"EXPIRES_IN":1}`, `{"access_token":"first","expires_in":3600,"token_type":"Bearer","TOKEN_TYPE":"other"}`} {
		if _, err := decodeServiceAccountToken([]byte(body), now, now); err == nil {
			t.Fatal("ambiguous service-account token accepted")
		}
	}
}

func TestPrivateTokenSourcesDoNotFormatTokens(t *testing.T) {
	iam := &iamTokenSource{rejected: newServiceAccountSecret("REJECTEDCANARY")}
	sa := newServiceAccountTokenSource(nil, time.Second, nil, nil, nil)
	sa.cached = &transport.Token{AccessToken: "CACHEDCANARY", ExpiresAt: time.Now().Add(time.Hour)}
	sa.rejected = newServiceAccountSecret("REJECTEDCANARY")
	for _, source := range []any{iam, sa} {
		for _, verb := range []string{"%v", "%+v", "%#v"} {
			output := fmt.Sprintf(verb, source)
			if strings.Contains(output, "REJECTEDCANARY") || strings.Contains(output, "CACHEDCANARY") {
				t.Error("private token source disclosed credentials")
			}
		}
	}
}

func TestPrivateStaticTokenDoesNotFormat(t *testing.T) {
	c, err := newClient(WithRegion("hcm-3"), WithStaticToken("STATICCANARY"))
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Authenticate(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, verb := range []string{"%v", "%+v", "%#v"} {
		if strings.Contains(fmt.Sprintf(verb, c.transport), "STATICCANARY") {
			t.Error("static token disclosed")
		}
	}
}

func TestIAMOptionSnapshotsCredentialsAndCacheIdentity(t *testing.T) {
	seed := &SecretTOTP{Secret: "JBSWY3DPEHPK3PXP"}
	auth := &IAMUserAuth{RootEmail: "root-one", Username: "user-one", Password: "password-one", TOTP: seed}
	option := WithIAMUser(auth)
	auth.Username, auth.RootEmail, auth.Password = "user-two", "root-two", "password-two"
	seed.Secret = "OTHERSEED"
	settings := defaultClientConfig()
	option.apply(&settings)
	snapshot := settings.iamUser
	if snapshot == auth || snapshot.Username != "user-one" || snapshot.RootEmail != "root-one" || snapshot.Password != "password-one" {
		t.Fatal("caller mutation changed credentials or cache identity")
	}
	if snapshot.TOTP.(*SecretTOTP).Secret != "JBSWY3DPEHPK3PXP" {
		t.Fatal("caller mutation changed TOTP seed")
	}

}

func TestIAMCredentialsFormattingAndJSON(t *testing.T) {
	auth := &IAMUserAuth{RootEmail: "root", Username: "user", Password: "PASSWORDCANARY", TOTP: &SecretTOTP{Secret: "TOTPCANARY"}}
	if err := auth.remember(context.Background(), "TOKENCANARY", time.Now().Add(time.Hour), time.Now()); err != nil {
		t.Fatal(err)
	}
	for _, value := range []any{auth, reflect.ValueOf(auth).Elem().Interface(), auth.Password, auth.TOTP} {
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		for _, output := range []string{string(raw), fmt.Sprintf("%v", value), fmt.Sprintf("%+v", value), fmt.Sprintf("%#v", value)} {
			for _, secret := range []string{"PASSWORDCANARY", "TOTPCANARY", "TOKENCANARY"} {
				if strings.Contains(output, secret) {
					t.Error("credential disclosed")
				}
			}
		}
	}
}
