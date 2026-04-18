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

	accessToken, expiresAt, err := Login(context.Background(), LoginRequest{
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
	if accessToken != "token-value" {
		t.Fatalf("unexpected token: %s", accessToken)
	}
	if time.Until(expiresAt) < 59*time.Minute {
		t.Fatalf("unexpected expiry: %s", expiresAt)
	}
}
