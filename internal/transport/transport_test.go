package transport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type testTokenSource struct {
	count atomic.Int64
}

func (s *testTokenSource) Token(context.Context) (Token, error) {
	n := s.count.Add(1)
	return Token{
		AccessToken: fmt.Sprintf("token-%d", n),
		ExpiresAt:   time.Now().Add(time.Hour),
	}, nil
}

func (s *testTokenSource) Invalidate(string) {}

func TestDoJSONRefreshesOn401(t *testing.T) {
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := requests.Add(1)
		if n == 1 {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"message":"expired"}`))
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer token-2" {
			t.Fatalf("Authorization = %q", got)
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	source := &testTokenSource{}
	client := New(Config{
		HTTPClient:  server.Client(),
		TokenSource: source,
	})

	var out struct {
		OK bool `json:"ok"`
	}
	if err := client.DoJSON(context.Background(), Request{
		Operation: "test",
		Method:    http.MethodGet,
		URL:       server.URL,
		OK:        []int{200},
	}, &out); err != nil {
		t.Fatalf("DoJSON() error = %v", err)
	}
	if !out.OK {
		t.Fatal("response was not decoded")
	}
	if source.count.Load() != 2 {
		t.Fatalf("token refresh count = %d", source.count.Load())
	}
}

func TestDoJSONCapturesResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"items":[{"id":"one"}]}`))
	}))
	defer server.Close()

	var captured Capture
	client := New(Config{
		HTTPClient: server.Client(),
		Capture: func(c Capture) {
			captured = c
			captured.Body = append([]byte(nil), c.Body...)
			c.Body[0] = '['
		},
	})

	var out struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	if err := client.DoJSON(context.Background(), Request{
		Operation: "Inventory.List",
		Method:    http.MethodGet,
		URL:       server.URL + "/resources",
		OK:        []int{200},
		SkipAuth:  true,
	}, &out); err != nil {
		t.Fatalf("DoJSON() error = %v", err)
	}

	if captured.Operation != "Inventory.List" {
		t.Fatalf("captured operation = %q", captured.Operation)
	}
	if captured.Method != http.MethodGet {
		t.Fatalf("captured method = %q", captured.Method)
	}
	if captured.URL != server.URL+"/resources" {
		t.Fatalf("captured URL = %q", captured.URL)
	}
	if captured.StatusCode != http.StatusOK {
		t.Fatalf("captured status = %d", captured.StatusCode)
	}
	if string(captured.Body) != `{"items":[{"id":"one"}]}` {
		t.Fatalf("captured body = %s", captured.Body)
	}

	if out.Items[0].ID != "one" {
		t.Fatal("captured body mutation changed decoded output")
	}
}

