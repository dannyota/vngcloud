//go:build livewrite

package livetest_test

import (
	"errors"
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/testutil"
	"danny.vn/vngcloud/network"
)

func TestIsLiveVirtualIPName(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"vngcloud-live-1a2b3c4d", true},
		{"vngcloud-live-1a2b3c4d-b", true},
		{"vngcloud-live-1a2b3c4d-c", true},
		{"vngcloud-live-1a2b3c4d-b-renamed", true},
		{"vngcloud-live-1a2b3c4d-extra", false},
		{"vngcloud-live-1A2B3C4D", false},
		{"vngcloud-live-1a2b3c4d-c-renamed", false},
		{"other", false},
	}
	for _, tt := range cases {
		if got := isLiveVirtualIPName(tt.name); got != tt.want {
			t.Errorf("isLiveVirtualIPName(%q) = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestRegisterVirtualIPCleanupIfCreated(t *testing.T) {
	var deletes atomic.Int32
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v2/project-1/virtualIpAddress/vip-1":
			_, _ = w.Write([]byte(`{"data":{"uuid":"vip-1","type":"private"}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v2/project-1/virtualIpAddress/vip-1/addressPairs":
			_, _ = w.Write([]byte(`{"data":[]}`))
		case r.Method == http.MethodDelete && r.URL.Path == "/v2/project-1/virtualIpAddress/vip-1":
			deletes.Add(1)
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	})
	client := network.New(testutil.NewConfig(t, handler))

	t.Run("created output", func(t *testing.T) {
		registerVirtualIPCleanupIfCreated(t, client, &network.CreateVirtualIPAddressOutput{
			VirtualIPAddress: network.VirtualIPAddress{UUID: "vip-1"},
		}, "first")
	})
	if got := deletes.Load(); got != 1 {
		t.Fatalf("cleanup deletes = %d, want 1", got)
	}
}

func TestSafeVirtualIPErrRedactsSentinelDetails(t *testing.T) {
	err := fmt.Errorf("%w: virtual IP vip-1 at 10.0.0.10 in 10.0.0.0/24", network.ErrNotSettled)
	if got := safeVirtualIPErr(err); got != "not settled" {
		t.Fatalf("safeVirtualIPErr() = %q, want not settled", got)
	}
	if got := safeVirtualIPErr(&vngcloud.APIError{StatusCode: 402, Code: "PaymentRequired", Message: "credit for 10.0.0.10"}); got != "status=402 code=PaymentRequired" {
		t.Fatalf("safeVirtualIPErr(APIError) = %q, want redacted API status", got)
	}
	if got := safeVirtualIPErr(errors.New("tcp 10.0.0.10")); got != "non-API error (*errors.errorString)" {
		t.Fatalf("safeVirtualIPErr(other) = %q, want generic error type", got)
	}
}

func TestIsVirtualIPAddressDuplicateRefusal(t *testing.T) {
	cases := []struct {
		err  error
		want bool
	}{
		{&vngcloud.APIError{StatusCode: http.StatusBadRequest}, true},
		{&vngcloud.APIError{StatusCode: http.StatusConflict}, true},
		{&vngcloud.APIError{StatusCode: http.StatusUnauthorized}, false},
		{&vngcloud.APIError{StatusCode: http.StatusInternalServerError}, false},
		{fmt.Errorf("%w: virtual IP vip-1", network.ErrNotSettled), false},
	}
	for _, tt := range cases {
		if got := isVirtualIPAddressDuplicateRefusal(tt.err); got != tt.want {
			t.Errorf("isVirtualIPAddressDuplicateRefusal(%v) = %v, want %v", tt.err, got, tt.want)
		}
	}
}
