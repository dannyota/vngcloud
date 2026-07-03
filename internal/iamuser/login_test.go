package iamuser

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestLoginWithout2FA(t *testing.T) {
	var tokenURL string
	signin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, loginPath):
			_, _ = w.Write([]byte(`<html><input name="_csrf" value="csrf-login"></html>`))
		case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, loginPath):
			http.Redirect(w, r, tokenURL+"/callback?code=auth-code", http.StatusFound)
		default:
			http.NotFound(w, r)
		}
	}))
	defer signin.Close()

	token := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["code"] != "auth-code" {
			t.Fatalf("unexpected auth code: %s", body["code"])
		}
		_, _ = w.Write([]byte(`{"accessToken":"token-value","expiresIn":3600}`))
	}))
	defer token.Close()
	tokenURL = token.URL

	result, err := Login(context.Background(), LoginRequest{
		RootEmail:     "<root-email>",
		Username:      "user",
		Password:      "password",
		SigninBaseURL: signin.URL,
		TokenURL:      token.URL,
		DashboardURI:  token.URL + "/",
	})
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}
	if result.AccessToken != "token-value" {
		t.Fatalf("unexpected token: %s", result.AccessToken)
	}
	if time.Until(result.ExpiresAt) < 59*time.Minute {
		t.Fatalf("unexpected expiry: %s", result.ExpiresAt)
	}
}

func TestLoginErrorsOnLoginPageFailure(t *testing.T) {
	signin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer signin.Close()

	_, err := Login(context.Background(), LoginRequest{
		RootEmail: "r", Username: "u", Password: "p",
		SigninBaseURL: signin.URL, TokenURL: signin.URL, DashboardURI: signin.URL + "/",
	})
	if err == nil || !strings.Contains(err.Error(), "status 500") {
		t.Fatalf("expected status 500 error, got %v", err)
	}
}

func TestLoginErrorsOnMovedEndpoint(t *testing.T) {
	signin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			_, _ = w.Write([]byte(`<html><input name="_csrf" value="csrf-login"></html>`))
		default:
			w.Header().Set("Location", "https://elsewhere.example/ap/auth/iam/login")
			w.WriteHeader(http.StatusMovedPermanently)
		}
	}))
	defer signin.Close()

	_, err := Login(context.Background(), LoginRequest{
		RootEmail: "r", Username: "u", Password: "p",
		SigninBaseURL: signin.URL, TokenURL: signin.URL, DashboardURI: signin.URL + "/",
	})
	if err == nil || !strings.Contains(err.Error(), "moved permanently") {
		t.Fatalf("expected moved-endpoint error, got %v", err)
	}
}

func TestLoginErrorsOnFormRedisplay(t *testing.T) {
	signin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<html><input name="_csrf" value="csrf-login"></html>`))
	}))
	defer signin.Close()

	_, err := Login(context.Background(), LoginRequest{
		RootEmail: "r", Username: "u", Password: "p",
		SigninBaseURL: signin.URL, TokenURL: signin.URL, DashboardURI: signin.URL + "/",
	})
	if err == nil || !strings.Contains(err.Error(), "login rejected") {
		t.Fatalf("expected login-rejected error, got %v", err)
	}
}