// TestDoJSONSensitiveSkipsCapture checks that Sensitive suppresses the
// capture hook for its own request only: a later, non-Sensitive request on
// the same Client is still captured, proving Sensitive is a per-request
// flag rather than something that disables capture globally.
func TestDoJSONSensitiveSkipsCapture(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"secret":"top-secret-value"}`))
	}))
	defer server.Close()

	var captures []Capture
	client := New(Config{
		HTTPClient: server.Client(),
		Capture: func(c Capture) {
			captures = append(captures, c)
		},
	})

	var out struct {
		Secret string `json:"secret"`
	}
	if err := client.DoJSON(context.Background(), Request{
		Operation: "test.Sensitive",
		Method:    http.MethodGet,
		URL:       server.URL,
		OK:        []int{200},
		SkipAuth:  true,
		Sensitive: true,
	}, &out); err != nil {
		t.Fatalf("DoJSON() error = %v", err)
	}
	if len(captures) != 0 {
		t.Fatalf("capture called %d time(s) for a Sensitive request, want 0", len(captures))
	}

	if err := client.DoJSON(context.Background(), Request{
		Operation: "test.NotSensitive",
		Method:    http.MethodGet,
		URL:       server.URL,
		OK:        []int{200},
		SkipAuth:  true,
	}, &out); err != nil {
		t.Fatalf("DoJSON() error = %v", err)
	}
	if len(captures) != 1 {
		t.Fatalf("capture called %d time(s) after a non-Sensitive request, want 1", len(captures))
	}
}

// TestDoJSONSensitiveDecodeErrorOmitsBody checks that a Sensitive request
// whose response fails to decode into the caller's out returns a fixed,
// generic error, never one built from the underlying json error (which, for
// a plain request, ends up in the returned error's chain and so its
// Error() text). This proves the Sensitive branch in DoJSONStatus actually
// runs, rather than relying only on json.Unmarshal never quoting a body on
// its own.
func TestDoJSONSensitiveDecodeErrorOmitsBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`"unexpected-string-body"`))
	}))
	defer server.Close()

	client := New(Config{HTTPClient: server.Client()})

	var out struct {
		Secret string `json:"secret"`
	}
	sensitiveErr := client.DoJSON(context.Background(), Request{
		Operation: "test.SensitiveDecode",
		Method:    http.MethodGet,
		URL:       server.URL,
		OK:        []int{200},
		SkipAuth:  true,
		Sensitive: true,
	}, &out)
	if sensitiveErr == nil {
		t.Fatal("DoJSON() error = nil, want a decode error")
	}
	if strings.Contains(sensitiveErr.Error(), "unexpected-string-body") {
		t.Fatalf("sensitive decode error holds the response body: %q", sensitiveErr.Error())
	}

	plainErr := client.DoJSON(context.Background(), Request{
		Operation: "test.PlainDecode",
		Method:    http.MethodGet,
		URL:       server.URL,
		OK:        []int{200},
		SkipAuth:  true,
	}, &out)
	if plainErr == nil {
		t.Fatal("DoJSON() error = nil, want a decode error")
	}
	if sensitiveErr.Error() == plainErr.Error() {
		t.Fatalf("Sensitive decode error is identical to the plain one: %q", sensitiveErr.Error())
	}
}

func TestDoJSONContextCancelDuringRetryWait(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	c := New(Config{HTTPClient: server.Client(), RetryCount: 3, RetryInterval: 10 * time.Second})
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	err := c.DoJSON(ctx, Request{Operation: "Op", URL: server.URL, SkipAuth: true}, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if time.Since(start) > 3*time.Second {
		t.Fatalf("retry wait ignored context cancellation (took %s)", time.Since(start))
	}
}

// TestCancelledContextNotRetryable checks that a request made with an
// already-canceled context fails with Retryable false, even for an
// idempotent GET that would otherwise be retried after this kind of
// transport error.
func TestCancelledContextNotRetryable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	c := New(Config{HTTPClient: server.Client()})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := c.DoJSON(ctx, Request{
		Operation: "Op",
		Method:    http.MethodGet,
		URL:       server.URL,
		SkipAuth:  true,
	}, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %T", err)
	}
	if apiErr.Retryable {
		t.Fatal("Retryable = true, want false for a canceled context")
	}
}

func TestBackoffBounds(t *testing.T) {
	c := New(Config{RetryInterval: time.Second})
	for attempt := 0; attempt < 10; attempt++ {
		d := c.backoff(attempt, 0)
		if d <= 0 || d > 30*time.Second {
			t.Fatalf("attempt %d: backoff %s out of bounds", attempt, d)
		}
	}
	if d := c.backoff(0, 5*time.Second); d != 5*time.Second {
		t.Fatalf("expected Retry-After to win, got %s", d)
	}
	if d := c.backoff(0, 10*time.Minute); d != 30*time.Second {
		t.Fatalf("expected Retry-After to be capped, got %s", d)
	}
}

func TestRetryAfterHint(t *testing.T) {
	h := http.Header{}
	if retryAfterHint(h) != 0 {
		t.Fatal("expected 0 for missing header")
	}
	h.Set("Retry-After", "7")
	if retryAfterHint(h) != 7*time.Second {
		t.Fatalf("unexpected hint: %s", retryAfterHint(h))
	}
	h.Set("Retry-After", "garbage")
	if retryAfterHint(h) != 0 {
		t.Fatal("expected 0 for unparseable header")
	}
}

func TestDecodeErrorArrayBody(t *testing.T) {
	err := decodeError(Request{Operation: "Compute.ListServers", Method: http.MethodGet}, 403, []byte(`[{"code":"IAM_PERMISSION_DENIED","message":"IAM denied action"}]`))
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %T", err)
	}
	if apiErr.Code != "IAM_PERMISSION_DENIED" {
		t.Fatalf("unexpected code: %q", apiErr.Code)
	}
	if apiErr.Message != "IAM denied action" {
		t.Fatalf("unexpected message: %q", apiErr.Message)
	}
}

func TestDecodeErrorObjectBody(t *testing.T) {
	err := decodeError(Request{Operation: "Op", Method: http.MethodGet}, 404, []byte(`{"code":"NOT_FOUND","message":"missing"}`))
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %T", err)
	}
	if apiErr.Code != "NOT_FOUND" || apiErr.Message != "missing" {
		t.Fatalf("unexpected error fields: %+v", apiErr)
	}
}

// fakePEMPrivateKey builds a placeholder PEM private key from separate
// header, body, and footer literals, joined only here: a secret scanner
// matches a private key's BEGIN/END markers as one contiguous string, so no
// test literal ever writes that shape directly in source.
func fakePEMPrivateKey(body string) string {
	const header = "-----BEGIN PRIVATE KEY-----"
	const footer = "-----END PRIVATE KEY-----"
	return header + "\n" + body + "\n" + footer
}

// TestDecodeErrorRedactsWholeValue checks that a Redact value quoted whole in
// the message is replaced.
func TestDecodeErrorRedactsWholeValue(t *testing.T) {
	key := fakePEMPrivateKey("FAKEKEYMATERIAL")
	req := Request{Operation: "Op", Method: http.MethodPost, Redact: []string{key}}
	body, marshalErr := json.Marshal(map[string]string{"message": "rejected: " + key})
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	err := decodeError(req, 400, body)
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %T", err)
	}
	if strings.Contains(apiErr.Message, key) {
		t.Fatalf("Message = %q, still holds the redacted value", apiErr.Message)
	}
	if !strings.Contains(apiErr.Message, "[redacted]") {
		t.Fatalf("Message = %q, want it to contain [redacted]", apiErr.Message)
	}
}

// TestDecodeErrorRedactsOneLine checks that a message quoting only one line
// of a multi-line Redact value, at least 8 characters after trimming, still
// gets that line redacted even though the whole value never appears.
func TestDecodeErrorRedactsOneLine(t *testing.T) {
	const quotedLine = "MIIFAKELINEOFKEYMATERIAL"
	key := fakePEMPrivateKey(quotedLine)
	req := Request{Operation: "Op", Method: http.MethodPost, Redact: []string{key}}
	err := decodeError(req, 400, []byte(`{"message":"invalid line: `+quotedLine+`"}`))
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %T", err)
	}
	if strings.Contains(apiErr.Message, quotedLine) {
		t.Fatalf("Message = %q, still holds the quoted line", apiErr.Message)
	}
	if !strings.Contains(apiErr.Message, "[redacted]") {
		t.Fatalf("Message = %q, want it to contain [redacted]", apiErr.Message)
	}
}

// TestDecodeErrorRedactsJSONEscapedForm checks that a message embedding the
// value's JSON-escaped form (its newlines as literal backslash-n, as a
// server might when it dumps the received field's raw JSON text into an
// error) is also redacted, not just the value's own unescaped form.
func TestDecodeErrorRedactsJSONEscapedForm(t *testing.T) {
	key := fakePEMPrivateKey("MIIFAKELINEOFKEYMATERIAL")
	// escaped is key with its real newlines as literal backslash-n, the text
	// a server produces when it echoes the value's own JSON encoding into a
	// message rather than decoding it first.
	escaped := strings.ReplaceAll(key, "\n", `\n`)
	req := Request{Operation: "Op", Method: http.MethodPost, Redact: []string{key}}
	body, err := json.Marshal(map[string]string{"message": "invalid field value \"" + escaped + "\""})
	if err != nil {
		t.Fatal(err)
	}
	decoded := decodeError(req, 400, body)
	var apiErr *APIError
	if !errors.As(decoded, &apiErr) {
		t.Fatalf("expected *APIError, got %T", decoded)
	}
	if strings.Contains(apiErr.Message, escaped) {
		t.Fatalf("Message = %q, still holds the JSON-escaped value", apiErr.Message)
	}
	if !strings.Contains(apiErr.Message, "[redacted]") {
		t.Fatalf("Message = %q, want it to contain [redacted]", apiErr.Message)
	}
}

// TestDecodeErrorRedactsCode checks that Redact also covers the envelope's
// code field, not just its message.
func TestDecodeErrorRedactsCode(t *testing.T) {
	const passphrase = "correct-horse-battery"
	req := Request{Operation: "Op", Method: http.MethodPost, Redact: []string{passphrase}}
	err := decodeError(req, 400, []byte(`{"code":"bad: `+passphrase+`","message":"m"}`))
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %T", err)
	}
	if strings.Contains(apiErr.Code, passphrase) {
		t.Fatalf("Code = %q, still holds the redacted value", apiErr.Code)
	}
}

// TestDecodeErrorRedactSkipsEmptyValues checks that an empty Redact value,
// such as an unset optional passphrase, is never matched: an empty string
// would otherwise appear "in" every message and corrupt it.
func TestDecodeErrorRedactSkipsEmptyValues(t *testing.T) {
	req := Request{Operation: "Op", Method: http.MethodPost, Redact: []string{""}}
	err := decodeError(req, 400, []byte(`{"message":"plain message"}`))
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %T", err)
	}
	if apiErr.Message != "plain message" {
		t.Fatalf("Message = %q, want it unchanged", apiErr.Message)
	}
}

// TestDecodeErrorNoRedactByDefault checks that a Request with no Redact
// value behaves exactly as before: a message is returned unchanged.
func TestDecodeErrorNoRedactByDefault(t *testing.T) {
	err := decodeError(Request{Operation: "Op", Method: http.MethodPost}, 400, []byte(`{"message":"plain message"}`))
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %T", err)
	}
	if apiErr.Message != "plain message" {
		t.Fatalf("Message = %q, want it unchanged", apiErr.Message)
	}
}

// TestDecodeErrorShortValueReplacesWholeMessage checks that a Redact value
// shorter than 8 characters, such as a short passphrase, never gets
// substituted in place: doing so could mangle an unrelated word that happens
// to contain it ("pass" inside "passphrase"). Instead the whole Message is
// replaced.
func TestDecodeErrorShortValueReplacesWholeMessage(t *testing.T) {
	req := Request{Operation: "Op", Method: http.MethodPost, Redact: []string{"pass"}}
	err := decodeError(req, 400, []byte(`{"message":"wrong passphrase given"}`))
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %T", err)
	}
	if strings.Contains(apiErr.Message, "phrase") {
		t.Fatalf("Message = %q, an in-place substitution corrupted an unrelated word", apiErr.Message)
	}
	if strings.Contains(apiErr.Message, "pass") {
		t.Fatalf("Message = %q, still holds the redacted value", apiErr.Message)
	}
	if !strings.Contains(apiErr.Message, "[redacted]") {
		t.Fatalf("Message = %q, want it to contain [redacted]", apiErr.Message)
	}
}

// TestDecodeErrorShortValueDoesNotMangleDigits checks that a single-character
// Redact value, such as a one-digit PIN, never corrupts an unrelated number
// that happens to contain it: "4" must not turn "400" into "[redacted]00".
func TestDecodeErrorShortValueDoesNotMangleDigits(t *testing.T) {
	req := Request{Operation: "Op", Method: http.MethodPost, Redact: []string{"4"}}
	err := decodeError(req, 400, []byte(`{"message":"status 400 returned"}`))
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %T", err)
	}
	if strings.Contains(apiErr.Message, "00 returned") {
		t.Fatalf("Message = %q, an in-place substitution mangled 400", apiErr.Message)
	}
}

// TestDecodeErrorShortValueLeavesNonMatchingMessageAlone checks that a short
// Redact value that never appears in Message or Code leaves both untouched:
// the whole-string fallback only fires on an actual match.
func TestDecodeErrorShortValueLeavesNonMatchingMessageAlone(t *testing.T) {
	req := Request{Operation: "Op", Method: http.MethodPost, Redact: []string{"pass"}}
	err := decodeError(req, 400, []byte(`{"code":"BadRequest","message":"unrelated failure"}`))
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %T", err)
	}
	if apiErr.Message != "unrelated failure" || apiErr.Code != "BadRequest" {
		t.Fatalf("Message = %q, Code = %q, want both unchanged", apiErr.Message, apiErr.Code)
	}
}

// TestDecodeErrorRedactsCRLFKeyLine checks that a multi-line Redact value
// using CRLF line endings still gets an individually quoted line redacted:
// splitting on "\n" leaves a trailing "\r" on every line but the last, which
// TrimSpace must strip before the line is matched.
func TestDecodeErrorRedactsCRLFKeyLine(t *testing.T) {
	const quotedLine = "MIIFAKELINEOFKEYMATERIALCRLF"
	key := strings.ReplaceAll(fakePEMPrivateKey(quotedLine), "\n", "\r\n")
	req := Request{Operation: "Op", Method: http.MethodPost, Redact: []string{key}}
	err := decodeError(req, 400, []byte(`{"message":"invalid line: `+quotedLine+`"}`))
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %T", err)
	}
	if strings.Contains(apiErr.Message, quotedLine) {
		t.Fatalf("Message = %q, still holds the quoted line", apiErr.Message)
	}
	if !strings.Contains(apiErr.Message, "[redacted]") {
		t.Fatalf("Message = %q, want it to contain [redacted]", apiErr.Message)
	}
}

// TestDecodeErrorWithholdsMessage checks that a non-empty
// Request.WithholdMessage replaces Message with that exact text on a failing
// response, regardless of what the body said, while Code still goes through
// the ordinary Redact path.
func TestDecodeErrorWithholdsMessage(t *testing.T) {
	req := Request{
		Operation:       "Op",
		Method:          http.MethodPost,
		WithholdMessage: "server message withheld",
		Redact:          []string{"secret-value"},
	}
	err := decodeError(req, 400, []byte(`{"code":"bad: secret-value","message":"rejected: secret-value"}`))
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %T", err)
	}
	if apiErr.Message != "server message withheld" {
		t.Fatalf("Message = %q, want the withheld text", apiErr.Message)
	}
	if strings.Contains(apiErr.Code, "secret-value") {
		t.Fatalf("Code = %q, still holds the redacted value", apiErr.Code)
	}
}

// TestDecodeErrorWithholdsMessageOnEveryStatus checks that WithholdMessage
// applies whatever the failing status is, not just one particular class.
func TestDecodeErrorWithholdsMessageOnEveryStatus(t *testing.T) {
	req := Request{Operation: "Op", Method: http.MethodPost, WithholdMessage: "withheld"}
	for _, status := range []int{400, 404, 409, 500, 503} {
		err := decodeError(req, status, []byte(`{"message":"server said something"}`))
		var apiErr *APIError
		if !errors.As(err, &apiErr) {
			t.Fatalf("status %d: expected *APIError, got %T", status, err)
		}
		if apiErr.Message != "withheld" {
			t.Fatalf("status %d: Message = %q, want the withheld text", status, apiErr.Message)
		}
		if apiErr.StatusCode != status {
			t.Fatalf("status %d: StatusCode = %d, want it preserved", status, apiErr.StatusCode)
		}
	}
}

// TestDecodeErrorNoWithholdByDefault checks that an empty WithholdMessage (the
// zero value) leaves Message exactly as Redact alone would have produced it,
// so every existing caller keeps its current behavior.
func TestDecodeErrorNoWithholdByDefault(t *testing.T) {
	err := decodeError(Request{Operation: "Op", Method: http.MethodPost}, 400, []byte(`{"message":"plain message"}`))
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %T", err)
	}
	if apiErr.Message != "plain message" {
		t.Fatalf("Message = %q, want it unchanged", apiErr.Message)
	}
}

// TestPostRetriedAfter429RedactsFinalMessage checks that Redact still applies
// to the final response's error after a 429 forced a retry: the redaction
// path must not be skipped just because the failing response was not the
// first attempt.
func TestPostRetriedAfter429RedactsFinalMessage(t *testing.T) {
	const secret = "correct-horse-battery-staple"
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"message":"rejected: ` + secret + `"}`))
	}))
	defer server.Close()

	c := New(Config{HTTPClient: server.Client(), RetryCount: 3, RetryInterval: time.Millisecond})
	err := c.DoJSON(context.Background(), Request{
		Operation: "Op",
		Method:    http.MethodPost,
		URL:       server.URL,
		OK:        []int{200},
		SkipAuth:  true,
		Redact:    []string{secret},
	}, nil)
	if err == nil {
		t.Fatal("DoJSON() error = nil, want the final 400")
	}
	if calls.Load() != 2 {
		t.Fatalf("calls = %d, want 2 (one 429, one 400)", calls.Load())
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %T", err)
	}
	if strings.Contains(apiErr.Message, secret) {
		t.Fatalf("Message = %q, still holds the secret after a 429 retry", apiErr.Message)
	}
	if !strings.Contains(apiErr.Message, "[redacted]") {
		t.Fatalf("Message = %q, want it to contain [redacted]", apiErr.Message)
	}
}

