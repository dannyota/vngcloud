package core

import (
	"errors"
	"fmt"
)

var (
	ErrAuth             = errors.New("vngcloud: authentication failed")
	ErrNotFound         = errors.New("vngcloud: resource not found")
	ErrPermission       = errors.New("vngcloud: permission denied")
	ErrRateLimited      = errors.New("vngcloud: rate limited")
	ErrProjectNotFound  = errors.New("vngcloud: no project found for region")
	ErrProjectAmbiguous = errors.New("vngcloud: multiple projects found for region")
	ErrMissingProjectID = errors.New("vngcloud: project id is required")
	ErrInvalidConfig    = errors.New("vngcloud: invalid config")
)

// APIError describes an error response returned by VNG Cloud.
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
	if e.Operation != "" {
		msg = e.Operation + ": " + msg
	}
	if e.StatusCode > 0 {
		msg = fmt.Sprintf("%s (status %d)", msg, e.StatusCode)
	}
	return msg
}

func (e *APIError) Unwrap() error {
	return e.Err
}

func IsNotFound(err error) bool {
	return errors.Is(err, ErrNotFound) || statusIs(err, 404)
}

func IsPermissionDenied(err error) bool {
	return errors.Is(err, ErrPermission) || statusIs(err, 403)
}

func IsRateLimited(err error) bool {
	return errors.Is(err, ErrRateLimited) || statusIs(err, 429)
}

func IsRetryable(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.Retryable
}

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
