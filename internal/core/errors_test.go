package core

import (
	"errors"
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
