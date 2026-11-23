package transport

import (
	"context"
	"errors"
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
