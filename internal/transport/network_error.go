package transport

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/url"
	"syscall"
)

// safeNetworkError keeps classification and sentinel matching while hiding
// text supplied by a transport or redirect response.
type safeNetworkError struct{ error }

func (e safeNetworkError) Error() string { return NetworkFailureCause(e.error) }
func (e safeNetworkError) Unwrap() error { return e.error }

// NetworkFailureCause returns only fixed descriptions and SDK redirect hosts.
func NetworkFailureCause(err error) string {
	if err == nil {
		return ""
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

	var timeout net.Error
	if errors.As(err, &timeout) && timeout.Timeout() {
		return "timed out"
	}
	var op *net.OpError
	if errors.As(err, &op) {
		operation := "network operation"
		switch op.Op {
		case "dial", "read", "write", "accept", "listen", "close", "proxyconnect":
			operation = op.Op
		}
		if errors.Is(op, syscall.ECONNREFUSED) || (op.Err != nil && op.Err.Error() == "connect: connection refused") {
			return operation + ": connect: connection refused"
		}
		return operation + ": network request failed"
	}
	var u *url.Error
	if errors.As(err, &u) {
		return NetworkFailureCause(u.Err)
	}
	return "network request failed"
}
