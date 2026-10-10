package transport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestRedirectHostRedactsAttemptSecrets(t *testing.T) {
	for _, mode := range []string{"token", "key", "explicit"} {
		t.Run(mode, func(t *testing.T) {
			secret := "synthetic-token-1"
			req := Request{}
			if mode == "key" {
				secret = "synthetic-api-key"
				req.APIKey = secret
			}
			if mode == "explicit" {
				secret = "synthetic-explicit-secret"
				req.Redact = []string{secret}
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, "https://"+secret+".invalid/", http.StatusFound)
			}))
			defer server.Close()
			c := New(Config{HTTPClient: server.Client(), TokenSource: &longTokens{}})
			req.URL = server.URL
			err := c.DoJSON(context.Background(), req, nil)
			if err == nil {
				t.Fatal("expected refusal")
			}
			if strings.Contains(err.Error(), secret) {
				t.Fatal("refusal leaked secret")
			}
			if !strings.Contains(err.Error(), ".invalid") {
				t.Fatal("refusal lost hostname")
			}
		})
	}
}

func TestCaptureRedactsDecodedJSONSecrets(t *testing.T) {
	for _, status := range []int{200, 400} {
		for _, key := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d/%t", status, key), func(t *testing.T) {
				credential := "synthetic-token-1"
				req := Request{}
				if key {
					credential = "synthetic-api-key"
					req.APIKey = credential
				}
				body := ` {"\u0073` + credential[1:] + `":{"nested":["\u0073` + credential[1:] + `"]},"number":9007199254740993} `
				var captured []byte
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(status); _, _ = w.Write([]byte(body)) }))
				defer server.Close()
				c := New(Config{HTTPClient: server.Client(), TokenSource: &longTokens{}, Capture: func(c Capture) { captured = c.Body }})
				req.URL = server.URL
				_ = c.DoJSON(context.Background(), req, nil)
				var decoded map[string]any
				if err := json.Unmarshal(captured, &decoded); err != nil {
					t.Fatal(err)
				}
				if _, leaked := decoded[credential]; leaked {
					t.Error("decoded object key leaked")
				}
				text, err := json.Marshal(decoded)
				if err != nil {
					t.Fatal(err)
				}
				if strings.Contains(string(text), credential) {
					t.Error("decoded value leaked")
				}
				if !strings.Contains(string(captured), "9007199254740993") {
					t.Error("number changed")
				}
			})
		}
	}
	body := []byte(" {\"text\":\"ordinary\",\"number\":1e1000} \n")
	if got := (Request{Redact: []string{"synthetic-secret"}}).redactBody(body); string(got) != string(body) {
		t.Fatal("unmatched capture changed")
	}
}

type failingBody struct{ readErr, closeErr error }

func (b failingBody) Read([]byte) (int, error) {
	if b.readErr != nil {
		return 0, b.readErr
	}
	return 0, io.EOF
}
func (b failingBody) Close() error { return b.closeErr }

type bodyTripper struct{ body io.ReadCloser }

func (r bodyTripper) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: 200, Header: make(http.Header), Body: r.body}, nil
}

func TestTransportErrorChainWithholdsOriginal(t *testing.T) {
	secretErr := errors.New("http://host/path?secret=synthetic-secret")
	timeout := &url.Error{URL: "http://host/path?secret=synthetic-secret", Err: fmt.Errorf("wrapped: %w", &net.OpError{Op: "read", Err: &net.DNSError{IsTimeout: true, Name: "synthetic-secret", Err: "synthetic-secret"}})}
	for _, phase := range []string{"send", "read", "close"} {
		for _, cause := range []error{secretErr, timeout, context.Canceled, context.DeadlineExceeded, io.ErrUnexpectedEOF} {
			t.Run(phase+"/"+fmt.Sprintf("%T", cause), func(t *testing.T) {
				var rt http.RoundTripper = &stubRoundTripper{failErr: cause}
				if phase == "read" {
					rt = bodyTripper{failingBody{readErr: cause}}
				}
				if phase == "close" {
					rt = bodyTripper{failingBody{closeErr: cause}}
				}
				c := New(Config{HTTPClient: &http.Client{Transport: rt}})
				err := c.DoJSON(context.Background(), Request{URL: "http://host/path?secret=synthetic-secret", SkipAuth: true}, nil)
				var u *url.Error
				if errors.As(err, &u) {
					t.Error("original URL error exposed")
				}
				for current := err; current != nil; current = errors.Unwrap(current) {
					if strings.Contains(current.Error(), "synthetic-secret") || strings.Contains(current.Error(), "http://") {
						t.Error("unsafe cause exposed")
					}
				}
				if errors.Is(cause, context.Canceled) || errors.Is(cause, context.DeadlineExceeded) || errors.Is(cause, io.ErrUnexpectedEOF) {
					if !errors.Is(err, cause) {
						t.Error("sentinel lost")
					}
				}
				if errors.Is(cause, timeout) {
					var n net.Error
					if !errors.As(err, &n) || !n.Timeout() {
						t.Error("timeout classification lost")
					}
				}
			})
		}
	}
}

func TestWrappedTimeoutAndDNSDescriptions(t *testing.T) {
	timeout := &url.Error{Err: fmt.Errorf("wrapped: %w", &net.OpError{Op: "dial", Err: &net.DNSError{IsTimeout: true}})}
	if got := NetworkFailureCause(timeout); got != "timed out" {
		t.Errorf("cause = %q", got)
	}
	dns := &net.OpError{Op: "dial", Err: &net.DNSError{IsNotFound: true, Name: "synthetic-name", Server: "synthetic-server", Err: "synthetic-detail"}}
	if got := NetworkFailureCause(dns); got != "dial: no such host" {
		t.Errorf("cause = %q", got)
	}
}

func TestCaptureChecksDuplicateJSONKeys(t *testing.T) {
	req := Request{Redact: []string{"synthetic-secret"}}
	body := []byte(`{"key":"\u0073ynthetic-secret","key":"safe"}`)
	got := req.redactBody(body)
	if strings.Contains(string(got), `\u0073ynthetic-secret`) || strings.Contains(string(got), "synthetic-secret") {
		t.Fatal("duplicate key kept recoverable secret")
	}
}

type failingJSON struct{ err error }

func (v failingJSON) MarshalJSON() ([]byte, error) { return nil, v.err }
func (v *failingJSON) UnmarshalJSON([]byte) error  { return v.err }

func TestRequestAndDecodeErrorsWithholdCause(t *testing.T) {
	cause := &url.Error{URL: "http://host/path?secret=synthetic-secret", Err: errors.New("synthetic-secret")}
	for _, phase := range []string{"request", "encode", "decode"} {
		t.Run(phase, func(t *testing.T) {
			req := Request{URL: "http://host/path", SkipAuth: true}
			var out any
			if phase == "request" {
				req.URL = "http://host/%zz?secret=synthetic-secret"
			}
			if phase == "encode" {
				req.Body = failingJSON{cause}
			}
			if phase == "decode" {
				out = &failingJSON{cause}
			}
			rt := bodyTripper{io.NopCloser(strings.NewReader(`{}`))}
			c := New(Config{HTTPClient: &http.Client{Transport: rt}})
			err := c.DoJSON(context.Background(), req, out)
			if err == nil {
				t.Fatal("expected error")
			}
			var u *url.Error
			if errors.As(err, &u) {
				t.Error("URL error exposed")
			}
			for current := err; current != nil; current = errors.Unwrap(current) {
				if strings.Contains(current.Error(), "synthetic-secret") {
					t.Error("unsafe cause exposed")
				}
			}
		})
	}
}
