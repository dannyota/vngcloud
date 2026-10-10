package transport

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/url"
	"syscall"
)

// safeNetworkError exposes only fixed text, timeout state, and safe sentinels.
// The original transport cause can contain URLs or credentials at any depth.
type safeNetworkError struct {
	message string
	timeout bool
	cause   error
}

func (e *safeNetworkError) Error() string   { return e.message }
func (e *safeNetworkError) Unwrap() error   { return e.cause }
func (e *safeNetworkError) Timeout() bool   { return e.timeout }
func (e *safeNetworkError) Temporary() bool { return false }

func sanitizeNetworkError(err error, values []string) error {
	var sentinels []error
	for _, sentinel := range []error{context.Canceled, context.DeadlineExceeded, io.EOF, io.ErrUnexpectedEOF, io.ErrClosedPipe, net.ErrClosed, syscall.ECONNREFUSED, errRedirectLimit, errRedirectScheme} {
		if errors.Is(err, sentinel) {
			sentinels = append(sentinels, sentinel)
		}
	}
	var syntax *json.SyntaxError
	if errors.As(err, &syntax) {
		var value any
		var fixed *json.SyntaxError
		if errors.As(json.Unmarshal([]byte("?"), &value), &fixed) {
			fixed.Offset = syntax.Offset
			sentinels = append(sentinels, fixed)
		}
	}
	return &safeNetworkError{
		message: redact(NetworkFailureCause(err), values),
		timeout: errors.Is(err, context.DeadlineExceeded) || hasTimeout(err),
		cause:   errors.Join(sentinels...),
	}
}

//nolint:errorlint // Inspect each cause directly; the first net.Error can hide a deeper timeout.
func hasTimeout(err error) bool {
	if err == nil {
		return false
	}
	if n, ok := err.(net.Error); ok && n.Timeout() {
		return true
	}
	switch e := err.(type) {
	case interface{ Unwrap() []error }:
		for _, cause := range e.Unwrap() {
			if hasTimeout(cause) {
				return true
			}
		}
	case interface{ Unwrap() error }:
		return hasTimeout(e.Unwrap())
	}
	return false
}

// NetworkFailureCause returns only fixed descriptions and SDK redirect hosts.
func NetworkFailureCause(err error) string {
	if err == nil {
		return ""
	}
	var safe *safeNetworkError
	if errors.As(err, &safe) {
		return safe.message
	}
	switch {
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "timed out"
	case errors.Is(err, errRedirectScheme):
		return errRedirectScheme.Error()
	case errors.Is(err, errRedirectLimit):
		return errRedirectLimit.Error()
	}
	if hasTimeout(err) {
		return "timed out"
	}
	var redirect *redirectError
	if errors.As(err, &redirect) {
		return redirect.Error()
	}
	var unknownAuthority x509.UnknownAuthorityError
	var unknownAuthorityPtr *x509.UnknownAuthorityError
	if errors.As(err, &unknownAuthority) || errors.As(err, &unknownAuthorityPtr) {
		return "TLS certificate verification failed: unknown authority"
	}
	var hostname x509.HostnameError
	var hostnamePtr *x509.HostnameError
	if errors.As(err, &hostname) || errors.As(err, &hostnamePtr) {
		return "TLS certificate verification failed: hostname mismatch"
	}
	var invalidCertificate x509.CertificateInvalidError
	var invalidCertificatePtr *x509.CertificateInvalidError
	if errors.As(err, &invalidCertificate) || errors.As(err, &invalidCertificatePtr) {
		return "TLS certificate verification failed: invalid certificate"
	}
	var verification *tls.CertificateVerificationError
	if errors.As(err, &verification) {
		return "TLS certificate verification failed"
	}

	var op *net.OpError
	if errors.As(err, &op) {
		operation := "network operation"
		switch op.Op {
		case "dial", "read", "write", "accept", "listen", "close", "proxyconnect":
			operation = op.Op
		}
		var dns *net.DNSError
		if errors.As(op, &dns) && dns.IsNotFound {
			return operation + ": no such host"
		}
		if errors.Is(op, syscall.ECONNREFUSED) || (op.Err != nil && op.Err.Error() == "connect: connection refused") {
			return operation + ": connect: connection refused"
		}
		return operation + ": network request failed"
	}
	var dns *net.DNSError
	if errors.As(err, &dns) && dns.IsNotFound {
		return "no such host"
	}
	var u *url.Error
	if errors.As(err, &u) {
		return NetworkFailureCause(u.Err)
	}
	return "network request failed"
}