// stubRoundTripper fails the first RoundTrip with failErr, then returns a
// 200 with an empty JSON body, so a test can force one transport error
// before the real request would succeed.
type stubRoundTripper struct {
	calls   atomic.Int64
	failErr error
}

func (s *stubRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	if s.calls.Add(1) == 1 {
		return nil, s.failErr
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(`{}`)),
		Header:     make(http.Header),
	}, nil
}

func TestPostNotRetriedAfter502(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer server.Close()

	c := New(Config{HTTPClient: server.Client(), RetryCount: 3, RetryInterval: time.Millisecond})
	err := c.DoJSON(context.Background(), Request{
		Operation: "Op",
		Method:    http.MethodPost,
		URL:       server.URL,
		SkipAuth:  true,
	}, nil)
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %T (%v)", err, err)
	}
	if apiErr.StatusCode != http.StatusBadGateway {
		t.Fatalf("StatusCode = %d, want %d", apiErr.StatusCode, http.StatusBadGateway)
	}
	if calls.Load() != 1 {
		t.Fatalf("calls = %d, want 1", calls.Load())
	}
}

func TestIdempotentPostRetried(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer server.Close()

	c := New(Config{HTTPClient: server.Client(), RetryCount: 3, RetryInterval: time.Millisecond})
	err := c.DoJSON(context.Background(), Request{
		Operation:  "Op",
		Method:     http.MethodPost,
		URL:        server.URL,
		SkipAuth:   true,
		Idempotent: true,
	}, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if calls.Load() != 4 {
		t.Fatalf("calls = %d, want 4", calls.Load())
	}
}

func TestPostRetriedAfter429(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()

	c := New(Config{HTTPClient: server.Client(), RetryCount: 3, RetryInterval: time.Millisecond})
	err := c.DoJSON(context.Background(), Request{
		Operation: "Op",
		Method:    http.MethodPost,
		URL:       server.URL,
		OK:        []int{200},
		SkipAuth:  true,
	}, nil)
	if err != nil {
		t.Fatalf("DoJSON() error = %v", err)
	}
	if calls.Load() != 2 {
		t.Fatalf("calls = %d, want 2", calls.Load())
	}
}

func TestPutRetriedAfter502(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()

	c := New(Config{HTTPClient: server.Client(), RetryCount: 3, RetryInterval: time.Millisecond})
	err := c.DoJSON(context.Background(), Request{
		Operation: "Op",
		Method:    http.MethodPut,
		URL:       server.URL,
		OK:        []int{200},
		SkipAuth:  true,
	}, nil)
	if err != nil {
		t.Fatalf("DoJSON() error = %v", err)
	}
	if calls.Load() != 2 {
		t.Fatalf("calls = %d, want 2", calls.Load())
	}
}

func TestPostRetriedAfterDialError(t *testing.T) {
	rt := &stubRoundTripper{failErr: &net.OpError{Op: "dial", Err: errors.New("refused")}}
	c := New(Config{HTTPClient: &http.Client{Transport: rt}, RetryCount: 3, RetryInterval: time.Millisecond})
	err := c.DoJSON(context.Background(), Request{
		Operation: "Op",
		Method:    http.MethodPost,
		URL:       "http://vngcloud.invalid/",
		OK:        []int{200},
		SkipAuth:  true,
	}, nil)
	if err != nil {
		t.Fatalf("DoJSON() error = %v", err)
	}
	if rt.calls.Load() != 2 {
		t.Fatalf("calls = %d, want 2", rt.calls.Load())
	}

	rt2 := &stubRoundTripper{failErr: &net.OpError{Op: "read", Err: errors.New("connection reset")}}
	c2 := New(Config{HTTPClient: &http.Client{Transport: rt2}, RetryCount: 3, RetryInterval: time.Millisecond})
	err = c2.DoJSON(context.Background(), Request{
		Operation: "Op",
		Method:    http.MethodPost,
		URL:       "http://vngcloud.invalid/",
		OK:        []int{200},
		SkipAuth:  true,
	}, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if rt2.calls.Load() != 1 {
		t.Fatalf("calls = %d, want 1", rt2.calls.Load())
	}
}

func TestRetryableMatchesRetryRule(t *testing.T) {
	server502 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer server502.Close()
	server429 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server429.Close()

	cases := []struct {
		name   string
		client *Client
		method string
		url    string
		want   bool
	}{
		{"POST 502", New(Config{HTTPClient: server502.Client()}), http.MethodPost, server502.URL, false},
		{"GET 502", New(Config{HTTPClient: server502.Client()}), http.MethodGet, server502.URL, true},
		{"POST 429", New(Config{HTTPClient: server429.Client()}), http.MethodPost, server429.URL, true},
	}
	for _, tc := range cases {
		err := tc.client.DoJSON(context.Background(), Request{
			Operation: "Op",
			Method:    tc.method,
			URL:       tc.url,
			SkipAuth:  true,
		}, nil)
		var apiErr *APIError
		if !errors.As(err, &apiErr) {
			t.Fatalf("%s: expected *APIError, got %T", tc.name, err)
		}
		if apiErr.Retryable != tc.want {
			t.Fatalf("%s: Retryable = %v, want %v", tc.name, apiErr.Retryable, tc.want)
		}
	}

	rtRead := &stubRoundTripper{failErr: &net.OpError{Op: "read", Err: errors.New("connection reset")}}
	cRead := New(Config{HTTPClient: &http.Client{Transport: rtRead}})
	err := cRead.DoJSON(context.Background(), Request{Operation: "Op", Method: http.MethodPost, URL: "http://vngcloud.invalid/", SkipAuth: true}, nil)
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("POST read error: expected *APIError, got %T", err)
	}
	if apiErr.Retryable {
		t.Fatal("POST read error: Retryable = true, want false")
	}

	rtDial := &stubRoundTripper{failErr: &net.OpError{Op: "dial", Err: errors.New("refused")}}
	cDial := New(Config{HTTPClient: &http.Client{Transport: rtDial}})
	err = cDial.DoJSON(context.Background(), Request{Operation: "Op", Method: http.MethodPost, URL: "http://vngcloud.invalid/", SkipAuth: true}, nil)
	if !errors.As(err, &apiErr) {
		t.Fatalf("POST dial error: expected *APIError, got %T", err)
	}
	if !apiErr.Retryable {
		t.Fatal("POST dial error: Retryable = false, want true")
	}
}

// failTokenSource fails the test if Token or Invalidate is ever called, so a
// test using it proves a request never consulted a credentials provider.
type failTokenSource struct{ t *testing.T }

func (f failTokenSource) Token(context.Context) (Token, error) {
	f.t.Helper()
	f.t.Fatal("token source called for a request with SkipAuth set")
	return Token{}, nil
}

func (f failTokenSource) Invalidate(string) {
	f.t.Helper()
	f.t.Fatal("token source invalidated for a request with SkipAuth set")
}

func TestDoRawSkipsAuthAndReturnsBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "" {
			t.Fatalf("Authorization = %q, want none", got)
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte("<html>ok</html>"))
	}))
	defer server.Close()

	c := New(Config{HTTPClient: server.Client(), TokenSource: failTokenSource{t}})
	status, contentType, body, err := c.DoRaw(context.Background(), Request{
		Operation: "cdn.ListIPRanges",
		Method:    http.MethodGet,
		URL:       server.URL,
		SkipAuth:  true,
	})
	if err != nil {
		t.Fatalf("DoRaw() error = %v", err)
	}
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	if contentType != "text/html; charset=utf-8" {
		t.Fatalf("contentType = %q", contentType)
	}
	if string(body) != "<html>ok</html>" {
		t.Fatalf("body = %q", body)
	}
}

