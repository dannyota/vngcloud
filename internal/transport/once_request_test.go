package transport

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

type onceRequestTripper func(*http.Request) (*http.Response, error)

func (f onceRequestTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestOnceRequestReplayControls(t *testing.T) {
	for _, body := range []bool{true, false} {
		for _, once := range []bool{true, false} {
			t.Run(fmt.Sprintf("body=%t/once=%t", body, once), func(t *testing.T) {
				wantBody := ""
				if body {
					wantBody = `{"pay":true}`
				}
				rt := onceRequestTripper(func(req *http.Request) (*http.Response, error) {
					if once {
						if req.GetBody != nil || req.Body == nil || req.Body == http.NoBody {
							t.Error("Once request must have a non-sentinel body and no GetBody, even when empty")
						}
					} else if req.GetBody == nil {
						t.Error("ordinary request lost GetBody")
					}
					for _, key := range []string{"Idempotency-Key", "X-Idempotency-Key"} {
						want := "synthetic-key"
						if once {
							want = ""
						}
						if got := req.Header.Get(key); got != want {
							t.Errorf("%s = %q, want %q", key, got, want)
						}
					}
					if req.Header.Get("X-Custom") != "preserved" {
						t.Error("unrelated header changed")
					}
					if req.ContentLength != int64(len(wantBody)) {
						t.Errorf("ContentLength = %d, want %d", req.ContentLength, len(wantBody))
					}
					data, err := io.ReadAll(req.Body)
					if err != nil || string(data) != wantBody {
						t.Errorf("body = %q, err=%v, want %q", data, err, wantBody)
					}
					_ = req.Body.Close()
					return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(""))}, nil
				})
				req := Request{URL: "http://example.invalid", Once: once, Headers: map[string]string{
					"iDeMpOtEnCy-kEy": "synthetic-key", "X-IDEMPOTENCY-KEY": "synthetic-key", "X-Custom": "preserved",
				}}
				if body {
					req.Body = map[string]bool{"pay": true}
				}
				if _, _, _, err := New(Config{HTTPClient: &http.Client{Transport: rt}}).DoRaw(context.Background(), req); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}
