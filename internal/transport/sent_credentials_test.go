package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestRedactionMatchesOriginalText(t *testing.T) {
	const first = "synthetic-shared-token"
	const second = first + "-SECRET-TAIL"
	for _, tc := range []struct {
		name, text, want string
		values           []string
	}{
		{"prefix", "bad " + second + "; " + first, "bad [redacted]; [redacted]", []string{first, second}},
		{"staggered", "abcdefghijklmnopqr", "[redacted]", []string{"abcdefghijk", "hijklmnopqr"}},
		{"self overlap", "aaaaaaaaaa", "[redacted]", []string{"aaaaaaaa", "aaaaaaaaa"}},
		{"escaped", "bad " + jsonEscapedForm(`synthetic-"token-SECRET-TAIL`), "bad [redacted]", []string{`synthetic-"token`, `synthetic-"token-SECRET-TAIL`}},
		{"trimmed line", "bad " + second, "bad [redacted]", []string{"header\n  " + first + "\nfooter", "header\n  " + second + "\nfooter"}},
		{"short", "bad tok-SECRET-TAIL", "[redacted]", []string{"tok", "tok-SECRET-TAIL"}},
		{"adjacent", first + second, "[redacted][redacted]", []string{first, second}},
	} {
		for _, reverse := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/reverse=%t", tc.name, reverse), func(t *testing.T) {
				values := slices.Clone(tc.values)
				if reverse {
					slices.Reverse(values)
				}
				if got := RedactValues(tc.text, values...); got != tc.want {
					t.Errorf("redacted text = %q, want %q", got, tc.want)
				}
			})
		}
	}
}

type overlapTokens struct {
	values []string
	calls  atomic.Int32
}

func (s *overlapTokens) Token(context.Context) (Token, error) {
	i := min(int(s.calls.Add(1))-1, len(s.values)-1)
	return Token{AccessToken: s.values[i], ExpiresAt: time.Now().Add(time.Hour)}, nil
}

func (*overlapTokens) Invalidate(string) {}

func TestCollectedOverlappingCredentials(t *testing.T) {
	const first = "synthetic-shared-token"
	const second = first + "-SECRET-TAIL"
	for _, reverse := range []bool{false, true} {
		for _, status := range []int{200, 400} {
			t.Run(fmt.Sprintf("reverse=%t/%d", reverse, status), func(t *testing.T) {
				values := []string{first, second}
				if reverse {
					slices.Reverse(values)
				}
				var attempts atomic.Int32
				var captures [][]byte
				var logs bytes.Buffer
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if attempts.Add(1) == 1 {
						w.WriteHeader(http.StatusUnauthorized)
						return
					}
					w.WriteHeader(status)
					_, _ = fmt.Fprintf(w, `{"code":%q,"message":%q,"%s":%q}`, second, second, second, second)
				}))
				defer server.Close()
				client := New(Config{HTTPClient: server.Client(), TokenSource: &overlapTokens{values: values}, Capture: func(c Capture) { captures = append(captures, c.Body) }, Logger: slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))})
				var sent []string
				var out struct{ Code, Message string }
				err := client.DoJSON(context.Background(), Request{URL: server.URL + "/" + second, SentCredentials: &sent}, &out)
				if !slices.Equal(sent, values) || attempts.Load() != 2 {
					t.Fatal("did not collect both credentials across refresh")
				}
				switch {
				case status == 400:
					var api *APIError
					if !errors.As(err, &api) {
						t.Fatal("missing APIError")
					}
					out.Code, out.Message = api.Code, api.Message
				case err != nil:
					t.Fatal(err)
				default:
					out.Code, out.Message = RedactErrorText(out.Code, sent...), RedactErrorText(out.Message, sent...)
				}
				if out.Code != redactedText || out.Message != redactedText {
					t.Error("error or envelope retained part of an overlapping credential")
				}
				if len(captures) == 0 {
					t.Fatal("missing response capture")
				}
				for _, body := range captures {
					if strings.Contains(string(body), "SECRET-TAIL") {
						t.Error("capture retained a credential suffix")
					}
				}
				loggedRetry := false
				for _, line := range strings.Split(strings.TrimSpace(logs.String()), "\n") {
					var record struct {
						Status int    `json:"status"`
						Path   string `json:"path"`
					}
					if json.Unmarshal([]byte(line), &record) != nil {
						t.Fatal("invalid log record")
					}
					// The retry is the first attempt that knows both credentials.
					if record.Status == status {
						loggedRetry = true
						if record.Path != "/[redacted]" {
							t.Error("debug path retained a credential suffix after refresh")
						}
					}
				}
				if !loggedRetry {
					t.Fatal("missing retry debug record")
				}
			})
		}
	}
}

