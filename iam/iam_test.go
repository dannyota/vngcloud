package iam

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/testutil"
)

func newTestClient(t *testing.T, handler http.Handler) *Client {
	t.Helper()

	return New(testutil.NewConfig(t, handler))
}

// TestIAMZeroConfig checks that a Client built from a zero Config fails
// every call with ErrInvalidConfig instead of panicking.
func TestIAMZeroConfig(t *testing.T) {
	c := New(vngcloud.Config{})
	if _, err := c.ListPolicies(context.Background(), nil); !errors.Is(err, vngcloud.ErrInvalidConfig) {
		t.Fatalf("ListPolicies() err = %v, want ErrInvalidConfig", err)
	}
}
