package transport

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

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
