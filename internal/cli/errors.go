package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"danny.vn/vngcloud"
)

// usageError marks a bad flag, argument, unknown command, or missing
// required input as the caller's mistake. It always exits 2 with JSON class
// InvalidUsage. Every command sets SilenceUsage, so cobra never prints its
// own usage text alongside the JSON error line.
type usageError struct{ msg string }

func newUsageError(format string, args ...any) usageError {
	return usageError{msg: fmt.Sprintf(format, args...)}
}

func (e usageError) Error() string { return e.msg }

// readOnlyError reports a write command refused because read-only is on.
// source names the setting that turned it on, for example "--read-only" or
// `read_only in profile "agent"`.
type readOnlyError struct{ source string }

func (e readOnlyError) Error() string {
	return "read-only: refused by " + e.source
}

// queryFailedError reports a --query that failed to run after the operation
// itself succeeded. writeSucceeded marks a write, so the message tells the
// caller not to retry it.
type queryFailedError struct {
	err            error
	writeSucceeded bool
}

func (e *queryFailedError) Error() string {
	if e.writeSucceeded {
		return fmt.Sprintf("query failed after the write succeeded: %s", e.err)
	}
	return fmt.Sprintf("query failed: %s", e.err)
}

func (e *queryFailedError) Unwrap() error { return e.err }

// errorEnvelope is the one JSON line the CLI prints to stderr for a failed
// command, per the CLI design's error shape.
type errorEnvelope struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Status    int    `json:"status,omitempty"`
	Operation string `json:"operation,omitempty"`
}

// classify turns err into the stderr JSON shape. For an *vngcloud.APIError,
// code is APIError.Code (already resolved to the status-derived fallback by
// the SDK), or "RequestFailed" when the error carries neither a code nor an
// HTTP status. Every other error is named by one of the CLI's own classes:
// InvalidUsage, ReadOnly, InvalidConfig, NoCredentials, LoginFailed,
// RequestFailed, or QueryFailed.
func classify(err error) errorEnvelope {
	var apiErr *vngcloud.APIError
	if errors.As(err, &apiErr) {
		// Message is the API's own message alone: apiErr.Error() repeats the
		// operation and status inside the string, which would duplicate the
		// separate operation and status fields below.
		message := apiErr.Message
		if message == "" {
			message = apiErr.Error()
		}
		env := errorEnvelope{Code: apiErr.Code, Message: message, Operation: apiErr.Operation}
		if apiErr.StatusCode > 0 {
			env.Status = apiErr.StatusCode
		}
		if env.Code == "" {
			env.Code = "RequestFailed"
		}
		return env
	}

	var usageErr usageError
	if errors.As(err, &usageErr) {
		return errorEnvelope{Code: "InvalidUsage", Message: err.Error()}
	}
	var roErr readOnlyError
	if errors.As(err, &roErr) {
		return errorEnvelope{Code: "ReadOnly", Message: err.Error()}
	}
	var queryErr *queryFailedError
	if errors.As(err, &queryErr) {
		return errorEnvelope{Code: "QueryFailed", Message: err.Error()}
	}
	if errors.Is(err, vngcloud.ErrNoCredentials) || errors.Is(err, vngcloud.ErrCredentialsFile) {
		return errorEnvelope{Code: "NoCredentials", Message: err.Error()}
	}
	var loginErr *vngcloud.LoginError
	if errors.As(err, &loginErr) {
		return errorEnvelope{Code: "LoginFailed", Message: err.Error()}
	}
	if errors.Is(err, vngcloud.ErrInvalidInput) {
		return errorEnvelope{Code: "InvalidUsage", Message: err.Error()}
	}
	if errors.Is(err, vngcloud.ErrInvalidConfig) {
		return errorEnvelope{Code: "InvalidConfig", Message: err.Error()}
	}
	return errorEnvelope{Code: "RequestFailed", Message: err.Error()}
}

// exitCode maps err to the CLI's process exit code, checked in this fixed
// order: a canceled or expired context anywhere in the chain always exits 1,
// even for a *LoginError or an *APIError that wraps it, because the command
// was interrupted rather than genuinely rejected. Missing or refused
// credentials and every login failure exit 3. A not-found result exits 4.
// Every remaining usage or config problem exits 2. Anything else exits 1.
func exitCode(err error) int {
	if err == nil {
		return 0
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return 1
	}
	if errors.Is(err, vngcloud.ErrNoCredentials) || errors.Is(err, vngcloud.ErrCredentialsFile) {
		return 3
	}
	var loginErr *vngcloud.LoginError
	if errors.As(err, &loginErr) {
		return 3
	}
	if errors.Is(err, vngcloud.ErrAuth) {
		return 3
	}
	if vngcloud.IsNotFound(err) {
		return 4
	}
	// A runtime --query failure (queryFailedError) is deliberately not listed
	// here: it means the operation itself already succeeded, so it falls
	// through to the plain exit 1 below rather than the usage-error exit 2,
	// per the CLI design.
	var usageErr usageError
	var roErr readOnlyError
	switch {
	case errors.As(err, &usageErr),
		errors.As(err, &roErr),
		errors.Is(err, vngcloud.ErrInvalidInput),
		errors.Is(err, vngcloud.ErrInvalidConfig),
		errors.Is(err, vngcloud.ErrProjectAmbiguous):
		return 2
	}
	return 1
}

// printError writes err to w as one JSON line, per the CLI design's error
// shape. A JSON encoding failure (never expected, since errorEnvelope holds
// only strings and an int) falls back to a plain-text line so a command
// never exits silently.
func printError(w io.Writer, err error) {
	env := classify(err)
	data, encErr := json.Marshal(struct {
		Error errorEnvelope `json:"error"`
	}{Error: env})
	// A write failure here has nowhere left to report to (this is already the
	// error-output path), so every error return below is deliberately
	// ignored.
	if encErr != nil {
		_, _ = fmt.Fprintf(w, "{\"error\":{\"code\":%q,\"message\":%q}}\n", "RequestFailed", err.Error())
		return
	}
	_, _ = w.Write(data)
	_, _ = fmt.Fprintln(w)
}
