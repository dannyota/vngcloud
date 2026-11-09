package billing

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"syscall"
	"unicode"
	"unicode/utf8"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/transport"
)

const historyWithheld = "billing history response withheld"

var numericHistoryCredential = regexp.MustCompile(`^[+-]?([0-9]+\.?[0-9]*|\.[0-9]+)([eE][+-]?[0-9]+)?$`)

// Number formatting cannot reliably redact numeric credentials. Keep the
// refusal consistent with quotes and docs/design/billing-history.md.
func historyHasNumericCredential(credentials []string) bool {
	for _, credential := range credentials {
		if numericHistoryCredential.MatchString(strings.TrimSpace(credential)) {
			return true
		}
	}
	return false
}

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

const historyRedactionBudget = 8 << 20
const historyRedactionForms = 256

type historyCredentialForm struct {
	runes  []rune
	prefix []int
}

type historyRedactor struct {
	forms    []historyCredentialForm
	withhold bool
}

// Canonicalize Unicode simple-fold classes without changing returned text.
func historyFoldRune(value rune) rune {
	least := value
	for next := unicode.SimpleFold(value); next != value; next = unicode.SimpleFold(next) {
		least = min(least, next)
	}
	return least
}

func newHistoryCredentialForm(text string) historyCredentialForm {
	form := historyCredentialForm{runes: []rune(text)}
	for i, value := range form.runes {
		form.runes[i] = historyFoldRune(value)
	}
	form.prefix = make([]int, len(form.runes))
	for i, matched := 1, 0; i < len(form.runes); i++ {
		for matched > 0 && form.runes[i] != form.runes[matched] {
			matched = form.prefix[matched-1]
		}
		if form.runes[i] == form.runes[matched] {
			matched++
		}
		form.prefix[i] = matched
	}
	return form
}

// Limit preparation and matching work; exhaustion withholds the whole field.
// Only literal and JSON-escaped forms participate, without joining fragments.
func newHistoryRedactor(credentials []string) historyRedactor {
	var redactor historyRedactor
	seen := make(map[string]struct{})
	remaining := historyRedactionBudget
	for _, credential := range credentials {
		if credential == "" {
			continue
		}
		// Reserve for JSON expansion, folding, and prefix-table preparation.
		if len(credential) > remaining/12 {
			return historyRedactor{withhold: true}
		}
		remaining -= 12 * len(credential)
		encoded, err := json.Marshal(credential)
		if err != nil {
			return historyRedactor{withhold: true}
		}
		for _, text := range []string{credential, string(encoded[1 : len(encoded)-1])} {
			if _, exists := seen[text]; exists {
				continue
			}
			if len(seen) == historyRedactionForms {
				return historyRedactor{withhold: true}
			}
			seen[text] = struct{}{}
			redactor.forms = append(redactor.forms, newHistoryCredentialForm(text))
		}
	}
	return redactor
}

// KMP bounds overlapping literal searches. Retain original byte positions so
// Unicode folds with different UTF-8 widths still select the exact substring.
func (f historyCredentialForm) matches(value string, budget *int, found map[string]struct{}) bool {
	size := len(f.runes)
	if len(value) < size {
		return true
	}
	if size > *budget {
		return false
	}
	*budget -= size
	positions := make([]int, size)
	matched, index := 0, 0
	for offset, valueRune := range value {
		if *budget < 2 {
			return false
		}
		*budget -= 2
		positions[index%size] = offset
		folded := historyFoldRune(valueRune)
		for matched > 0 && folded != f.runes[matched] {
			if *budget == 0 {
				return false
			}
			*budget -= 1
			matched = f.prefix[matched-1]
		}
		if folded == f.runes[matched] {
			matched++
		}
		if matched == size {
			_, width := utf8.DecodeRuneInString(value[offset:])
			start, end := positions[(index+1-size)%size], offset+width
			// Hashing a matched spelling also consumes work, including duplicates.
			if end-start > *budget {
				return false
			}
			*budget -= end - start
			found[value[start:end]] = struct{}{}
			if len(found) > historyRedactionForms {
				return false
			}
			matched = f.prefix[matched-1]
		}
		index++
	}
	return true
}

func (r historyRedactor) redact(value string) string {
	value = transport.RedactErrorText(value)
	if value == "" {
		return value
	}
	if r.withhold {
		return "[redacted]"
	}
	budget := historyRedactionBudget
	found := make(map[string]struct{})
	for _, form := range r.forms {
		if !form.matches(value, &budget, found) {
			return "[redacted]"
		}
	}
	if len(found) == 0 {
		return value
	}
	spellings := make([]string, 0, len(found))
	for text := range found {
		spellings = append(spellings, text)
	}
	return transport.RedactErrorText(value, spellings...)
}

func sanitizeHistoryPointer(value *string, redactor historyRedactor) {
	if value != nil {
		*value = redactor.redact(*value)
	}
}

func sanitizeHistoryTransaction(item *Transaction, redactor historyRedactor) {
	item.Type = redactor.redact(item.Type)
	item.Direction = redactor.redact(item.Direction)
	item.CreatedAt = redactor.redact(item.CreatedAt)
	sanitizeHistoryPointer(item.SourceReference, redactor)
	sanitizeHistoryPointer(item.Description, redactor)
	sanitizeHistoryPointer(item.UserServiceReference, redactor)
}

func sanitizeHistoryPayment(item *Payment, redactor historyRedactor) {
	item.TransactionID = redactor.redact(item.TransactionID)
	item.Gateway = redactor.redact(item.Gateway)
	item.Status = redactor.redact(item.Status)
	item.CreatedAt = redactor.redact(item.CreatedAt)
	sanitizeHistoryPointer(item.Description, redactor)
}
