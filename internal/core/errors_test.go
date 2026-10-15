package core

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
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
			"url.Error wrapping certificate verification error",
			&url.Error{Op: "Post", URL: "http://host/path?token=secret", Err: &tls.CertificateVerificationError{Err: x509.UnknownAuthorityError{}}},
			"compute.ListServers: request failed: TLS certificate verification failed: unknown authority",
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

func TestNetworkCauseRejectsArbitraryText(t *testing.T) {
	const secret = "synthetic-secret"
	for _, cause := range []error{
		errors.New("http://host/path?projectId=" + secret),
		&net.OpError{Op: "dial", Err: errors.New("http://host/path?projectId=" + secret)},
		&net.OpError{Op: secret, Err: errors.New("failure")},
	} {
		err := &APIError{Err: cause}
		if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), "http://") {
			t.Fatal("network cause leaked")
		}
		if !errors.Is(err, cause) {
			t.Fatal("cause matching lost")
		}
	}
}

func TestCertificateFailureDescriptions(t *testing.T) {
	cert := &x509.Certificate{Subject: pkix.Name{CommonName: "synthetic-subject-secret"}}
	for _, tc := range []struct {
		cause error
		want  string
	}{
		{x509.UnknownAuthorityError{Cert: cert}, "TLS certificate verification failed: unknown authority"},
		{&x509.UnknownAuthorityError{Cert: cert}, "TLS certificate verification failed: unknown authority"},
		{x509.HostnameError{Certificate: cert, Host: "synthetic-host-secret"}, "TLS certificate verification failed: hostname mismatch"},
		{&x509.HostnameError{Certificate: cert, Host: "synthetic-host-secret"}, "TLS certificate verification failed: hostname mismatch"},
		{x509.CertificateInvalidError{Cert: cert, Reason: x509.Expired, Detail: "synthetic-detail-secret"}, "TLS certificate verification failed: invalid certificate"},
		{&x509.CertificateInvalidError{Cert: cert, Reason: x509.Expired, Detail: "synthetic-detail-secret"}, "TLS certificate verification failed: invalid certificate"},
		{&tls.CertificateVerificationError{Err: errors.New("http://host/path?token=synthetic-query-secret")}, "TLS certificate verification failed"},
	} {
		for _, cause := range []error{tc.cause, &tls.CertificateVerificationError{Err: tc.cause}, &net.OpError{Op: "dial", Err: tc.cause}, &url.Error{URL: "http://host/path?token=synthetic-query-secret", Err: tc.cause}} {
			err := &APIError{Err: cause}
			if got := err.Error(); got != "request failed: "+tc.want {
				t.Errorf("error = %q, want class %q", got, tc.want)
			}
			if !errors.Is(err, cause) {
				t.Fatal("cause matching lost")
			}
		}
	}
}

func TestServiceAccountLoginErrorMessageAndUnwrap(t *testing.T) {
	for _, cause := range []error{ErrAuth, context.Canceled, context.DeadlineExceeded} {
		login := &ServiceAccountLoginError{Status: 401, Reason: "token endpoint rejected request", Err: cause}
		if !errors.Is(login, cause) || login.Error() != "token endpoint rejected request (status 401)" {
			t.Fatalf("error identity/text: %v", login)
		}
		var found *ServiceAccountLoginError
		if !errors.As(login, &found) || found != login {
			t.Fatal("error type lost")
		}
	}
	if got := (&ServiceAccountLoginError{Reason: "token request failed", Err: ErrAuth}).Error(); got != "token request failed" {
		t.Fatal(got)
	}
	if ErrTokenCache.Error() != "vngcloud: token cache unavailable" || errors.Is(ErrTokenCache, ErrAuth) || errors.Is(ErrTokenCache, ErrInvalidConfig) || errors.Unwrap(ErrTokenCache) != nil {
		t.Fatal("cache error exposes another cause")
	}
}

func TestWrapTransportObservedCause(t *testing.T) {
	for _, status := range []int{401, 403, 404, 429, 201} {
		for _, marked := range []bool{false, true} {
			cause := &url.Error{Op: "read", URL: "http://localhost/", Err: context.Canceled}
			terr := &transport.APIError{Operation: "iam.Read", StatusCode: status, Code: "Fixed", Message: "safe", Retryable: true, Err: cause, KeptObservedStatus: marked}
			err := wrapTransportErr(terr)
			var api *APIError
			if !errors.As(err, &api) {
				t.Fatalf("status %d: APIError lost", status)
			}
			sentinel := mapStatusError(status)
			wantCause := marked || sentinel == nil
			var network *url.Error
			if errors.As(err, &network) != wantCause || errors.Is(err, context.Canceled) != wantCause {
				t.Errorf("status %d, marked %t: cause mapping changed", status, marked)
			}
			if sentinel != nil && !errors.Is(err, sentinel) {
				t.Errorf("status %d: sentinel lost", status)
			}
			if api.StatusCode != status || api.Code != terr.Code || api.Message != terr.Message || api.Operation != terr.Operation || api.Retryable != terr.Retryable {
				t.Errorf("status %d: fields changed", status)
			}
			terr.Err = nil
			var without *APIError
			if !errors.As(wrapTransportErr(terr), &without) || !errors.Is(without.Err, sentinel) || errors.Unwrap(without.Err) != nil {
				t.Errorf("status %d: nil cause mapping changed", status)
			}
		}
	}
}

func TestWrapTransportOverflowChain(t *testing.T) {
	for _, status := range []int{201, 401, 403, 404, 429} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(status)
			_, _ = w.Write([]byte("synthetic-body-canary"))
		}))
		for _, keep := range []bool{false, true} {
			c := transport.New(transport.Config{HTTPClient: server.Client()})
			got, terr := c.DoJSONStatus(context.Background(), transport.Request{URL: server.URL, OK: []int{201}, MaxBody: 1, KeepObservedStatus: keep}, nil)
			if got != status {
				t.Errorf("status = %d, want %d", got, status)
			}
			err := wrapTransportErr(terr)
			if status == 201 {
				if !errors.Is(err, transport.ErrBodyTooLarge) {
					t.Errorf("error = %v, want overflow", err)
				}
				continue
			}
			var api *APIError
			var transportAPI *transport.APIError
			if !errors.As(terr, &transportAPI) || transportAPI.KeptObservedStatus {
				t.Fatal("overflow marked as a body failure")
			}
			if !errors.As(err, &api) || api.StatusCode != status || !errors.Is(api.Err, mapStatusError(status)) || errors.Unwrap(api.Err) != nil {
				t.Fatalf("overflow chain changed: %v", err)
			}
			if strings.Contains(err.Error(), "canary") {
				t.Error("overflow leaked body")
			}
		}
		server.Close()
	}
}