func TestDoRawNonOKStatusIsNotAnError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("not found"))
	}))
	defer server.Close()

	c := New(Config{HTTPClient: server.Client()})
	status, _, body, err := c.DoRaw(context.Background(), Request{
		Operation: "Op",
		Method:    http.MethodGet,
		URL:       server.URL,
		SkipAuth:  true,
	})
	if err != nil {
		t.Fatalf("DoRaw() error = %v, want nil for a non-2xx status", err)
	}
	if status != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", status)
	}
	if string(body) != "not found" {
		t.Fatalf("body = %q", body)
	}
}

func TestDoRawMaxBodyExactLimitSucceeds(t *testing.T) {
	payload := strings.Repeat("a", 10)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(payload))
	}))
	defer server.Close()

	c := New(Config{HTTPClient: server.Client()})
	_, _, body, err := c.DoRaw(context.Background(), Request{
		Operation: "Op",
		Method:    http.MethodGet,
		URL:       server.URL,
		SkipAuth:  true,
		MaxBody:   int64(len(payload)),
	})
	if err != nil {
		t.Fatalf("DoRaw() error = %v, want nil for a body exactly at MaxBody", err)
	}
	if string(body) != payload {
		t.Fatalf("body = %q", body)
	}
}

func TestDoRawMaxBodyExceededFails(t *testing.T) {
	payload := strings.Repeat("a", 11)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(payload))
	}))
	defer server.Close()

	c := New(Config{HTTPClient: server.Client()})
	_, _, _, err := c.DoRaw(context.Background(), Request{
		Operation: "Op",
		Method:    http.MethodGet,
		URL:       server.URL,
		SkipAuth:  true,
		MaxBody:   10,
	})
	if !errors.Is(err, ErrBodyTooLarge) {
		t.Fatalf("DoRaw() error = %v, want ErrBodyTooLarge", err)
	}
}

