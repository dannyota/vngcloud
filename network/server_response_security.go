package network

import (
	"errors"
	"strings"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/transport"
)

func sanitizeServerError(err error, credentials []string) error {
	allowAPI := func(api *core.APIError) bool {
		if api == nil {
			return false
		}
		for _, text := range []string{api.Error(), api.Operation, api.Code, api.Message} {
			for _, credential := range credentials {
				if credential != "" && strings.Contains(text, credential) {
					return false
				}
			}
			if transport.RedactErrorText(text, credentials...) != text {
				return false
			}
		}
		return true
	}
	result, _ := sanitizeServerCause(err, credentials, allowAPI)
	return result
}

//nolint:errorlint // Inspect each node directly, including hidden API metadata and every joined cause.
func sanitizeServerCause(err error, credentials []string, allowAPI func(*core.APIError) bool) (error, bool) {
	if err == nil {
		return nil, false
	}
	text := err.Error()
	message := transport.RedactErrorText(text, credentials...)
	changed := message != text
	switch cause := err.(type) {
	case *core.APIError:
		safe := *cause
		var childChanged bool
		safe.Err, childChanged = sanitizeServerCause(cause.Err, credentials, allowAPI)
		safe.Operation = transport.RedactErrorText(cause.Operation, credentials...)
		safe.Code = transport.RedactErrorText(cause.Code, credentials...)
		safe.Message = transport.RedactErrorText(cause.Message, credentials...)
		// APIError can regenerate fixed cause text or a numeric status. Keep
		// identities separate when its rendered metadata is unsafe.
		if !allowAPI(&safe) || changed || childChanged || safe.Operation != cause.Operation || safe.Code != cause.Code || safe.Message != cause.Message {
			result := new(serverResponseError)
			result.message = transport.RedactErrorText(safe.Error(), credentials...)
			result.cause = safe.Err
			result.serverErrorIdentity = serverCauseIdentity(err)
			safe.Err = nil
			if safe.Message == "" {
				safe.Message = transport.RedactErrorText("request failed", credentials...)
			}
			result.api, result.allowAPI = &safe, allowAPI
			return result, true
		}
	case interface{ Unwrap() []error }:
		children := cause.Unwrap()
		safe := make([]error, len(children))
		for i, child := range children {
			var childChanged bool
			safe[i], childChanged = sanitizeServerCause(child, credentials, allowAPI)
			changed = changed || childChanged
		}
		if changed {
			result := new(serverResponseJoinedError)
			result.message, result.causes = message, safe
			result.serverErrorIdentity = serverCauseIdentity(err)
			return result, true
		}
	case interface{ Unwrap() error }:
		safe, childChanged := sanitizeServerCause(cause.Unwrap(), credentials, allowAPI)
		if changed || childChanged {
			result := new(serverResponseError)
			result.message, result.cause = message, safe
			result.serverErrorIdentity = serverCauseIdentity(err)
			return result, true
		}
	default:
		if changed {
			result := new(serverResponseError)
			result.message = message
			result.serverErrorIdentity = serverCauseIdentity(err)
			return result, true
		}
	}
	return err, false
}

// Keep the original cause private to the yes/no identity predicate. Unwrap
// exposes only sanitized causes, and As never delegates to this predicate.
type serverErrorIdentity func(error) bool

func serverCauseIdentity(original error) serverErrorIdentity {
	return func(target error) bool { return errors.Is(original, target) }
}

// Is preserves every original identity without exposing its cause text.
func (identity serverErrorIdentity) Is(target error) bool {
	return identity != nil && identity(target)
}

type serverResponseError struct {
	serverErrorIdentity
	cause    error
	message  string
	api      *core.APIError
	allowAPI func(*core.APIError) bool
}

// Error returns text checked against every credential observed by the operation.
func (e *serverResponseError) Error() string { return e.message }

// Unwrap exposes only causes checked against the operation's credentials.
func (e *serverResponseError) Unwrap() error { return e.cause }

// As exposes safe API metadata without reintroducing credential-bearing cause text.
func (e *serverResponseError) As(target any) bool {
	if api, ok := target.(**core.APIError); ok && e.api != nil && e.allowAPI != nil && e.allowAPI(e.api) {
		*api = e.api
		return true
	}
	return false
}

type serverResponseJoinedError struct {
	serverErrorIdentity
	message string
	causes  []error
}

// Error returns text checked against every credential observed by the operation.
func (e *serverResponseJoinedError) Error() string { return e.message }

// Unwrap preserves joined identities without retaining credential-bearing causes.
func (e *serverResponseJoinedError) Unwrap() []error { return e.causes }
