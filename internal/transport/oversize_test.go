package transport_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/endpoints"
	"danny.vn/vngcloud/internal/transport"
)

type oversizeTokens struct {
	calls         atomic.Int32
	invalidations atomic.Int32
}

type redactionMemoryPeer struct{ payload, credential string }

func (p redactionMemoryPeer) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Header.Get("Authorization") != "Bearer "+p.credential {
		return nil, errors.New("incorrect synthetic token")
	}
	return &http.Response{StatusCode: http.StatusForbidden, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(p.payload)), Request: req}, nil
}

func TestRedactionBoundedHTTPErrorCost(t *testing.T) {
	const capBytes = 4 << 20
	token := strings.Repeat("a", 128<<10)
	payload := `{"message":"` + strings.Repeat("a", capBytes-64) + `"}`
	httpClient := &http.Client{Transport: redactionMemoryPeer{payload: payload, credential: token}}
	cfg, err := vngcloud.NewConfig(vngcloud.WithRegion("hcm-3"), vngcloud.WithStaticToken(token), vngcloud.WithHTTPClient(httpClient), vngcloud.WithRetry(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	started := time.Now()
	status, err := core.ClientOf(cfg).DoJSONStatus(ctx, transport.Request{URL: "http://synthetic.invalid/", MaxBody: capBytes, Sensitive: true}, nil)
	elapsed := time.Since(started)
	t.Logf("body=%d token=%d elapsed=%s context=%v", len(payload), len(token), elapsed, ctx.Err())
	var api *vngcloud.APIError
	if status != http.StatusForbidden || !errors.As(err, &api) || api.StatusCode != status || api.Message != "[redacted]" || !errors.Is(err, vngcloud.ErrPermission) {
		t.Fatal("redaction changed the fixed HTTP error identity")
	}
	// Allow one second beyond the deadline for scheduling and race instrumentation.
	if elapsed > 1500*time.Millisecond {
		t.Fatal("bounded response redaction ran past the deadline margin")
	}
}

func (s *oversizeTokens) Token(context.Context) (transport.Token, error) {
	s.calls.Add(1)
	return transport.Token{AccessToken: "synthetic-oversize-token", ExpiresAt: time.Now().Add(time.Hour)}, nil
}

func (s *oversizeTokens) Invalidate(string) { s.invalidations.Add(1) }

func TestOversizedJSONPreservesStatusAndClassification(t *testing.T) {
	for _, sdk := range []bool{false, true} {
		for _, status := range []int{200, 201, 202, 401, 403, 404, 429, 500, 503} {
			t.Run(fmt.Sprintf("sdk=%t/%d", sdk, status), func(t *testing.T) {
				var attempts atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					attempts.Add(1)
					if r.Header.Get("Authorization") != "Bearer synthetic-oversize-token" {
						t.Error("missing authentication")
					}
					w.WriteHeader(status)
					_, _ = w.Write([]byte(`{"code":"peer-body-canary","message":"synthetic-oversize-token peer-body-canary","extra":"` + strings.Repeat("x", 1024) + `"}`))
				}))
				defer server.Close()
				var logs bytes.Buffer
				captures := 0
				source := new(oversizeTokens)
				client := transport.New(transport.Config{HTTPClient: server.Client(), TokenSource: source, RetryCount: 2, RetryInterval: time.Nanosecond, Capture: func(transport.Capture) { captures++ }, Logger: slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))})
				req := transport.Request{Operation: "test.Oversize", URL: server.URL, MaxBody: 128}
				out := json.RawMessage(`{"unchanged":true}`)
				var got int
				var err error
				if sdk {
					got, err = core.NewTestClient("hcm-3", "", endpoints.Set{}, client).DoJSONStatus(context.Background(), req, &out)
				} else {
					got, err = client.DoJSONStatus(context.Background(), req, &out)
				}
				if got != status {
					t.Errorf("status = %d, want %d", got, status)
				}
				if err == nil {
					t.Fatal("oversized body was accepted")
				}
				switch {
				case status/100 == 2:
					if !errors.Is(err, transport.ErrBodyTooLarge) || err.Error() != transport.ErrBodyTooLarge.Error() {
						t.Errorf("oversized success error = %v, want ErrBodyTooLarge", err)
					}
				case sdk:
					var api *vngcloud.APIError
					if !errors.As(err, &api) {
						t.Errorf("HTTP failure = %T, want public APIError", err)
					} else if api.StatusCode != status || api.Operation != req.Operation || api.Code != core.ResolvedCode(status, "") || api.Message != http.StatusText(status) || api.Retryable != reqRetryable(status) {
						t.Errorf("public HTTP classification = %+v", api)
					}
					for sentinelStatus, sentinel := range map[int]error{401: vngcloud.ErrAuth, 403: vngcloud.ErrPermission, 404: vngcloud.ErrNotFound, 429: vngcloud.ErrRateLimited} {
						if errors.Is(err, sentinel) != (status == sentinelStatus) {
							t.Errorf("public sentinel for %d did not match HTTP status", sentinelStatus)
						}
					}
				default:
					var api *transport.APIError
					if !errors.As(err, &api) {
						t.Errorf("HTTP failure = %T, want transport APIError", err)
					} else if api.StatusCode != status || api.Operation != req.Operation || api.Code != "" || api.Message != http.StatusText(status) || api.Retryable != reqRetryable(status) {
						t.Errorf("transport HTTP classification = %+v", api)
					}
				}
				if status >= 400 && errors.Is(err, transport.ErrBodyTooLarge) {
					t.Error("HTTP failure still matches the successful-body size sentinel")
				}
				for _, text := range []string{err.Error(), fmt.Sprintf("%+v", err), logs.String()} {
					if strings.Contains(text, "peer-body-canary") || strings.Contains(text, "synthetic-oversize-token") {
						t.Error("oversized peer body leaked into error or log")
					}
				}
				if string(out) != `{"unchanged":true}` || captures != 0 || attempts.Load() != 1 || source.calls.Load() != 1 || source.invalidations.Load() != 0 {
					t.Error("oversized response was decoded, captured, retried, or refreshed")
				}
			})
		}
	}
}

