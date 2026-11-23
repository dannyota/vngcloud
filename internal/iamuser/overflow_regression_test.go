package iamuser

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type overflowReader struct{ count int }

func (r *overflowReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 'x'
	}
	r.count += len(p)
	if r.count >= (1<<20)+1 {
		return len(p), errors.New("synthetic read failure")
	}
	return len(p), nil
}

func TestLoginOverflowReadError(t *testing.T) {
	r := &overflowReader{}
	body, err := readLoginBody(r)
	if body != nil || err == nil || err.Error() != "login response too large" {
		t.Fatalf("body %d error %v", len(body), err)
	}
	if r.count != (1<<20)+1 {
		t.Fatalf("read %d bytes", r.count)
	}
}

func TestLoginPageOverflowSanitized(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, strings.Repeat("secret", (1<<20)/6+1))
	}))
	defer server.Close()
	_, err := Login(context.Background(), LoginRequest{SigninBaseURL: server.URL, HTTPClient: server.Client()})
	var failure *LoginFailure
	if !errors.As(err, &failure) || failure.Step != StepSigninPage || !strings.Contains(err.Error(), "login response too large") || strings.Contains(err.Error(), "secret") {
		t.Fatalf("error %v", err)
	}
}

func TestLoginResponseBoundaries(t *testing.T) {
	for _, stage := range []string{"page", "token"} {
		for _, overflow := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/overflow=%t", stage, overflow), func(t *testing.T) {
				page := `<input name="_csrf" value="csrf">`
				token := `{"accessToken":"synthetic","expiresIn":3600}`
				size := 1 << 20
				if overflow {
					size++
				}
				if stage == "page" {
					page += strings.Repeat(" ", size-len(page))
				} else {
					token += strings.Repeat(" ", size-len(token))
				}
				client := &http.Client{Transport: boundaryRoundTripper(func(r *http.Request) (*http.Response, error) {
					status, body := http.StatusOK, page
					header := make(http.Header)
					if r.URL.Path == "/token" {
						body = token
					} else if r.Method == http.MethodPost {
						status = http.StatusFound
						header.Set("Location", "/callback?code=synthetic")
					}
					return &http.Response{StatusCode: status, Header: header, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
				})}
				_, err := Login(context.Background(), LoginRequest{SigninBaseURL: "http://example.test", TokenURL: "http://example.test/token", HTTPClient: client})
				if !overflow {
					if err != nil {
						t.Fatal(err)
					}
					return
				}
				var failure *LoginFailure
				step := StepSigninPage
				if stage == "token" {
					step = StepTokenExchange
				}
				if !errors.As(err, &failure) || failure.Step != step || !errors.Is(err, ErrResponseTooLarge) || strings.Contains(err.Error(), "synthetic") {
					t.Fatalf("error %v", err)
				}
			})
		}
	}
}

type cancelOverflowReader struct {
	count  int
	cancel context.CancelFunc
}

func (r *cancelOverflowReader) Read(p []byte) (int, error) {
	r.count += len(p)
	if r.count == (1<<20)+1 {
		r.cancel()
		return len(p), errors.New("synthetic")
	}
	return len(p), nil
}

func TestLoginOverflowContextPrecedence(t *testing.T) {
	for _, stage := range []string{"page", "token"} {
		t.Run(stage, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			client := &http.Client{Transport: boundaryRoundTripper(func(r *http.Request) (*http.Response, error) {
				status := http.StatusOK
				body := io.NopCloser(strings.NewReader(`<input name="_csrf" value="csrf">`))
				header := make(http.Header)
				if stage == "page" || r.URL.Path == "/token" {
					body = io.NopCloser(&cancelOverflowReader{cancel: cancel})
				} else if r.Method == http.MethodPost {
					status = http.StatusFound
					header.Set("Location", "/callback?code=synthetic")
				}
				return &http.Response{StatusCode: status, Header: header, Body: body, Request: r}, nil
			})}
			_, err := Login(ctx, LoginRequest{SigninBaseURL: "http://example.test", TokenURL: "http://example.test/token", HTTPClient: client})
			if !errors.Is(err, context.Canceled) || errors.Is(err, ErrResponseTooLarge) {
				t.Fatalf("error %v", err)
			}
		})
	}
}

type wrappedContextReader struct{ err error }

func (r wrappedContextReader) Read([]byte) (int, error) {
	return 0, fmt.Errorf("private value: %w", r.err)
}

func TestLoginReadContextErrorSanitized(t *testing.T) {
	for _, want := range []error{context.Canceled, context.DeadlineExceeded} {
		_, err := readLoginBody(wrappedContextReader{err: want})
		if !errors.Is(err, want) || err.Error() != want.Error() || errors.Unwrap(err) != nil {
			t.Fatalf("error %v", err)
		}
	}
}