func TestDoRawStripsCallerCookieJar(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := r.Cookie("session"); err == nil {
			t.Fatal("Cookie header reached the server")
		}
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New() error = %v", err)
	}
	serverURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("url.Parse() error = %v", err)
	}
	jar.SetCookies(serverURL, []*http.Cookie{{Name: "session", Value: "secret", HttpOnly: true, SameSite: http.SameSiteLaxMode}}) //nolint:gosec // Secure must stay false: the test server is plain HTTP, and the jar would never attach a Secure cookie to it

	httpClient := &http.Client{Transport: server.Client().Transport, Jar: jar}
	c := New(Config{HTTPClient: httpClient})
	if _, _, _, err := c.DoRaw(context.Background(), Request{
		Operation: "Op",
		Method:    http.MethodGet,
		URL:       server.URL,
		SkipAuth:  true,
	}); err != nil {
		t.Fatalf("DoRaw() error = %v", err)
	}
}

func TestDoRawSameHostRedirectSucceeds(t *testing.T) {
	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	defer server.Close()
	mux.HandleFunc("/start", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, server.URL+"/final", http.StatusFound)
	})
	mux.HandleFunc("/final", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("final"))
	})

	c := New(Config{HTTPClient: server.Client()})
	status, _, body, err := c.DoRaw(context.Background(), Request{
		Operation: "Op",
		Method:    http.MethodGet,
		URL:       server.URL + "/start",
		SkipAuth:  true,
	})
	if err != nil {
		t.Fatalf("DoRaw() error = %v", err)
	}
	if status != http.StatusOK || string(body) != "final" {
		t.Fatalf("status = %d, body = %q", status, body)
	}
}

