package transport

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"unicode"
)

type APIError struct {
	Operation  string
	StatusCode int
	Code       string
	Message    string
	Retryable  bool
	Err        error
}

func (e *APIError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	if e.Err != nil {
		return NetworkFailureCause(e.Err)
	}
	return fmt.Sprintf("request failed with status %d", e.StatusCode)
}

func (e *APIError) Unwrap() error {
	return e.Err
}

// ErrBodyTooLarge reports a response body that exceeds Request.MaxBody.
// DoRaw returns it with the observed HTTP status and no partial body.
// DoJSONStatus also preserves the status, but an oversized 4xx or 5xx body
// becomes an APIError classified from that status without any peer body text.
// Request.BodyLimitOverridesStatus keeps ErrBodyTooLarge instead for callers
// whose contracts give body overflow precedence over HTTP classification.
// Other statuses return ErrBodyTooLarge directly, including oversized 2xx
// responses; callers can distinguish these from HTTP failures with errors.Is.
var ErrBodyTooLarge = errors.New("transport: response body exceeds limit")

func retryableStatus(status int) bool {
	return status == http.StatusTooManyRequests || status == http.StatusBadGateway ||
		status == http.StatusServiceUnavailable || status == http.StatusGatewayTimeout
}

type errorBody struct {
	Code    json.RawMessage `json:"code"`
	Error   string          `json:"error"`
	Message string          `json:"message"`
	Detail  string          `json:"detail"`
	Title   string          `json:"title"`
	Errors  json.RawMessage `json:"errors"`
}

// text returns the first non-empty of message, error, detail, and title. The
// title is the problem+json summary, the weakest of the four.
func (eb errorBody) text() string {
	for _, s := range []string{eb.Message, eb.Error, eb.Detail, eb.Title} {
		if s != "" {
			return s
		}
	}
	return ""
}

// unwrapEntries reads the accounts API wrapper {"errors":[{"code","message"}]}.
// The first object entry gives the code; the messages of all object entries
// are joined with "; ". A null, non-object, or empty list gives the zero
// errorBody, so the caller falls back to the status text.
func unwrapEntries(raw json.RawMessage) errorBody {
	var entries []json.RawMessage
	if err := json.Unmarshal(raw, &entries); err != nil {
		return errorBody{}
	}
	var first errorBody
	var msgs []string
	found := false
	for _, entry := range entries {
		trimmed := bytes.TrimSpace(entry)
		if len(trimmed) == 0 || trimmed[0] != '{' {
			continue
		}
		var eb errorBody
		_ = json.Unmarshal(trimmed, &eb)
		if !found {
			first, found = eb, true
		}
		if m := eb.text(); m != "" {
			msgs = append(msgs, m)
		}
	}
	first.Message = strings.Join(msgs, "; ")
	first.Error, first.Detail, first.Title, first.Errors = "", "", "", nil
	return first
}

// codeString renders an envelope code as decimal text: null or an empty
// value gives "", a JSON string gives its value, and a JSON number is
// already decimal text, so it is returned as is.
func codeString(raw json.RawMessage) string {
	code, _ := ParseErrorCode(raw)
	return code
}

func decodeError(req Request, status int, body []byte) error {
	var eb errorBody
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) > 0 {
		if trimmed[0] == '[' {
			var items []errorBody
			if err := json.Unmarshal(trimmed, &items); err == nil && len(items) > 0 {
				eb = items[0]
			}
		} else {
			_ = json.Unmarshal(trimmed, &eb)
		}
	}
	if len(eb.Errors) > 0 && eb.text() == "" && codeString(eb.Code) == "" {
		eb = unwrapEntries(eb.Errors)
	}
	msg := eb.text()
	if msg == "" {
		msg = http.StatusText(status)
	}

	code := codeString(eb.Code)
	if req.ClassifyError != nil {
		if classified := req.ClassifyError(status, msg); classified != "" {
			code = classified
		}
	}
	apiErr := &APIError{
		Operation:  req.Operation,
		StatusCode: status,
		Code:       RedactErrorText(code, req.redactValues()...),
		Message:    RedactErrorText(strings.TrimSpace(msg), req.redactValues()...),
		Retryable:  req.retryable(status),
	}
	if req.WithholdMessage != "" {
		apiErr.Message = req.WithholdMessage
	}
	switch status {
	case http.StatusUnauthorized:
		apiErr.Err = errors.New("authentication failed")
	case http.StatusForbidden:
		apiErr.Err = errors.New("permission denied")
	case http.StatusNotFound:
		apiErr.Err = errors.New("resource not found")
	case http.StatusTooManyRequests:
		apiErr.Err = errors.New("rate limited")
	}
	return apiErr
}

// redactLineMinLen is the shortest trimmed line redact treats as worth
// matching on its own. A shorter line, such as a bare "-----BEGIN" split
// oddly, is common enough in ordinary text that redacting it would corrupt
// unrelated messages; see Request.Redact.
const redactLineMinLen = 8

// shortValueMinLen is the shortest Redact value redact ever substitutes in
// place. A shorter value, such as a four-character passphrase or a
// single-digit PIN, is likely to also occur as an ordinary substring of
// unrelated words or numbers ("pass" inside "passphrase", "4" inside "400"),
// so redact never substitutes it in place: a match discards the whole string
// instead.
const shortValueMinLen = 8

