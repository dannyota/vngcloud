package transport

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func TestCredentialEchoRedaction(t *testing.T) {
	for _, apiKey := range []bool{false, true} {
		t.Run(strconv.FormatBool(apiKey), func(t *testing.T) {
			var captured []byte
			var logs bytes.Buffer
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				credential := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
				w.WriteHeader(http.StatusBadRequest)
				_, _ = fmt.Fprintf(w, `{"message":"bad %s again %s","code":"%s","extra":"%s"}`, credential, credential, credential, credential)
			}))
			defer server.Close()
			c := New(Config{HTTPClient: server.Client(), TokenSource: &longTokens{}, Capture: func(c Capture) { captured = c.Body }, Logger: slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))})
			req := Request{URL: server.URL}
			credential := "synthetic-token-1"
			if apiKey {
				req.APIKey = testAPIKey
				credential = testAPIKey
			}
			err := c.DoJSON(context.Background(), req, nil)
			var ae *APIError
			if !errors.As(err, &ae) {
				t.Fatalf("error = %v", err)
			}
			for name, value := range map[string]string{"error": err.Error(), "message": ae.Message, "code": ae.Code, "capture": string(captured), "log": logs.String()} {
				if strings.Contains(value, credential) {
					t.Errorf("%s leaked credential", name)
				}
			}
			wantMessage := "bad [redacted] again [redacted]"
			if ae.Message != wantMessage || ae.Code != "[redacted]" {
				t.Errorf("message/code not redacted")
			}
		})
	}
}

type longTokens struct{ testTokenSource }

func (s *longTokens) Token(ctx context.Context) (Token, error) {
	token, err := s.testTokenSource.Token(ctx)
	token.AccessToken = "synthetic-" + token.AccessToken
	return token, err
}

func TestRefreshedCredentialEchoRedaction(t *testing.T) {
	var captured [][]byte
	var attempts int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		credential := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if attempts == 1 {
			w.WriteHeader(http.StatusUnauthorized)
		} else {
			w.WriteHeader(http.StatusBadRequest)
		}
		_, _ = fmt.Fprintf(w, `{"message":"bad %s","code":"%s"}`, credential, credential)
	}))
	defer server.Close()
	c := New(Config{HTTPClient: server.Client(), TokenSource: &longTokens{}, Capture: func(c Capture) { captured = append(captured, c.Body) }})
	err := c.DoJSON(context.Background(), Request{URL: server.URL}, nil)
	var ae *APIError
	if !errors.As(err, &ae) || ae.Message != "bad [redacted]" || ae.Code != "[redacted]" {
		t.Fatalf("error = %v", err)
	}
	if attempts != 2 || len(captured) != 2 {
		t.Fatalf("attempts %d, captures %d", attempts, len(captured))
	}
	for _, body := range captured {
		if strings.Contains(string(body), "synthetic-token-") {
			t.Fatal("capture leaked an attempt's token")
		}
	}
}

func TestNetworkFailureKeepsMatchingAndRetries(t *testing.T) {
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded, errors.New("http://host/path?secret=synthetic-secret")} {
		rt := &stubRoundTripper{failErr: cause}
		c := New(Config{HTTPClient: &http.Client{Transport: rt}})
		err := c.DoJSON(context.Background(), Request{URL: "http://host/path", SkipAuth: true}, nil)
		if !errors.Is(err, cause) {
			t.Fatal("cause matching lost")
		}
		if strings.Contains(err.Error(), "synthetic-secret") || strings.Contains(err.Error(), "http://") {
			t.Fatal("cause leaked")
		}
	}
	rt := &stubRoundTripper{failErr: errors.New("http://host/path?secret=synthetic-secret")}
	c := New(Config{HTTPClient: &http.Client{Transport: rt}, RetryCount: 1})
	if err := c.DoJSON(context.Background(), Request{URL: "http://host/path", SkipAuth: true}, nil); err != nil {
		t.Fatal(err)
	}
	if rt.calls.Load() != 2 {
		t.Fatalf("attempts = %d, want 2", rt.calls.Load())
	}
}

func TestRawCaptureRedactsCredentialWithoutChangingBody(t *testing.T) {
	for _, apiKey := range []bool{false, true} {
		t.Run(strconv.FormatBool(apiKey), func(t *testing.T) {
			var captured []byte
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")))
			}))
			defer server.Close()
			c := New(Config{HTTPClient: server.Client(), TokenSource: &longTokens{}, Capture: func(c Capture) { captured = c.Body }})
			req := Request{URL: server.URL}
			credential := "synthetic-token-1"
			if apiKey {
				req.APIKey = testAPIKey
				credential = testAPIKey
			}
			_, _, body, err := c.DoRaw(context.Background(), req)
			if err != nil {
				t.Fatal(err)
			}
			if string(body) != credential {
				t.Fatal("raw body changed")
			}
			if string(captured) != "[redacted]" {
				t.Fatal("capture did not redact credential")
			}
		})
	}
}