func TestDoRawCrossHostRedirectFails(t *testing.T) {
	var targetHits atomic.Int64
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetHits.Add(1)
		_, _ = w.Write([]byte("final"))
	}))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/", http.StatusFound)
	}))
	defer source.Close()

	c := New(Config{HTTPClient: source.Client()})
	_, _, _, err := c.DoRaw(context.Background(), Request{
		Operation: "Op",
		Method:    http.MethodGet,
		URL:       source.URL,
		SkipAuth:  true,
	})
	if err == nil {
		t.Fatal("expected error for a cross-host redirect")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %T", err)
	}
	if targetHits.Load() != 0 {
		t.Fatalf("redirect target was hit %d times, want 0: a refused redirect must never be followed", targetHits.Load())
	}
}

// TestDoRawCallerCheckRedirectRunsAfterSameHost checks the design's
// composition order in rawClient: the same-host rule runs first, and only a
// redirect that passes it reaches a caller-supplied CheckRedirect. This
// redirect is same-host, so it would succeed if the caller's CheckRedirect
// were skipped; the caller's refusal proves it ran.
func TestDoRawCallerCheckRedirectRunsAfterSameHost(t *testing.T) {
	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	defer server.Close()
	mux.HandleFunc("/start", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, server.URL+"/final", http.StatusFound)
	})
	var finalHits atomic.Int64
	mux.HandleFunc("/final", func(w http.ResponseWriter, r *http.Request) {
		finalHits.Add(1)
		_, _ = w.Write([]byte("final"))
	})

	httpClient := &http.Client{
		Transport: server.Client().Transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return errors.New("caller refuses every redirect")
		},
	}
	c := New(Config{HTTPClient: httpClient})
	_, _, _, err := c.DoRaw(context.Background(), Request{
		Operation: "Op",
		Method:    http.MethodGet,
		URL:       server.URL + "/start",
		SkipAuth:  true,
	})
	if err == nil {
		t.Fatal("expected error from the caller-supplied CheckRedirect")
	}
	if finalHits.Load() != 0 {
		t.Fatalf("redirect target was hit %d times, want 0: the caller's CheckRedirect must have run", finalHits.Load())
	}
}

