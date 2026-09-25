package core

import (
	"context"
	"errors"
	"net"
	"net/url"
	"strings"
	"testing"

	"danny.vn/vngcloud/internal/transport"
)

func TestResolvedCodeStatusFallback(t *testing.T) {
	tests := []struct {
		name   string
		status int
		code   string
		want   string
	}{
		{"api code wins", 400, "CustomCode", "CustomCode"},
		{"envelope code equal to status counts as none", 400, "400", "BadRequest"},
		{"bad request fallback", 400, "", "BadRequest"},
		{"unauthorized fallback", 401, "", "Unauthorized"},
		{"forbidden fallback", 403, "", "Forbidden"},
		{"not found fallback", 404, "", "NotFound"},
		{"conflict fallback", 409, "", "Conflict"},
		{"throttled fallback", 429, "", "Throttled"},
		{"server error fallback", 500, "", "ServerError"},
		{"server error fallback 503", 503, "", "ServerError"},
		{"other 4xx fallback", 418, "", "ClientError"},
		{"no status keeps empty code", 0, "", ""},
		{"2xx keeps empty code", 200, "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ResolvedCode(tt.status, tt.code); got != tt.want {
				t.Fatalf("ResolvedCode(%d, %q) = %q, want %q", tt.status, tt.code, got, tt.want)
			}
		})
	}
}

func TestWrapTransportErrAPICodeWins(t *testing.T) {
	terr := &transport.APIError{Operation: "x.Y", StatusCode: 400, Code: "CustomCode", Message: "bad"}
	err := wrapTransportErr(terr)
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("wrapTransportErr did not return *APIError: %v", err)
	}
	if apiErr.Code != "CustomCode" {
		t.Fatalf("Code = %q, want CustomCode", apiErr.Code)
	}
}

func TestWrapTransportErrEnvelopeCodeEqualToStatusFallsBack(t *testing.T) {
	terr := &transport.APIError{Operation: "x.Y", StatusCode: 400, Code: "400", Message: "bad"}
	err := wrapTransportErr(terr)
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("wrapTransportErr did not return *APIError: %v", err)
	}
	if apiErr.Code != "BadRequest" {
		t.Fatalf("Code = %q, want BadRequest", apiErr.Code)
	}
}

func TestWrapTransportErrNoCodeFallsBackToStatus(t *testing.T) {
	terr := &transport.APIError{Operation: "x.Y", StatusCode: 404, Message: "not found"}
	err := wrapTransportErr(terr)
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("wrapTransportErr did not return *APIError: %v", err)
	}
	if apiErr.Code != "NotFound" {
		t.Fatalf("Code = %q, want NotFound", apiErr.Code)
	}
}

func TestAPIErrorNoCauseWhenNoErr(t *testing.T) {
	err := &APIError{Operation: "compute.ListServers", StatusCode: 500, Code: "ServerError"}
	want := "compute.ListServers: request failed (status 500)"
	if got := err.Error(); got != want {
		t.Fatalf("Error() = %q, want %q", got, want)
	}
}

// TestAPIErrorNamesNetworkFailureCause covers each cause shape the design
// names: a bare *net.OpError, a *url.Error wrapping one, a *url.Error
// wrapping something else, and a canceled or expired context, bare or
// wrapped in a *url.Error.
func TestAPIErrorNamesNetworkFailureCause(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{
			"net.OpError",
			&net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connect: connection refused")},
			"compute.ListServers: request failed: dial: connect: connection refused",
		},
		{
			"url.Error wrapping net.OpError",
			&url.Error{Op: "Post", URL: "http://host/path?token=secret", Err: &net.OpError{Op: "dial", Err: errors.New("connect: connection refused")}},
			"compute.ListServers: request failed: dial: connect: connection refused",
		},
		{
			"url.Error wrapping another error",
			&url.Error{Op: "Post", URL: "http://host/path?token=secret", Err: errors.New("tls: unknown authority")},
			"compute.ListServers: request failed: tls: unknown authority",
		},
		{
			"bare context.Canceled",
			context.Canceled,
			"compute.ListServers: request failed: canceled",
		},
		{
			"bare context.DeadlineExceeded",
			context.DeadlineExceeded,
			"compute.ListServers: request failed: timed out",
		},
		{
			"url.Error wrapping context.Canceled",
			&url.Error{Op: "Post", URL: "http://host/path", Err: context.Canceled},
			"compute.ListServers: request failed: canceled",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := &APIError{Operation: "compute.ListServers", Err: tt.err}
			if got := err.Error(); got != tt.want {
				t.Fatalf("Error() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestAPIErrorNetworkFailureNeverLeaksURLOrQuery is the design's own
// example: a refused connection to a URL carrying a token in its query
// string must name the cause without the token or the host.
func TestAPIErrorNetworkFailureNeverLeaksURLOrQuery(t *testing.T) {
	err := &APIError{
		Operation: "compute.ListServers",
		Err: &url.Error{
			Op:  "Post",
			URL: "http://internal-host.example/v1/servers?token=secret-XYZ",
			Err: &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connect: connection refused")},
		},
	}
	msg := err.Error()
	if strings.Contains(msg, "secret-XYZ") {
		t.Fatalf("Error() leaked the query string: %q", msg)
	}
	if strings.Contains(msg, "internal-host.example") {
		t.Fatalf("Error() leaked the host: %q", msg)
	}
	if !strings.Contains(msg, "connection refused") {
		t.Fatalf("Error() = %q, want it to name the cause", msg)
	}
}

func TestLoginErrorMessageAndUnwrap(t *testing.T) {
	le := &LoginError{Status: 500, Reason: "could not load the sign-in page", Err: ErrAuth}
	if !errors.Is(le, ErrAuth) {
		t.Fatalf("errors.Is(le, ErrAuth) = false")
	}
	if got := le.Error(); got != "could not load the sign-in page (status 500)" {
		t.Fatalf("Error() = %q", got)
	}

	captcha := &LoginError{Status: 200, Reason: "the sign-in form was rejected", CaptchaSuspected: true, Err: ErrAuth}
	if got := captcha.Error(); got != "the sign-in form was rejected (status 200); a captcha may be required" {
		t.Fatalf("Error() = %q", got)
	}
}
