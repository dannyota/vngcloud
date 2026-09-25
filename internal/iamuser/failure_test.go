package iamuser

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// asFailure extracts the *LoginFailure Login returned, failing the test if
// err is not one.
func asFailure(t *testing.T, err error) *LoginFailure {
	t.Helper()
	var failure *LoginFailure
	if !errors.As(err, &failure) {
		t.Fatalf("error is not a *LoginFailure: %v", err)
	}
	return failure
}

func TestLoginFailureStepSigninPage(t *testing.T) {
	signin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer signin.Close()

	_, err := Login(context.Background(), LoginRequest{
		RootEmail: "r", Username: "u", Password: "p",
		SigninBaseURL: signin.URL, TokenURL: signin.URL, DashboardURI: signin.URL + "/",
	})
	failure := asFailure(t, err)
	if failure.Step != StepSigninPage {
		t.Fatalf("Step = %v, want StepSigninPage", failure.Step)
	}
	if failure.Status != http.StatusInternalServerError {
		t.Fatalf("Status = %d, want 500", failure.Status)
	}
	if failure.CaptchaSuspected {
		t.Fatal("CaptchaSuspected = true, want false")
	}
}

func TestLoginFailureStepSigninRejectedSuspectsCaptcha(t *testing.T) {
	signin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<html><input name="_csrf" value="csrf-login"></html>`))
	}))
	defer signin.Close()

	_, err := Login(context.Background(), LoginRequest{
		RootEmail: "r", Username: "u", Password: "p",
		SigninBaseURL: signin.URL, TokenURL: signin.URL, DashboardURI: signin.URL + "/",
	})
	failure := asFailure(t, err)
	if failure.Step != StepSigninRejected {
		t.Fatalf("Step = %v, want StepSigninRejected", failure.Step)
	}
	if failure.Status != http.StatusOK {
		t.Fatalf("Status = %d, want 200", failure.Status)
	}
	if !failure.CaptchaSuspected {
		t.Fatal("CaptchaSuspected = false, want true")
	}
}

func TestLoginFailureStepTokenExchangeStatus(t *testing.T) {
	signin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			_, _ = w.Write([]byte(`<html><input name="_csrf" value="csrf-login"></html>`))
		default:
			http.Redirect(w, r, "/callback?code=auth-code", http.StatusFound)
		}
	}))
	defer signin.Close()
	tokenServer := httptest.NewServer(http.NotFoundHandler())
	defer tokenServer.Close()

	_, err := Login(context.Background(), LoginRequest{
		RootEmail: "r", Username: "u", Password: "p",
		SigninBaseURL: signin.URL, TokenURL: tokenServer.URL, DashboardURI: signin.URL + "/",
	})
	failure := asFailure(t, err)
	if failure.Step != StepTokenExchange {
		t.Fatalf("Step = %v, want StepTokenExchange", failure.Step)
	}
	if failure.Status != http.StatusNotFound {
		t.Fatalf("Status = %d, want 404 (token server has no handler)", failure.Status)
	}
}

// TestLoginFailureNeverExposesMarkersOutsideCause plants distinctive marker
// values in the username, password, TOTP code, authorization code, and the
// token endpoint's response body, drives a full login that fails at the
// token exchange step, and checks that LoginFailure's exported fields (Step,
// Status, CaptchaSuspected) carry none of them. The underlying cause is
// allowed to hold them; core builds its own message from these fields
// instead of the cause, precisely because the cause is not held to this
// rule.
func TestLoginFailureNeverExposesMarkersOutsideCause(t *testing.T) {
	const (
		usernameMarker  = "marker-username-af92"
		passwordMarker  = "marker-password-af92"
		totpMarker      = "marker-totp-af92"
		authCodeMarker  = "marker-authcode-af92"
		tokenBodyMarker = "marker-tokenbody-af92"
	)

	var tokenURLRef string
	signin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, twoFAPathMatch):
			_, _ = w.Write([]byte(`<html><input name="_csrf" value="csrf-2fa"></html>`))
		case r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`<html><input name="_csrf" value="csrf-login"></html>`))
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, twoFAPathMatch):
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

	_, err := Login(context.Background(), LoginRequest{
		RootEmail:     "root@example.test",
		Username:      usernameMarker,
		Password:      passwordMarker,
		TOTP:          TOTPFuncForTest(func(context.Context) (string, error) { return totpMarker, nil }),
		SigninBaseURL: signin.URL,
		TokenURL:      token.URL,
		DashboardURI:  token.URL + "/",
	})
	failure := asFailure(t, err)
	if failure.Step != StepTokenExchange {
		t.Fatalf("Step = %v, want StepTokenExchange", failure.Step)
	}
	if failure.Status != http.StatusBadRequest {
		t.Fatalf("Status = %d, want 400", failure.Status)
	}
	for _, marker := range []string{usernameMarker, passwordMarker, totpMarker, authCodeMarker, tokenBodyMarker} {
		if strings.Contains(string(failure.Step), marker) {
			t.Fatalf("Step %q contains marker %q", failure.Step, marker)
		}
	}
}

// TOTPFuncForTest adapts a function to TOTPProvider, mirroring core.TOTPFunc,
// which this package must not import (it would be a dependency cycle: core
// imports iamuser).
type TOTPFuncForTest func(ctx context.Context) (string, error)

func (f TOTPFuncForTest) GetCode(ctx context.Context) (string, error) { return f(ctx) }
