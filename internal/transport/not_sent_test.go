package transport

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

type notSentTokens struct{ failure error }

func (s notSentTokens) Token(context.Context) (Token, error) { return Token{}, s.failure }
func (notSentTokens) Invalidate(string)                      {}

func TestRequestNotSentBeforeSend(t *testing.T) {
	for _, cause := range []string{"token", "provider APIError", "empty token", "body", "method", "endpoint", "API key", "context", "unauthenticated context", "dial"} {
		t.Run(cause, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.WriteHeader(http.StatusNoContent)
			}))
			defer server.Close()
			notSent := false
			req := Request{URL: server.URL, Method: http.MethodPost, Once: true, NotSent: &notSent}
			cfg := Config{HTTPClient: server.Client()}
			ctx := context.Background()
			switch cause {
			case "token":
				cfg.TokenSource = notSentTokens{failure: errors.New("acquisition failed")}
			case "provider APIError":
				cfg.TokenSource = notSentTokens{failure: &APIError{StatusCode: 503}}
			case "empty token":
				cfg.TokenSource = notSentTokens{}
			case "body":
				req.Body = make(chan int)
			case "method":
				req.Method = "invalid method"
			case "endpoint":
				req.URL = "://invalid"
			case "API key":
				req.APIKey = "synthetic-key"
				req.SkipAuth = true
			case "context", "unauthenticated context":
				req.SkipAuth = cause == "unauthenticated context"
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "dial":
				tr := server.Client().Transport.(*http.Transport).Clone()
				tr.DialContext = func(context.Context, string, string) (net.Conn, error) {
					return nil, &net.OpError{Op: "dial", Err: errors.New("refused")}
				}
				defer tr.CloseIdleConnections()
				cfg.HTTPClient = &http.Client{Transport: tr}
			}
			_, _, _, err := New(cfg).DoRaw(ctx, req)
			if err == nil || !notSent || calls.Load() != 0 {
				t.Fatalf("err=%v notSent=%v calls=%d", err, notSent, calls.Load())
			}
		})
	}
}

func TestRequestNotSentResponses(t *testing.T) {
	for _, status := range []int{200, 204, 301, 302, 303, 307, 308, 400, 401, 403, 404, 409, 429, 500, 502, 503, 504} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.Header().Set("Location", "/redirect")
				w.WriteHeader(status)
			}))
			defer server.Close()
			notSent := true
			c := New(Config{HTTPClient: server.Client(), RetryCount: 3})
			_, _ = c.DoJSONStatus(context.Background(), Request{URL: server.URL, Method: http.MethodPost, Once: true, NotSent: &notSent}, nil)
			if notSent || calls.Load() != 1 {
				t.Fatalf("notSent=%v calls=%d", notSent, calls.Load())
			}
		})
	}
}

// The real connection accepts the full request before returning a write error.
type notSentWriteConn struct {
	net.Conn
	cancel   context.CancelFunc
	written  *atomic.Int64
	received <-chan struct{}
}

func (c notSentWriteConn) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p)
	c.written.Add(int64(n))
	if err != nil {
		return n, err
	}
	<-c.received
	if c.cancel != nil {
		c.cancel()
	}
	return n, &net.OpError{Op: "write", Err: &net.OpError{Op: "dial", Err: errors.New("write failed")}}
}

func TestRequestNotSentWriteStarted(t *testing.T) {
	for _, cancelWrite := range []bool{false, true} {
		t.Run(strconv.FormatBool(cancelWrite), func(t *testing.T) {
			var calls atomic.Int32
			var written atomic.Int64
			received := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				calls.Add(1)
				close(received)
				// A response must not win the race against the injected failure.
				<-r.Context().Done()
			}))
			defer server.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			tr := server.Client().Transport.(*http.Transport).Clone()
			tr.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
				conn, err := (&net.Dialer{}).DialContext(ctx, network, address)
				if err != nil {
					return nil, err
				}
				wrapped := notSentWriteConn{Conn: conn, written: &written, received: received}
				if cancelWrite {
					wrapped.cancel = cancel
				}
				return wrapped, nil
			}
			defer tr.CloseIdleConnections()
			notSent := true
			c := New(Config{HTTPClient: &http.Client{Transport: tr, Timeout: time.Second}})
			_, _, _, err := c.DoRaw(ctx, Request{URL: server.URL, Method: http.MethodPost, Body: map[string]string{"order": "synthetic"}, Once: true, NotSent: &notSent})
			if err == nil || notSent || written.Load() == 0 || calls.Load() != 1 {
				t.Fatalf("err=%v notSent=%v written=%d calls=%d", err, notSent, written.Load(), calls.Load())
			}
		})
	}
}

func TestRequestNotSentUnknownTransport(t *testing.T) {
	notSent := true
	rt := &stubRoundTripper{failErr: &net.OpError{Op: "dial", Err: errors.New("unknown progress")}}
	c := New(Config{HTTPClient: &http.Client{Transport: rt}})
	_, _, _, err := c.DoRaw(context.Background(), Request{URL: "http://example.invalid", Once: true, NotSent: &notSent})
	if err == nil || notSent {
		t.Fatalf("err=%v notSent=%v", err, notSent)
	}
}
