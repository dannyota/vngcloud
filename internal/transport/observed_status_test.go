package transport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type observedBody struct {
	io.Reader
	readErr, closeErr error
}

func (b observedBody) Read(p []byte) (int, error) {
	if b.readErr != nil {
		return 0, b.readErr
	}
	return b.Reader.Read(p)
}
func (b observedBody) Close() error { return b.closeErr }

type observedRoundTripper func(*http.Request) (*http.Response, error)

func (f observedRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestObservedStatusBodyFailures(t *testing.T) {
	for _, status := range []int{201, 204, 404, 401, 403, 429, 503} {
		for _, closeFailure := range []bool{false, true} {
			for _, sensitive := range []bool{false, true} {
				for _, keep := range []bool{false, true} {
					t.Run(fmt.Sprintf("%d/close=%t/sensitive=%t/keep=%t", status, closeFailure, sensitive, keep), func(t *testing.T) {
						calls := 0
						cause := fmt.Errorf("synthetic-bearer-canary: %w", context.Canceled)
						body := observedBody{Reader: strings.NewReader("synthetic-body-canary")}
						if closeFailure {
							body.closeErr = cause
						} else {
							body.readErr = cause
						}
						c := New(Config{RetryCount: 3, HTTPClient: &http.Client{Transport: observedRoundTripper(func(*http.Request) (*http.Response, error) {
							calls++
							return &http.Response{StatusCode: status, Header: make(http.Header), Body: body}, nil
						})}})
						notSent := true
						got, err := c.DoJSONStatus(context.Background(), Request{Operation: "test.Read", URL: "http://localhost/", OK: []int{status}, APIKey: "synthetic-bearer-canary", NotSent: &notSent, Sensitive: sensitive, KeepObservedStatus: keep}, nil)
						var api *APIError
						if !errors.As(err, &api) {
							t.Fatalf("error = %v, want APIError", err)
						}
						wantStatus := 0
						if keep {
							wantStatus = status
						}
						if got != wantStatus || api.StatusCode != wantStatus || api.KeptObservedStatus != keep {
							t.Errorf("statuses = %d, %d, marker = %t; want %d, %t", got, api.StatusCode, api.KeptObservedStatus, wantStatus, keep)
						}
						if !errors.Is(err, context.Canceled) {
							t.Error("cancellation identity lost")
						}
						var safe *safeNetworkError
						if !errors.As(err, &safe) {
							t.Error("sanitized cause lost")
						}
						if notSent || api.Retryable || calls != 1 {
							t.Errorf("NotSent=%t Retryable=%t calls=%d", notSent, api.Retryable, calls)
						}
						if strings.Contains(fmt.Sprintf("%+v %+v", api, safe), "canary") {
							t.Error("error leaked response or credential")
						}
					})
				}
			}
		}
	}
}

// The response decoder requires an object; null must reach that decoder.
type observedObject struct {
	Value   string `json:"value"`
	Decodes int    `json:"-"`
}

func (o *observedObject) UnmarshalJSON(body []byte) error {
	o.Decodes++
	if len(body) == 0 || body[0] != '{' {
		return errors.New("object required")
	}
	type wire observedObject
	return json.Unmarshal(body, (*wire)(o))
}

func TestObservedEmptyBody(t *testing.T) {
	for _, tc := range []struct {
		name, body                string
		allow, wantEmpty, wantErr bool
		decodes                   int
	}{
		{"empty allowed", "", true, true, false, 0},
		{"object", "{}", true, false, false, 1},
		{"value", `{"value":"decoded"}`, true, false, false, 1},
		{"whitespace", " \n", true, false, true, 0},
		{"null", "null", true, false, true, 1},
		{"array", "[]", true, false, true, 1},
		{"malformed", "{synthetic-body-canary", true, false, true, 0},
		{"empty refused", "", false, false, true, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(201); _, _ = io.WriteString(w, tc.body) }))
			defer server.Close()
			c := New(Config{HTTPClient: server.Client()})
			empty := true
			out := observedObject{Value: "untouched"}
			status, err := c.DoJSONStatus(context.Background(), Request{URL: server.URL, OK: []int{201}, StrictJSON: true, Sensitive: true, AllowEmptyBody: tc.allow, BodyWasEmpty: &empty}, &out)
			if (err != nil) != tc.wantErr || status != 201 || empty != tc.wantEmpty || out.Decodes != tc.decodes {
				t.Fatalf("status=%d error=%v empty=%t decodes=%d", status, err, empty, out.Decodes)
			}
			if tc.wantEmpty && out.Value != "untouched" {
				t.Error("empty body changed target")
			}
			if tc.body == `{"value":"decoded"}` && out.Value != "decoded" {
				t.Error("object was not decoded")
			}
			if tc.wantErr && err.Error() != "response failed to decode; body withheld" {
				t.Errorf("error = %v", err)
			}
		})
	}
}

func TestObservedEmptyFlagReset(t *testing.T) {
	calls := 0
	c := New(Config{HTTPClient: &http.Client{Transport: observedRoundTripper(func(*http.Request) (*http.Response, error) {
		calls++
		status := 201
		if calls == 2 {
			status = 404
		}
		return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(""))}, nil
	})}})
	empty := false
	req := Request{URL: "http://localhost/", OK: []int{201}, StrictJSON: true, AllowEmptyBody: true, BodyWasEmpty: &empty}
	var out observedObject
	if err := c.DoJSON(context.Background(), req, &out); err != nil || !empty {
		t.Fatalf("first call: empty=%t error=%v", empty, err)
	}
	if err := c.DoJSON(context.Background(), req, &out); err == nil || empty {
		t.Fatalf("failed status: empty=%t error=%v", empty, err)
	}
	empty = true
	req.URL = ":invalid"
	if err := c.DoJSON(context.Background(), req, &out); err == nil || empty {
		t.Fatalf("local failure: empty=%t error=%v", empty, err)
	}
}

func TestObservedOverflowUnchanged(t *testing.T) {
	for _, status := range []int{201, 404} {
		c := New(Config{HTTPClient: &http.Client{Transport: observedRoundTripper(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: status, Header: make(http.Header), Body: observedBody{Reader: strings.NewReader("overflow"), closeErr: context.Canceled}}, nil
		})}})
		got, err := c.DoJSONStatus(context.Background(), Request{URL: "http://localhost/", OK: []int{201}, MaxBody: 1, KeepObservedStatus: true}, nil)
		if got != status || errors.Is(err, context.Canceled) {
			t.Fatalf("status=%d error=%v", got, err)
		}
		if status == 201 && !errors.Is(err, ErrBodyTooLarge) {
			t.Errorf("error = %v, want overflow", err)
		}
		var api *APIError
		if status == 404 && (!errors.As(err, &api) || api.StatusCode != 404 || api.KeptObservedStatus) {
			t.Errorf("error = %v, want classified 404", err)
		}
	}
}

func TestObservedPreStatusUnchanged(t *testing.T) {
	c := New(Config{HTTPClient: &http.Client{Transport: observedRoundTripper(func(*http.Request) (*http.Response, error) {
		return nil, context.Canceled
	})}})
	got, err := c.DoJSONStatus(context.Background(), Request{URL: "http://localhost/", KeepObservedStatus: true}, nil)
	var api *APIError
	if got != 0 || !errors.As(err, &api) || api.StatusCode != 0 || api.KeptObservedStatus || !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-status failure changed: status=%d error=%v", got, err)
	}
}
