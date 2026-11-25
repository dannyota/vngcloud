//go:build livewrite

package livetest_test

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestNATHarnessCleanupDecision(t *testing.T) {
	for _, tc := range []struct{ attempted, nat, billing, routes, want bool }{
		{false, false, false, false, true},
		{true, true, true, true, true},
		{true, false, true, true, false},
		{true, true, false, true, false},
		{true, true, true, false, false},
	} {
		if natVPCDeleteAllowed(tc.attempted, tc.nat, tc.billing, tc.routes) != tc.want {
			t.Errorf("cleanup decision: %+v", tc)
		}
	}
}

func TestNATHarnessDeleteObserver(t *testing.T) {
	for _, tc := range []struct {
		name     string
		status   int
		body     string
		requests int
		fail     bool
		want     bool
	}{
		{"valid", 200, `{"code":0,"success":true}`, 1, false, true},
		{"missing", 200, `{"code":0,"success":true}`, 0, false, false},
		{"duplicate", 200, `{"code":0,"success":true}`, 2, false, false},
		{"http", 201, `{"code":0,"success":true}`, 1, false, false},
		{"code", 200, `{"code":1,"success":true}`, 1, false, false},
		{"success", 200, `{"code":0,"success":false}`, 1, false, false},
		{"missing code", 200, `{"success":true}`, 1, false, false},
		{"missing success", 200, `{"code":0}`, 1, false, false},
		{"malformed", 200, `{"code":0,"success":true`, 1, false, false},
		{"duplicate field", 200, `{"code":0,"code":0,"success":true}`, 1, false, false},
		{"transport failure", 0, "", 1, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id := "nat-1"
			tr := &natDeleteTransport{natID: &id, base: natTestTransport(func(*http.Request) (*http.Response, error) {
				if tc.fail {
					return nil, errors.New("failed")
				}
				return &http.Response{StatusCode: tc.status, Body: io.NopCloser(strings.NewReader(tc.body)), Header: make(http.Header)}, nil
			})}
			for range tc.requests {
				req, _ := http.NewRequest(http.MethodDelete, "https://example.invalid/nats/nat-1", nil)
				res, _ := tr.RoundTrip(req)
				if res != nil {
					data, _ := io.ReadAll(res.Body)
					_ = res.Body.Close()
					if string(data) != tc.body {
						t.Fatal("response body changed")
					}
				}
			}
			if tr.valid() != tc.want {
				t.Fatal("unexpected delete observation")
			}
		})
	}
}