func TestSentCredentialsRedactEarlierAttempts(t *testing.T) {
	for _, mode := range []string{"refresh", "5xx", "api-key"} {
		for _, status := range []int{http.StatusOK, http.StatusBadRequest} {
			t.Run(fmt.Sprintf("%s/%d", mode, status), func(t *testing.T) {
				var client *Client
				var sent []string
				var captured [][]byte
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					sent = append(sent, strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
					if len(sent) == 1 {
						if mode == "refresh" {
							w.WriteHeader(http.StatusUnauthorized)
							return
						}
						if mode == "5xx" {
							client.mu.Lock()
							client.token = Token{AccessToken: "synthetic-token-2", ExpiresAt: time.Now().Add(time.Hour)}
							client.mu.Unlock()
						}
					}
					if mode != "refresh" && len(sent) < 3 {
						w.WriteHeader(http.StatusServiceUnavailable)
						return
					}
					w.WriteHeader(status)
					_, _ = fmt.Fprintf(w, `{"code":%q,"message":%q}`, sent[0], "bad "+strings.Join(sent, " "))
				}))
				defer server.Close()
				client = New(Config{HTTPClient: server.Client(), TokenSource: &longTokens{}, RetryCount: 2, RetryInterval: time.Nanosecond, Capture: func(c Capture) { captured = append(captured, c.Body) }})
				credentials := []string{"stale-credential"}
				req := Request{URL: server.URL, SentCredentials: &credentials}
				if mode == "api-key" {
					req.APIKey = testAPIKey
					client.tokenSource = failTokens{t}
				}
				var out struct{ Code, Message string }
				err := client.DoJSON(context.Background(), req, &out)
				if status == http.StatusOK {
					if err != nil {
						t.Fatal(err)
					}
					out.Code = RedactErrorText(out.Code, credentials...)
					out.Message = RedactErrorText(out.Message, credentials...)
				} else {
					var api *APIError
					if !errors.As(err, &api) {
						t.Fatalf("expected APIError, got %v", err)
					}
					out.Code, out.Message = api.Code, api.Message
				}
				wantAttempts := 3
				if mode == "refresh" {
					wantAttempts = 2
				}
				if len(sent) != wantAttempts {
					t.Fatalf("attempts = %d, want %d", len(sent), wantAttempts)
				}
				wantCredentials := sent[:2]
				if mode == "api-key" {
					wantCredentials = sent[:1]
				}
				if !slices.Equal(credentials, wantCredentials) {
					t.Error("sent credentials were not cleared and collected distinctly")
				}
				for _, value := range sent {
					if strings.Contains(out.Code, value) || strings.Contains(out.Message, value) {
						t.Error("envelope leaked an earlier credential")
					}
					for _, body := range captured {
						if strings.Contains(string(body), value) {
							t.Error("capture leaked an earlier credential")
						}
					}
				}
			})
		}
	}
}

