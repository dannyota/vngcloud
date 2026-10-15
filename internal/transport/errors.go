package transport

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptrace"
	"sort"
	"strings"
	"sync/atomic"
	"unicode"
)

type APIError struct {
	// KeptObservedStatus marks only an opted-in response body read or close
	// failure, whose mapped SDK sentinel must retain the sanitized cause.
	KeptObservedStatus bool

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

// Bound preparation bytes and matcher comparisons across every credential form.
// Span and form limits also bound sorting and retained match metadata. Exhaustion
// withholds the whole field; collected matches must never escape as partial work.
const (
	redactionWorkLimit = 8 << 20
	redactionSpanLimit = 4096
	redactionFormLimit = 256
)

type redactionBudget struct{ work, spans int }

func (b *redactionBudget) takeWork(n int) bool {
	if n > b.work {
		return false
	}
	b.work -= n
	return true
}

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
	budget := redactionBudget{work: redactionWorkLimit, spans: redactionSpanLimit}
	forms := make(map[string]bool)
	addForm := func(form string) bool {
		if !budget.takeWork(len(form)) || !budget.takeWork(len(form)+1) {
			return false
		}
		if !forms[form] && len(forms) == redactionFormLimit {
			return false
		}
		forms[form] = true
		return true
	}
	for _, v := range values {
		if !budget.takeWork(1) {
			return redactedText
		}
		if v == "" {
			continue
		}
		// JSON escaping can expand each input byte into six output bytes.
		// Reserve before encoding, hashing forms, or preparing match tables.
		if len(v) > budget.work/6 {
			return redactedText
		}
		budget.takeWork(6 * len(v))
		escaped := jsonEscapedForm(v)
		if len(v) < shortValueMinLen {
			for _, form := range []string{v, escaped} {
				matches, ok := findRedactionSpans(s, form, nil, &budget)
				if !ok || len(matches) > 0 {
					return redactedText
				}
			}
			continue
		}
		if !addForm(v) || (escaped != "" && !addForm(escaped)) {
			return redactedText
		}
		for line := range strings.SplitSeq(v, "\n") {
			trimmed := strings.TrimSpace(line)
			if len(trimmed) >= redactLineMinLen && !addForm(trimmed) {
				return redactedText
			}
		}
	}
	var spans []redactionSpan
	for form := range forms {
		var ok bool
		spans, ok = findRedactionSpans(s, form, spans, &budget)
		if !ok {
			return redactedText
		}
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

// KMP reuses prefix matches, including self-overlap, instead of comparing the
// whole form at each byte. Preparation and scanning are linear in their inputs.
func findRedactionSpans(s, form string, spans []redactionSpan, budget *redactionBudget) ([]redactionSpan, bool) {
	if form == "" || len(form) > len(s) {
		return spans, true
	}
	if !budget.takeWork(len(form)) {
		return nil, false
	}
	prefix := make([]int, len(form))
	for i, matched := 1, 0; i < len(form); i++ {
		for {
			if !budget.takeWork(1) {
				return nil, false
			}
			if form[i] == form[matched] {
				matched++
				break
			}
			if matched == 0 {
				break
			}
			matched = prefix[matched-1]
		}
		prefix[i] = matched
	}
	base := len(spans)
	for i, matched := 0, 0; i < len(s); i++ {
		for {
			if !budget.takeWork(1) {
				return nil, false
			}
			if s[i] == form[matched] {
				matched++
				break
			}
			if matched == 0 {
				break
			}
			matched = prefix[matched-1]
		}
		if matched != len(form) {
			continue
		}
		start, end := i+1-len(form), i+1
		if len(spans) > base && start < spans[len(spans)-1].end {
			spans[len(spans)-1].end = end
		} else {
			if budget.spans == 0 {
				return nil, false
			}
			budget.spans--
			spans = append(spans, redactionSpan{start: start, end: end})
		}
		matched = prefix[matched-1]
	}
	return spans, true
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
	const key = "clientsecret"
	// Match the normalized stream without allocating a copy of peer text.
	// The internal 'c' is the marker's only nonempty repeated prefix.
	prefix := [len(key)]int{8: 1}
	matched := 0
	for _, r := range text {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			continue
		}
		r = unicode.ToLower(r)
		for matched > 0 && r != rune(key[matched]) {
			matched = prefix[matched-1]
		}
		if r == rune(key[matched]) {
			matched++
			if matched == len(key) {
				return redactedText
			}
		}
	}
	return text
}

// RedactErrorText scrubs known credentials and withholds credential-key text
// at the shared boundary for HTTP errors and successful failure envelopes.
// Inspect keys first so a known token cannot erase a credential-key marker.
func RedactErrorText(text string, values ...string) string {
	return redact(redactCredentialKeys(text), values)
}

// Keep evidence across retries and 401 recovery. Once any send is uncertain,
// later local refusals cannot make the whole call safe to repeat.
func (r *Request) beginNotSent() func(error) {
	if r.NotSent == nil {
		return func(error) {}
	}
	*r.NotSent = false
	safe := true
	r.notSentState = &safe
	return func(err error) { *r.NotSent = err != nil && safe }
}

func (r Request) observeSend(req *http.Request, client *http.Client) (*http.Request, func(bool, error)) {
	if r.notSentState == nil {
		return req, func(bool, error) {}
	}
	previous := *r.notSentState
	*r.notSentState = false
	base := client.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	if _, ok := base.(*http.Transport); !ok {
		// Arbitrary RoundTrippers may send without trace hooks or return a
		// dial-shaped error after a write. No evidence means unknown.
		return req, func(bool, error) {}
	}
	var connecting, connected atomic.Bool
	trace := &httptrace.ClientTrace{
		GetConn: func(string) { connecting.Store(true) },
		GotConn: func(httptrace.GotConnInfo) { connected.Store(true) },
	}
	req = req.WithContext(httptrace.WithClientTrace(req.Context(), trace))
	return req, func(hasResponse bool, err error) {
		// GotConn precedes every request write, including reused connections
		// and redirects. A response or any connection makes this uncertain.
		if previous && !hasResponse && err != nil && connecting.Load() &&
			!connected.Load() && isDialError(err) {
			*r.notSentState = true
		}
	}
}
