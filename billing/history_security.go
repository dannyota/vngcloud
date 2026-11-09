package billing

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"slices"
	"syscall"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/transport"
)

const historyWithheld = "billing history response withheld"

type historyNetworkFailure struct {
	cause   error
	timeout bool
}

func (*historyNetworkFailure) Error() string   { return "billing history network request failed" }
func (e *historyNetworkFailure) Unwrap() error { return e.cause }
func (e *historyNetworkFailure) Timeout() bool { return e.timeout }
func (*historyNetworkFailure) Temporary() bool { return false }

func historyInvalid(op string, cause error) error {
	return &core.APIError{Operation: op, StatusCode: http.StatusOK, Code: "InvalidResponse", Message: historyWithheld, Err: cause}
}

func historyIncomplete(op string) error {
	return &core.APIError{Operation: op, Code: "HistoryIncomplete", Message: "billing history is incomplete; result withheld", Err: ErrHistoryIncomplete}
}

func historyStatusCode(status int) string {
	if status >= 200 && status < 400 {
		return "InvalidResponse"
	}
	code := core.ResolvedCode(status, "")
	if code == "" {
		return "RequestFailed"
	}
	return code
}

// Retain only known-safe identities. Peer-bearing exported fields and nested
// causes must never survive through errors.As or errors.Unwrap.
func historySafeCause(err error) error {
	var causes []error
	for _, sentinel := range []error{context.Canceled, context.DeadlineExceeded, io.EOF, io.ErrUnexpectedEOF, io.ErrClosedPipe, net.ErrClosed, syscall.ECONNREFUSED, syscall.ECONNRESET, transport.ErrBodyTooLarge, core.ErrAuth, core.ErrNotFound, core.ErrPermission, core.ErrRateLimited, core.ErrInvalidConfig, core.ErrNoCredentials, core.ErrCredentialsFile, core.ErrTokenCache} {
		if errors.Is(err, sentinel) {
			causes = append(causes, sentinel)
		}
	}
	var syntax *json.SyntaxError
	var mismatch *json.UnmarshalTypeError
	if errors.As(err, &syntax) || errors.As(err, &mismatch) {
		causes = append(causes, transport.SanitizeDecodeError(err))
	}
	cause := errors.Join(causes...)
	var network net.Error
	if errors.As(err, &network) {
		return &historyNetworkFailure{cause: cause, timeout: network.Timeout()}
	}
	return cause
}

func historyRequestError(op string, status int, err error) error {
	var login *core.LoginError
	if errors.As(err, &login) {
		return &core.LoginError{Status: login.Status, Reason: "billing history authentication failed", CaptchaSuspected: login.CaptchaSuspected, Err: historySafeCause(login.Err)}
	}
	var exchange *core.ServiceAccountLoginError
	if errors.As(err, &exchange) {
		return &core.ServiceAccountLoginError{Status: exchange.Status, Reason: "billing history authentication failed", Err: historySafeCause(exchange.Err)}
	}
	var api *core.APIError
	retryable := errors.As(err, &api) && api.Retryable
	return &core.APIError{Operation: op, StatusCode: status, Code: historyStatusCode(status), Message: historyWithheld, Retryable: retryable, Err: historySafeCause(err)}
}

func retainHistoryCredentials(retained, observed []string) []string {
	for _, value := range observed {
		if value != "" && !slices.Contains(retained, value) {
			retained = append(retained, value)
		}
	}
	return retained
}

func sanitizeHistoryPointer(value *string, credentials []string) {
	if value != nil {
		*value = transport.RedactErrorText(*value, credentials...)
	}
}

func sanitizeHistoryTransaction(item *Transaction, credentials []string) {
	item.Type = transport.RedactErrorText(item.Type, credentials...)
	item.Direction = transport.RedactErrorText(item.Direction, credentials...)
	item.CreatedAt = transport.RedactErrorText(item.CreatedAt, credentials...)
	sanitizeHistoryPointer(item.SourceReference, credentials)
	sanitizeHistoryPointer(item.Description, credentials)
	sanitizeHistoryPointer(item.UserServiceReference, credentials)
}

func sanitizeHistoryPayment(item *Payment, credentials []string) {
	item.TransactionID = transport.RedactErrorText(item.TransactionID, credentials...)
	item.Gateway = transport.RedactErrorText(item.Gateway, credentials...)
	item.Status = transport.RedactErrorText(item.Status, credentials...)
	item.CreatedAt = transport.RedactErrorText(item.CreatedAt, credentials...)
	sanitizeHistoryPointer(item.Description, credentials)
}