func reqRetryable(status int) bool { return status == 429 || status == 503 }

type countedOversizeBody struct {
	reader *strings.Reader
	read   int
	closed bool
}

func (b *countedOversizeBody) Read(p []byte) (int, error) {
	n, err := b.reader.Read(p)
	b.read += n
	return n, err
}

func (b *countedOversizeBody) Close() error {
	b.closed = true
	return nil
}

type oversizeRoundTripper struct {
	status int
	body   io.ReadCloser
}

func (rt oversizeRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: rt.status, Header: make(http.Header), Body: rt.body, Request: req}, nil
}

func TestOversizedResponseReadBound(t *testing.T) {
	for _, raw := range []bool{false, true} {
		for _, status := range []int{200, 401, 403, 404, 429, 500} {
			t.Run(fmt.Sprintf("raw=%t/%d", raw, status), func(t *testing.T) {
				body := &countedOversizeBody{reader: strings.NewReader(strings.Repeat("peer-body-canary", 1024))}
				client := transport.New(transport.Config{HTTPClient: &http.Client{Transport: oversizeRoundTripper{status: status, body: body}}})
				req := transport.Request{Operation: "test.Oversize", URL: "http://example.invalid/", SkipAuth: true, MaxBody: 128}
				var got int
				var err error
				if raw {
					var returned []byte
					got, _, returned, err = client.DoRaw(context.Background(), req)
					if returned != nil || !errors.Is(err, transport.ErrBodyTooLarge) {
						t.Error("raw overflow returned a partial body or lost the size sentinel")
					}
				} else {
					got, err = client.DoJSONStatus(context.Background(), req, nil)
				}
				if got != status || err == nil {
					t.Errorf("status = %d, error = %v, want status %d and error", got, err, status)
				}
				if body.read != int(req.MaxBody)+1 || !body.closed {
					t.Errorf("read %d bytes, closed = %t, want one overflow byte and closed body", body.read, body.closed)
				}
				if err != nil && strings.Contains(err.Error(), "peer-body-canary") {
					t.Error("oversized peer body leaked")
				}
			})
		}
	}
}

func TestJSONBodyExactlyAtLimit(t *testing.T) {
	const payload = `{"message":"fits"}`
	body := &countedOversizeBody{reader: strings.NewReader(payload)}
	client := transport.New(transport.Config{HTTPClient: &http.Client{Transport: oversizeRoundTripper{status: 200, body: body}}})
	var out struct{ Message string }
	status, err := client.DoJSONStatus(context.Background(), transport.Request{URL: "http://example.invalid/", SkipAuth: true, MaxBody: int64(len(payload))}, &out)
	if err != nil || status != 200 || out.Message != "fits" || body.read != len(payload) || !body.closed {
		t.Fatalf("exact-limit response: status = %d, error = %v, bytes = %d, closed = %t", status, err, body.read, body.closed)
	}
}