// endlessReader yields 'a' forever. A test serving it as a response body,
// with a small Request.MaxBody, proves the transport bounds the read by
// streaming (io.LimitReader) rather than by measuring one large but finite
// byte slice after reading it all into memory.
type endlessReader struct{}

func (endlessReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 'a'
	}
	return len(p), nil
}

// TestDoRawMaxBodyExceededPreservesStatus checks that an oversized body on a
// non-200 response still reports that status alongside ErrBodyTooLarge,
// instead of the 0 a genuine network failure carries: the caller decides
// what the status means, DoRaw does not discard it.
func TestDoRawMaxBodyExceededPreservesStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.Copy(w, endlessReader{})
	}))
	defer server.Close()

	c := New(Config{HTTPClient: server.Client()})
	status, _, _, err := c.DoRaw(context.Background(), Request{
		Operation: "Op",
		Method:    http.MethodGet,
		URL:       server.URL,
		SkipAuth:  true,
		MaxBody:   1024,
	})
	if !errors.Is(err, ErrBodyTooLarge) {
		t.Fatalf("DoRaw() error = %v, want ErrBodyTooLarge", err)
	}
	if status != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", status, http.StatusServiceUnavailable)
	}
}

func TestDecodeErrorEnvelopeCode(t *testing.T) {
	req := Request{Operation: "Op", Method: http.MethodGet}
	cases := []struct {
		name string
		body string
		want string
	}{
		{"numeric code", `{"code":400,"message":"m"}`, "400"},
		{"null code", `{"code":null,"message":"m"}`, ""},
		{"string code", `{"code":"X","message":"m"}`, "X"},
	}
	for _, tc := range cases {
		err := decodeError(req, 400, []byte(tc.body))
		var apiErr *APIError
		if !errors.As(err, &apiErr) {
			t.Fatalf("%s: expected *APIError, got %T", tc.name, err)
		}
		if apiErr.Code != tc.want {
			t.Fatalf("%s: Code = %q, want %q", tc.name, apiErr.Code, tc.want)
		}
	}

	err := decodeError(req, 403, []byte(`[{"code":"IAM_PERMISSION_DENIED","message":"IAM denied action"}]`))
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("array form: expected *APIError, got %T", err)
	}
	if apiErr.Code != "IAM_PERMISSION_DENIED" {
		t.Fatalf("array form: Code = %q", apiErr.Code)
	}
}
