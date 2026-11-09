package core

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"danny.vn/vngcloud/internal/transport"
)

var (
	// ErrTokenCache reports service-account cache availability failures without paths or causes.
	ErrTokenCache = errors.New("vngcloud: token cache unavailable")
	// ErrAuth marks an authentication failure.
	ErrAuth = errors.New("vngcloud: authentication failed")
	// ErrNotFound marks a missing resource.
	ErrNotFound = errors.New("vngcloud: resource not found")
	// ErrPermission marks a permission failure.
	ErrPermission = errors.New("vngcloud: permission denied")
	// ErrRateLimited marks a rate limit response.
	ErrRateLimited = errors.New("vngcloud: rate limited")
	// ErrProjectNotFound marks an empty regional project discovery.
	ErrProjectNotFound = errors.New("vngcloud: no project found for region")
	// ErrProjectAmbiguous marks project discovery with multiple matches.
	ErrProjectAmbiguous = errors.New("vngcloud: multiple projects found for region")
	// ErrMissingProjectID marks a missing required project ID.
	ErrMissingProjectID = errors.New("vngcloud: project id is required")
	// ErrInvalidConfig marks an invalid configuration.
	ErrInvalidConfig = errors.New("vngcloud: invalid config")
	// ErrInvalidInput marks an invalid input.
	ErrInvalidInput = errors.New("vngcloud: invalid input")
	// ErrHistoryIncomplete marks a history walk whose completeness is unconfirmed.
	ErrHistoryIncomplete = errors.New("vngcloud: billing history is incomplete")

	// ErrPriceAboveMax means a paid write's quote priced the order above
	// Input.MaxPrice. Nothing was ordered.
	ErrPriceAboveMax = errors.New("vngcloud: quoted price is above MaxPrice")

	// ErrUnpriced means a paid write's quote priced the order at 0, so the
	// gateway could not price the input. Nothing was ordered.
	ErrUnpriced = errors.New("vngcloud: quote priced the order at 0; refusing to order an unpriced paid resource")

	// ErrNoCredentials is LoadConfig's error when no source (options,
	// environment variables, or the resolved profile) sets any credential
	// value. It wraps ErrInvalidConfig, so errors.Is(err, ErrInvalidConfig)
	// also matches.
	ErrNoCredentials = fmt.Errorf("%w: vngcloud: no credentials found", ErrInvalidConfig)

	// ErrCredentialsFile is LoadConfig's error for the credentials file
	// itself: missing at an explicit path, unreadable, refused for unsafe
	// permissions, or malformed. It wraps ErrInvalidConfig, so
	// errors.Is(err, ErrInvalidConfig) also matches.
	ErrCredentialsFile = fmt.Errorf("%w: vngcloud: credentials file error", ErrInvalidConfig)
)

// APIError describes an error response returned by GreenNode.
type APIError struct {
	Operation  string
	StatusCode int
	Code       string
	Message    string
	Retryable  bool
	Err        error
}

func (e *APIError) Error() string {
	msg := e.Message
	if msg == "" {
		msg = "request failed"
	}
	if e.StatusCode == 0 {
		if cause := networkFailureCause(e.Err); cause != "" {
			msg += ": " + cause
		}
	}
	if e.Operation != "" {
		msg = e.Operation + ": " + msg
	}
	if e.StatusCode > 0 {
		msg = fmt.Sprintf("%s (status %d)", msg, e.StatusCode)
	}
	return msg
}

func networkFailureCause(err error) string {
	return transport.NetworkFailureCause(err)
}

func (e *APIError) Unwrap() error {
	return e.Err
}

// IsNotFound reports a missing resource or HTTP 404.
func IsNotFound(err error) bool {
	return errors.Is(err, ErrNotFound) || statusIs(err, 404)
}

// IsPermissionDenied reports denied permission or HTTP 403.
func IsPermissionDenied(err error) bool {
	return errors.Is(err, ErrPermission) || statusIs(err, 403)
}

// IsRateLimited reports rate limiting or HTTP 429.
func IsRateLimited(err error) bool {
	return errors.Is(err, ErrRateLimited) || statusIs(err, 429)
}

// IsRetryable reports whether an API error is marked retryable.
func IsRetryable(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.Retryable
}

// ErrorCode returns the API error code or an empty string.
func ErrorCode(err error) string {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.Code
	}
	return ""
}

func statusIs(err error, status int) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.StatusCode == status
}

// ResolvedCode returns the Code an APIError for status should carry, given
// code as read from the API response. The API's own code wins, except that
// an empty code, or one that is just the decimal status repeated (the
// envelope carrying no real code of its own), falls back to the status
// table below. No status in the table is ever left with an empty code; a
// status outside it, including one with no HTTP status at all (0, for a
// failure with no response), keeps whatever fallback applies, which is empty
// unless it is a 4xx or 5xx.
func ResolvedCode(status int, code string) string {
	if code != "" && code != strconv.Itoa(status) {
		return code
	}
	switch status {
	case http.StatusBadRequest:
		return "BadRequest"
	case http.StatusUnauthorized:
		return "Unauthorized"
	case http.StatusForbidden:
		return "Forbidden"
	case http.StatusNotFound:
		return "NotFound"
	case http.StatusConflict:
		return "Conflict"
	case http.StatusTooManyRequests:
		return "Throttled"
	default:
		switch {
		case status >= 500:
			return "ServerError"
		case status >= 400:
			return "ClientError"
		default:
			return ""
		}
	}
}

// LoginError is returned for every IAM User login failure. Reason is fixed
// text naming the step that failed, and Status is the HTTP status observed
// there, when one applies. CaptchaSuspected is true when the sign-in form
// was redisplayed after a submit, which the console does both for wrong
// credentials and for a required captcha. Err is always ErrAuth, or the
// context error when the attempt ended because ctx was canceled or expired;
// it is the only wrapped error; neither it nor the message built by Error
// ever holds a password, TOTP secret or code, token, cookie, authorization
// code, root email, or username, because both are built from Status,
// CaptchaSuspected, and Reason alone, never from the failing step's own
// error text (which can hold a token response body or a full URL).
type LoginError struct {
	Status           int
	CaptchaSuspected bool
	Reason           string
	Err              error
}

func (e *LoginError) Error() string {
	msg := e.Reason
	if e.Status > 0 {
		msg = fmt.Sprintf("%s (status %d)", msg, e.Status)
	}
	if e.CaptchaSuspected {
		msg += "; a captcha may be required"
	}
	return msg
}

func (e *LoginError) Unwrap() error { return e.Err }

// ServiceAccountLoginError describes a failed private token exchange. Reason
// contains fixed text; Err holds only ErrAuth or the caller's context error.
// No peer body, header, URL, transport error, or decode error is retained.
type ServiceAccountLoginError struct {
	Status int
	Reason string
	Err    error
}

func (e *ServiceAccountLoginError) Error() string {
	if e.Status > 0 {
		return fmt.Sprintf("%s (status %d)", e.Reason, e.Status)
	}
	return e.Reason
}

func (e *ServiceAccountLoginError) Unwrap() error { return e.Err }