func TestOversizedAcceptedErrorStatusStillClassifies(t *testing.T) {
	for _, status := range []int{401, 403, 404, 429, 500} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			body := &countedOversizeBody{reader: strings.NewReader(strings.Repeat("peer-body-canary", 1024))}
			client := transport.New(transport.Config{HTTPClient: &http.Client{Transport: oversizeRoundTripper{status: status, body: body}}})
			req := transport.Request{Operation: "test.Oversize", URL: "http://example.invalid/", APIKey: "synthetic-api-key", OK: []int{status}, MaxBody: 128, Once: true, WithholdMessage: "response withheld"}
			got, err := client.DoJSONStatus(context.Background(), req, nil)
			var api *transport.APIError
			if got != status || !errors.As(err, &api) {
				t.Fatalf("status = %d, error = %v, want HTTP APIError for %d", got, err, status)
			}
			if api.Message != req.WithholdMessage || api.StatusCode != status || api.Retryable != (status == 429) {
				t.Errorf("oversized accepted status lost privacy or Once policy: %+v", api)
			}
		})
	}
}

type failedResponseBody struct {
	readErr  error
	closeErr error
}

func (b failedResponseBody) Read([]byte) (int, error) {
	if b.readErr != nil {
		return 0, b.readErr
	}
	return 0, io.EOF
}

func (b failedResponseBody) Close() error { return b.closeErr }

func TestJSONReadAndCloseFailuresKeepStatusZero(t *testing.T) {
	for _, tc := range []struct {
		name string
		body failedResponseBody
		want error
	}{
		{"read", failedResponseBody{readErr: io.ErrUnexpectedEOF}, io.ErrUnexpectedEOF},
		{"close", failedResponseBody{closeErr: io.ErrClosedPipe}, io.ErrClosedPipe},
	} {
		for _, status := range []int{200, 401, 500} {
			t.Run(fmt.Sprintf("%s/%d", tc.name, status), func(t *testing.T) {
				client := transport.New(transport.Config{HTTPClient: &http.Client{Transport: oversizeRoundTripper{status: status, body: tc.body}}})
				got, err := client.DoJSONStatus(context.Background(), transport.Request{URL: "http://example.invalid/", SkipAuth: true, MaxBody: 128}, nil)
				var api *transport.APIError
				if got != 0 || !errors.Is(err, tc.want) || !errors.As(err, &api) || api.StatusCode != 0 {
					t.Fatalf("status = %d, error = %v, want incomplete response with status 0", got, err)
				}
			})
		}
	}
}

func TestOversizedCallerCanPreferBodyLimitError(t *testing.T) {
	for _, sdk := range []bool{false, true} {
		for _, status := range []int{200, 201, 401, 403, 404, 429, 500, 503} {
			t.Run(fmt.Sprintf("sdk=%t/%d", sdk, status), func(t *testing.T) {
				body := &countedOversizeBody{reader: strings.NewReader(strings.Repeat("peer-body-canary", 1024))}
				client := transport.New(transport.Config{HTTPClient: &http.Client{Transport: oversizeRoundTripper{status: status, body: body}}, RetryCount: 2, RetryInterval: time.Nanosecond})
				req := transport.Request{Operation: "test.Oversize", URL: "http://example.invalid/", APIKey: "synthetic-api-key", MaxBody: 128, BodyLimitOverridesStatus: true}
				var got int
				var err error
				if sdk {
					got, err = core.NewTestClient("hcm-3", "", endpoints.Set{}, client).DoJSONStatus(context.Background(), req, nil)
				} else {
					got, err = client.DoJSONStatus(context.Background(), req, nil)
				}
				if got != status || !errors.Is(err, transport.ErrBodyTooLarge) || err.Error() != transport.ErrBodyTooLarge.Error() {
					t.Fatalf("status = %d, error = %v, want status %d and ErrBodyTooLarge", got, err, status)
				}
				var api *vngcloud.APIError
				var private *transport.APIError
				if errors.As(err, &api) || errors.As(err, &private) {
					t.Error("body-limit override still returned an HTTP APIError")
				}
				if body.read != int(req.MaxBody)+1 || !body.closed {
					t.Errorf("read %d bytes, closed = %t, want bounded read and closed body", body.read, body.closed)
				}
			})
		}
	}
}