func TestSentCredentialsAcrossRefreshAndRetries(t *testing.T) {
	for _, collect := range []bool{false, true} {
		t.Run(strconv.FormatBool(collect), func(t *testing.T) {
			source := &longTokens{}
			var client *Client
			var sent []string
			var captured [][]byte
			var logs bytes.Buffer
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				sent = append(sent, strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
				// Replace the cached token as another call's refresh would.
				if len(sent) == 1 || len(sent) == 3 {
					next, _ := source.Token(r.Context())
					client.mu.Lock()
					client.token = next
					client.mu.Unlock()
				}
				switch len(sent) {
				case 1, 3, 4:
					w.WriteHeader(http.StatusServiceUnavailable)
				case 2:
					w.WriteHeader(http.StatusUnauthorized)
				default:
					w.WriteHeader(http.StatusBadRequest)
				}
				_, _ = fmt.Fprintf(w, `{"code":%q,"message":%q}`, sent[0], "bad "+strings.Join(sent, " "))
			}))
			defer server.Close()
			client = New(Config{HTTPClient: server.Client(), TokenSource: source, RetryCount: 2, RetryInterval: time.Nanosecond, Capture: func(c Capture) { captured = append(captured, c.Body) }, Logger: slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))})
			legacy := "stale-credential"
			credentials := []string{"stale-credential"}
			req := Request{URL: server.URL + "/synthetic-token-1", SentCredential: &legacy}
			if collect {
				req.SentCredentials = &credentials
			}
			err := client.DoJSON(context.Background(), req, nil)
			var api *APIError
			if len(sent) != 5 || !errors.As(err, &api) {
				t.Fatalf("expected five attempts and APIError, got %v", err)
			}
			want := []string{"synthetic-token-1", "synthetic-token-2", "synthetic-token-3", "synthetic-token-4"}
			if !slices.Equal(sent, append(append([]string(nil), want...), want[3])) {
				t.Fatal("unexpected attempt credentials")
			}
			if collect && !slices.Equal(credentials, want) {
				t.Error("did not collect credentials across refresh and retries")
			}
			if legacy != want[3] {
				t.Error("legacy field did not receive the final credential")
			}
			for _, credential := range want {
				for _, text := range []string{api.Message, api.Code, api.Error(), logs.String()} {
					if strings.Contains(text, credential) {
						t.Error("error or log leaked an earlier credential")
					}
				}
				for _, body := range captured {
					if strings.Contains(string(body), credential) {
						t.Error("capture leaked an earlier credential")
					}
				}
			}
		})
	}
}

func TestSentCredentialsResetEachCall(t *testing.T) {
	for _, raw := range []bool{false, true} {
		t.Run(strconv.FormatBool(raw), func(t *testing.T) {
			var attempts int
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				attempts++
				_, _ = w.Write([]byte(`{}`))
			}))
			defer server.Close()
			client := New(Config{HTTPClient: server.Client()})
			credentials := []string{"stale-credential"}
			legacy := "stale-credential"
			req := Request{URL: server.URL, APIKey: testAPIKey, SentCredentials: &credentials, SentCredential: &legacy}
			call := func() error {
				if raw {
					_, _, _, err := client.DoRaw(context.Background(), req)
					return err
				}
				return client.DoJSON(context.Background(), req, nil)
			}
			if err := call(); err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(credentials, []string{testAPIKey}) || legacy != testAPIKey {
				t.Error("API key was not collected after clearing earlier values")
			}
			req.SkipAuth = true
			if err := call(); !errors.Is(err, errAPIKeyWithSkipAuth) {
				t.Fatalf("expected invalid authentication options, got %v", err)
			}
			if len(credentials) != 0 || legacy != "" {
				t.Error("call that sent nothing retained earlier credentials")
			}
			req.APIKey = ""
			if err := call(); err != nil {
				t.Fatal(err)
			}
			if len(credentials) != 0 || legacy != "" || attempts != 2 {
				t.Error("unauthenticated call collected a credential or invalid call was sent")
			}
		})
	}
}
