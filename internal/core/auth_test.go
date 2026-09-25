package core

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// walkErrorText collects Error() from err and every error reachable by
// repeatedly unwrapping it (this package never joins several errors into
// one, so a single-chain walk is enough), so a test can check that no marker
// leaked anywhere in the chain rather than only in the top-level message.
func walkErrorText(err error) []string {
	var texts []string
	for err != nil {
		texts = append(texts, err.Error())
		err = errors.Unwrap(err)
	}
	return texts
}

func assertNoMarkers(t *testing.T, err error, markers ...string) {
	t.Helper()
	for _, text := range walkErrorText(err) {
		for _, marker := range markers {
			if strings.Contains(text, marker) {
				t.Fatalf("error chain leaked marker %q: %q", marker, text)
			}
		}
	}
}

// authClient builds a Client configured for IAM User login against signin
// and dashboard/token endpoints, without a token cache.
func authClient(t *testing.T, auth *IAMUserAuth, signinURL, dashboardURL string) *Client {
	t.Helper()
	c, err := newClient(
		WithRegion("hcm-3"),
		WithIAMUser(auth),
		WithEndpointOverrides(EndpointOverrides{Signin: signinURL, Dashboard: dashboardURL}),
	)
	if err != nil {
		t.Fatalf("newClient() error = %v", err)
	}
	return c
}

func TestLoginErrorForSigninPageFailure(t *testing.T) {
	signin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer signin.Close()

	c := authClient(t, &IAMUserAuth{RootEmail: "r", Username: "u", Password: "p"}, signin.URL, signin.URL+"/")
	err := c.Authenticate(context.Background())

	var loginErr *LoginError
	if !errors.As(err, &loginErr) {
		t.Fatalf("error is not a *LoginError: %v", err)
	}
	if loginErr.Status != http.StatusInternalServerError {
		t.Fatalf("Status = %d, want 500", loginErr.Status)
	}
	if loginErr.CaptchaSuspected {
		t.Fatal("CaptchaSuspected = true, want false")
	}
	if !errors.Is(err, ErrAuth) {
		t.Fatalf("errors.Is(err, ErrAuth) = false")
	}
}

func TestLoginErrorSuspectsCaptchaOnFormRedisplay(t *testing.T) {
	signin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<html><input name="_csrf" value="csrf-login"></html>`))
	}))
	defer signin.Close()

	c := authClient(t, &IAMUserAuth{RootEmail: "r", Username: "u", Password: "p"}, signin.URL, signin.URL+"/")
	err := c.Authenticate(context.Background())

	var loginErr *LoginError
	if !errors.As(err, &loginErr) {
		t.Fatalf("error is not a *LoginError: %v", err)
	}
	if !loginErr.CaptchaSuspected {
		t.Fatal("CaptchaSuspected = false, want true")
	}
	if loginErr.Status != http.StatusOK {
		t.Fatalf("Status = %d, want 200", loginErr.Status)
	}
}

func TestLoginErrorOnCanceledContext(t *testing.T) {
	signin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<html><input name="_csrf" value="csrf-login"></html>`))
	}))
	defer signin.Close()

	c := authClient(t, &IAMUserAuth{RootEmail: "r", Username: "u", Password: "p"}, signin.URL, signin.URL+"/")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := c.Authenticate(ctx)

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("errors.Is(err, context.Canceled) = false, err = %v", err)
	}
	if errors.Is(err, ErrAuth) {
		t.Fatal("errors.Is(err, ErrAuth) = true, want false for a canceled attempt")
	}
}

// TestLoginErrorNeverLeaksMarkers drives a full login flow with distinctive
// marker values standing in for the username, password, TOTP code,
// authorization code, and the token endpoint's response body, and fails at
// the token exchange step so every marker is exercised. It asserts that none
// of them appears anywhere in the returned error's chain: LoginError.Err is
// only ErrAuth or ctx.Err(), and its message is fixed text, so the
// underlying iamuser failure (whose cause can hold the token response body
// or a full URL) must never be wrapped in or reachable from what
// Authenticate returns.
func TestLoginErrorNeverLeaksMarkers(t *testing.T) {
	const (
		usernameMarker  = "marker-username-e71c"
		passwordMarker  = "marker-password-e71c"
		totpMarker      = "marker-totp-e71c"
		authCodeMarker  = "marker-authcode-e71c"
		tokenBodyMarker = "marker-tokenbody-e71c"
	)

	var tokenURLRef string
	signin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/ap/auth/iam/google"):
			_, _ = w.Write([]byte(`<html><input name="_csrf" value="csrf-2fa"></html>`))
		case r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`<html><input name="_csrf" value="csrf-login"></html>`))
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/ap/auth/iam/google"):
			if err := r.ParseForm(); err != nil {
				t.Fatal(err)
			}
			if got := r.PostFormValue("token"); got != totpMarker {
				t.Fatalf("2FA token = %q, want %q", got, totpMarker)
			}
			http.Redirect(w, r, tokenURLRef+"/callback?code="+authCodeMarker, http.StatusFound)
		default:
			if err := r.ParseForm(); err != nil {
				t.Fatal(err)
			}
			if got := r.PostFormValue("username"); got != usernameMarker {
				t.Fatalf("username = %q, want %q", got, usernameMarker)
			}
			if got := r.PostFormValue("password"); got != passwordMarker {
				t.Fatalf("password = %q, want %q", got, passwordMarker)
			}
			http.Redirect(w, r, "/ap/auth/iam/google/verify", http.StatusFound)
		}
	}))
	defer signin.Close()

	token := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant","detail":"` + tokenBodyMarker + `"}`))
	}))
	defer token.Close()
	tokenURLRef = token.URL

	auth := &IAMUserAuth{
		RootEmail: "root@example.test",
		Username:  usernameMarker,
		Password:  passwordMarker,
		TOTP:      TOTPFunc(func(context.Context) (string, error) { return totpMarker, nil }),
	}
	c, err := newClient(
		WithRegion("hcm-3"),
		WithIAMUser(auth),
		WithEndpointOverrides(EndpointOverrides{Signin: signin.URL, Dashboard: token.URL + "/", Token: token.URL}),
	)
	if err != nil {
		t.Fatalf("newClient() error = %v", err)
	}

	authErr := c.Authenticate(context.Background())
	if authErr == nil {
		t.Fatal("Authenticate() error = nil, want an error")
	}

	var loginErr *LoginError
	if !errors.As(authErr, &loginErr) {
		t.Fatalf("error is not a *LoginError: %v", authErr)
	}
	if loginErr.Status != http.StatusBadRequest {
		t.Fatalf("Status = %d, want 400", loginErr.Status)
	}
	if loginErr.Reason != "could not exchange the authorization code for a token" {
		t.Fatalf("Reason = %q", loginErr.Reason)
	}
	if !errors.Is(authErr, ErrAuth) {
		t.Fatal("errors.Is(authErr, ErrAuth) = false")
	}

	assertNoMarkers(t, authErr, usernameMarker, passwordMarker, totpMarker, authCodeMarker, tokenBodyMarker)
}
