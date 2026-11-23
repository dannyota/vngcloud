package transport

import (
	"context"
	"errors"
	"io"
	"net/http"
	"testing"
)

type overflowErrorReader struct{}

func (overflowErrorReader) Read(p []byte) (int, error) { return len(p), errors.New("synthetic") }

type overflowContextReader struct{}

func (overflowContextReader) Read(p []byte) (int, error) { return len(p), context.Canceled }

func TestReadBodyOverflowWithReadError(t *testing.T) {
	body, err := readBody(overflowErrorReader{}, 128)
	if body != nil || !errors.Is(err, ErrBodyTooLarge) {
		t.Fatalf("body %v, error %v", body, err)
	}
}

func TestReadBodyOverflowWithContextError(t *testing.T) {
	body, err := readBody(overflowContextReader{}, 128)
	if body != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("body %v, error %v", body, err)
	}
}

type cancelOverflowBody struct{ cancel context.CancelFunc }

func (r cancelOverflowBody) Read(p []byte) (int, error) {
	r.cancel()
	return len(p), errors.New("synthetic")
}
func (cancelOverflowBody) Close() error { return nil }

type cancelOverflowTransport struct{ body io.ReadCloser }

func (rt cancelOverflowTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: rt.body, Request: r}, nil
}

func TestOverflowContextPrecedence(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := New(Config{HTTPClient: &http.Client{Transport: cancelOverflowTransport{body: cancelOverflowBody{cancel: cancel}}}})
	_, err := client.DoJSONStatus(ctx, Request{URL: "http://example.test", MaxBody: 1, SkipAuth: true}, nil)
	if !errors.Is(err, context.Canceled) || errors.Is(err, ErrBodyTooLarge) {
		t.Fatalf("error %v", err)
	}
}