// redactedText replaces every match in s.
const redactedText = "[redacted]"

// redact returns s with every occurrence of each non-empty value in values
// replaced by "[redacted]": the value itself, its JSON-escaped form (the
// literal text a server produces when it echoes the value's own JSON
// encoding into a message, escape sequences and all, rather than decoding it
// first), and each of its lines that is still at least redactLineMinLen
// characters after trimming surrounding whitespace. An empty value is
// skipped, since it would otherwise match everywhere and corrupt s.
//
// A value shorter than shortValueMinLen is never substituted in place: if it
// matches anywhere in s (or its JSON-escaped form does), the whole of s is
// discarded and replaced by "[redacted]", since an in-place substitution
// risks corrupting an unrelated word or number that merely contains the same
// short text.
func redact(s string, values []string) string {
	if s == "" || len(values) == 0 {
		return s
	}
	forms := make(map[string]bool)
	for _, v := range values {
		if v == "" {
			continue
		}
		escaped := jsonEscapedForm(v)
		if len(v) < shortValueMinLen {
			if strings.Contains(s, v) || (escaped != "" && strings.Contains(s, escaped)) {
				return redactedText
			}
			continue
		}
		forms[v] = true
		if escaped != "" {
			forms[escaped] = true
		}
		for _, line := range strings.Split(v, "\n") {
			trimmed := strings.TrimSpace(line)
			if len(trimmed) >= redactLineMinLen {
				forms[trimmed] = true
			}
		}
	}
	var spans []redactionSpan
	for form := range forms {
		spans = findRedactionSpans(s, form, spans)
	}
	if len(spans) == 0 {
		return s
	}
	sort.Slice(spans, func(i, j int) bool {
		if spans[i].start == spans[j].start {
			return spans[i].end > spans[j].end
		}
		return spans[i].start < spans[j].start
	})
	var cleaned strings.Builder
	start, end := spans[0].start, spans[0].end
	previous := 0
	for _, span := range spans[1:] {
		if span.start < end {
			end = max(end, span.end)
			continue
		}
		cleaned.WriteString(s[previous:start])
		cleaned.WriteString(redactedText)
		previous, start, end = end, span.start, span.end
	}
	cleaned.WriteString(s[previous:start])
	cleaned.WriteString(redactedText)
	cleaned.WriteString(s[end:])
	return cleaned.String()
}

type redactionSpan struct{ start, end int }

// Match the original text, including overlapping occurrences. Merge a form's
// self-overlap while collecting it so repeated input does not grow the list.
func findRedactionSpans(s, form string, spans []redactionSpan) []redactionSpan {
	base := len(spans)
	for offset := 0; offset < len(s); {
		match := strings.Index(s[offset:], form)
		if match < 0 {
			break
		}
		start := offset + match
		end := start + len(form)
		if len(spans) > base && start < spans[len(spans)-1].end {
			spans[len(spans)-1].end = end
		} else {
			spans = append(spans, redactionSpan{start: start, end: end})
		}
		offset = start + 1
	}
	return spans
}

type safeDecodeError struct{ cause error }

func (*safeDecodeError) Error() string   { return "response failed to decode" }
func (e *safeDecodeError) Unwrap() error { return e.cause }

// SanitizeDecodeError preserves JSON error types using fixed decoder inputs.
// It never retains the original cause or copies its exported fields.
func SanitizeDecodeError(err error) error {
	var syntax *json.SyntaxError
	var mismatch *json.UnmarshalTypeError
	var invalid *json.InvalidUnmarshalError
	var cause error
	switch {
	case errors.As(err, &syntax):
		var value any
		cause = json.Unmarshal([]byte("?"), &value)
	case errors.As(err, &mismatch):
		var value int
		cause = json.Unmarshal([]byte(`""`), &value)
	case errors.As(err, &invalid):
		var value *int
		cause = json.Unmarshal([]byte(`0`), value)
	}
	return &safeDecodeError{cause: cause}
}

// jsonEscapedForm returns the text between the quotes json.Marshal would
// produce for v: v itself with its control characters, quotes, and
// backslashes escaped the way encoding/json escapes them. It returns "" only
// if json.Marshal itself fails, which a plain string value never does.
func jsonEscapedForm(v string) string {
	encoded, err := json.Marshal(v)
	if err != nil || len(encoded) < 2 {
		return ""
	}
	return string(encoded[1 : len(encoded)-1])
}

// redactCredentialKeys withholds server text containing a service-account
// secret key. Normalizing punctuation and case also covers nested JSON text;
// withholding the whole string avoids guessing where an untrusted value ends.
func redactCredentialKeys(text string) string {
	var key strings.Builder
	for _, r := range text {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			key.WriteRune(unicode.ToLower(r))
		}
	}
	if strings.Contains(key.String(), "clientsecret") {
		return redactedText
	}
	return text
}

// RedactErrorText scrubs known credentials and withholds credential-key text
// at the shared boundary for HTTP errors and successful failure envelopes.
// Inspect keys first so a known token cannot erase a credential-key marker.
func RedactErrorText(text string, values ...string) string {
	return redact(redactCredentialKeys(text), values)
}
