package iamuser

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

func TestLoginResponseBounds(t *testing.T) {
	const limit = 1 << 20
	for _, n := range []int{limit, limit + 1} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(strings.Repeat(" ", n))) }))
		body, status, err := doGet(context.Background(), server.Client(), server.URL)
		server.Close()
		if status != 200 || (n == limit && (err != nil || len(body) != n)) || (n > limit && (err == nil || body != nil)) {
			t.Fatalf("n=%d status=%d err=%v", n, status, err)
		}
	}
}

func TestTokenExchangeRejectsInvalidLifetimeAndAliases(t *testing.T) {
	for _, body := range []string{
		`{"accessToken":"synthetic"}`, `{"accessToken":"synthetic","expiresIn":null}`,
		`{"accessToken":"synthetic","expiresIn":0}`, `{"accessToken":"synthetic","expiresIn":-1}`,
		`{"accessToken":"synthetic","expiresIn":30}`, `{"accessToken":"synthetic","expiresIn":9223372037}`,
		`{"accessToken":"synthetic","expiresIn":3600,"expiresIn":3601}`,
		`{"accessToken":"synthetic","expiresIn":3600,"ExpiresIn":3601}`,
		`{"accessToken":"synthetic","AccessToken":"other","expiresIn":3600}`,
		`{"accessToken":"synthetic","expiresIn":3600}` + strings.Repeat(" ", 1<<20),
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) }))
		_, err := exchangeToken(context.Background(), server.Client(), server.URL, "http://example.test/", "code", "verifier")
		server.Close()
		if err == nil {
			t.Error("invalid token response accepted")
		}
	}
}

type boundaryRoundTripper func(*http.Request) (*http.Response, error)

func (f boundaryRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type boundaryReadError struct{ io.Reader }

func (boundaryReadError) Read([]byte) (int, error) { return 0, errors.New("synthetic read failure") }

func TestTokenExchangeRejectsReadError(t *testing.T) {
	client := &http.Client{Transport: boundaryRoundTripper(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: make(http.Header), Request: r, Body: io.NopCloser(io.MultiReader(strings.NewReader(`{"accessToken":"synthetic","expiresIn":3600}`), boundaryReadError{}))}, nil
	})}
	if _, err := exchangeToken(context.Background(), client, "http://example.test/", "http://example.test/", "code", "verifier"); err == nil {
		t.Fatal("read error accepted")
	}
}

func TestLoginExpiryStartsBeforeExchange(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		start := time.Now()
		client := &http.Client{Transport: boundaryRoundTripper(func(r *http.Request) (*http.Response, error) {
			status, body := 200, `<input name="_csrf" value="csrf">`
			header := make(http.Header)
			if r.Method == "POST" {
				if r.URL.Path == "/token" {
					time.Sleep(10 * time.Second)
					body = `{"accessToken":"synthetic","expiresIn":3600}`
				} else {
					status = 302
					header.Set("Location", "http://example.test/?code=code")
				}
			}
			return &http.Response{StatusCode: status, Header: header, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
		})}
		result, err := Login(context.Background(), LoginRequest{RootEmail: "root", Username: "user", Password: "password", HTTPClient: client, SigninBaseURL: "http://example.test", TokenURL: "http://example.test/token"})
		if err != nil {
			t.Fatal(err)
		}
		if !result.ExpiresAt.Equal(start.Add(time.Hour)) {
			t.Fatal("exchange latency extended token lifetime")
		}
	})
}
