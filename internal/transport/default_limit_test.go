package transport

import (
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDefaultBodyLimit(t *testing.T) {
	const limit = 8 << 20
	for _, compressed := range []bool{false, true} {
		for _, status := range []int{200, 403, 503} {
			for _, n := range []int{limit, limit + 1} {
				t.Run(fmt.Sprintf("gzip=%t/status=%d/bytes=%d", compressed, status, n), func(t *testing.T) {
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
						var writer io.Writer = w
						if compressed {
							w.Header().Set("Content-Encoding", "gzip")
						}
						w.WriteHeader(status)
						if compressed {
							gz := gzip.NewWriter(w)
							defer func() { _ = gz.Close() }()
							writer = gz
						}
						_, _ = io.Copy(writer, io.LimitReader(strings.NewReader(strings.Repeat("x", n)), int64(n)))
					}))
					defer server.Close()
					captures := 0
					client := New(Config{HTTPClient: server.Client(), Capture: func(Capture) { captures++ }})
					got, _, body, err := client.DoRaw(context.Background(), Request{URL: server.URL, SkipAuth: true})
					if got != status {
						t.Fatalf("status=%d", got)
					}
					if n == limit && (err != nil || len(body) != n || captures != 1) {
						t.Fatalf("exact boundary: err=%v bytes=%d captures=%d", err, len(body), captures)
					}
					if n > limit && (!errors.Is(err, ErrBodyTooLarge) || body != nil || captures != 0) {
						t.Fatal("overflow returned bytes or reached capture")
					}
				})
			}
		}
	}
}

func TestDefaultBodyLimitNegativeAndTighter(t *testing.T) {
	for _, capBytes := range []int64{0, -1, 128} {
		limit := int64(8 << 20)
		if capBytes > 0 {
			limit = capBytes
		}
		body := &countedDefaultBody{reader: strings.NewReader(strings.Repeat("x", int(limit)+100))}
		client := New(Config{HTTPClient: &http.Client{Transport: discoveryRoundTripper(func(r *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: body, Request: r}, nil
		})}})
		_, _, raw, err := client.DoRaw(context.Background(), Request{URL: "http://example.test", SkipAuth: true, MaxBody: capBytes})
		if !errors.Is(err, ErrBodyTooLarge) || raw != nil || body.read != limit+1 {
			t.Fatalf("limit=%d read=%d err=%v", limit, body.read, err)
		}
	}
}

type countedDefaultBody struct {
	reader *strings.Reader
	read   int64
}

func (b *countedDefaultBody) Read(p []byte) (int, error) {
	n, err := b.reader.Read(p)
	b.read += int64(n)
	return n, err
}
func (*countedDefaultBody) Close() error { return nil }

func TestDiscoveryPreservesTighterBodyLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(strings.Repeat(" ", 129))) }))
	defer server.Close()
	client := New(Config{HTTPClient: server.Client()})
	_, err := client.DoDiscoveryJSONStatus(context.Background(), Request{URL: server.URL, MaxBody: 128}, nil, nil)
	if !errors.Is(err, ErrBodyTooLarge) {
		t.Fatal("discovery replaced tighter body limit")
	}
}

func TestPrivateTransportTokenDoesNotFormat(t *testing.T) {
	c := New(Config{TokenSource: &discoveryTokenSource{}})
	if err := c.EnsureToken(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, verb := range []string{"%v", "%+v", "%#v"} {
		if strings.Contains(fmt.Sprintf(verb, c), "canary-secret") {
			t.Error("private transport token disclosed")
		}
	}
}
