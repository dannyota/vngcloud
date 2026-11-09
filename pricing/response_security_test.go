package pricing

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

// Synthetic credentials distinguish the initial send from its 401 refresh.
type quoteTokens struct {
	calls  atomic.Int32
	values []string
}

func (s *quoteTokens) Token(context.Context) (transport.Token, error) {
	call := int(s.calls.Add(1))
	value := "synthetic-quote-token-initial"
	if call > 1 {
		value = "synthetic-quote-token-refreshed"
	}
	if len(s.values) > 0 {
		value = s.values[min(call-1, len(s.values)-1)]
	}
	return transport.Token{AccessToken: value, ExpiresAt: time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC)}, nil
}

func (*quoteTokens) Invalidate(string) {}

func newQuoteSecurityClient(t *testing.T, handler http.Handler, logs *bytes.Buffer, captures *int, tokens ...string) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	tc := transport.New(transport.Config{
		HTTPClient: srv.Client(), TokenSource: &quoteTokens{values: tokens},
		RetryCount: 1, RetryInterval: time.Nanosecond,
		Logger:  slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
		Capture: func(transport.Capture) { *captures++ },
	})
	return New(core.NewTestConfig("hcm-3", "project-1", endpoints.Set{Portal: srv.URL + "/"}, tc))
}

func TestGetQuoteRejectsDecodedNumericCredentialsSynthetic(t *testing.T) {
	for _, tc := range []struct{ name, credential, wire string }{
		{"initial", "1234", "1.234e3"},
		{"refreshed", "9876", "9.876e3"},
		{"negative", "1234", "-1.234e3"},
		{"rounded", "12.34", "1.23399999999999999e1"},
		{"decimal rendering", "1000000", "1e6"},
		{"Go rendering", "1e+06", "1e6"},
	} {
		for name, field := range map[string]string{
			"optimumPrice":      `"optimumPrice":` + tc.wire,
			"originalPrice":     `"originalPrice":` + tc.wire,
			"discountPrice":     `"discountPrice":` + tc.wire,
			"discountPercent":   `"discountPercent":` + tc.wire,
			"property optimum":  `"propertiesPrice":[{"optimumPrice":` + tc.wire + `}]`,
			"property monthly":  `"propertiesPrice":[{"monthlyPrice":` + tc.wire + `}]`,
			"property current":  `"propertiesPrice":[{"currentPrice":` + tc.wire + `}]`,
			"property discount": `"propertiesPrice":[{"discountPercent":` + tc.wire + `}]`,
		} {
			for _, action := range []string{ActionCreate, ActionResize} {
				t.Run(tc.name+"/"+name+"/"+action, func(t *testing.T) {
					var logs bytes.Buffer
					var captures, sends int
					initial, refreshed := tc.credential, "synthetic-numeric-refreshed"
					if tc.name == "refreshed" {
						initial, refreshed = "1234", tc.credential
					}
					c := newQuoteSecurityClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						sends++
						want := initial
						if sends > 1 {
							want = refreshed
						}
						if r.Header.Get("Authorization") != "Bearer "+want {
							t.Error("wrong synthetic credential")
						}
						if sends == 1 {
							w.WriteHeader(http.StatusUnauthorized)
							_, _ = w.Write([]byte(`{}`))
							return
						}
						_, _ = fmt.Fprintf(w, `{"optimumPrice":100,%s}`, field)
					}), &logs, &captures, initial, refreshed)
					out, err := c.GetQuote(context.Background(), &GetQuoteInput{ResourceType: ResourceServer, Action: action})
					var api *vngcloud.APIError
					if out != nil || !errors.As(err, &api) {
						t.Errorf("out = %+v, err = %v, want withheld quote and APIError", out, err)
						return
					}
					for _, text := range []string{err.Error(), api.Operation, api.Code, api.Message} {
						if strings.Contains(text, tc.credential) {
							t.Error("decoded numeric credential reached error text or metadata")
						}
					}
					if sends != 2 || captures != 0 {
						t.Errorf("sends = %d, captures = %d, want 2 and 0", sends, captures)
					}
				})
			}
		}
	}
}

func TestGetQuoteSensitiveSyntaxIdentitySynthetic(t *testing.T) {
	for name, body := range map[string]string{
		"truncated":     `{"optimumPrice":`,
		"invalid token": `{"message":"synthetic-quote-token-initial","optimumPrice":@}`,
		"trailing text": `{"optimumPrice":100}synthetic-quote-token-initial`,
	} {
		t.Run(name, func(t *testing.T) {
			var logs bytes.Buffer
			var captures int
			c := newQuoteSecurityClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(body))
			}), &logs, &captures)
			out, err := c.GetQuote(context.Background(), &GetQuoteInput{ResourceType: ResourceServer})
			var api *vngcloud.APIError
			if out != nil || !errors.As(err, &api) {
				t.Fatalf("out = %+v, err = %v, want nil and APIError", out, err)
			}
			var original, actual, fixed *json.SyntaxError
			var value any
			if !errors.As(json.Unmarshal([]byte(body), &value), &original) {
				t.Fatal("test body must have invalid JSON syntax")
			}
			if !errors.As(err, &actual) || actual.Offset != original.Offset {
				t.Errorf("syntax identity or offset lost: got %+v, want offset %d", actual, original.Offset)
			} else {
				if !errors.As(json.Unmarshal([]byte("?"), &value), &fixed) {
					t.Fatal("fixed decoder input must produce a syntax error")
				}
				if actual.Error() != fixed.Error() {
					t.Error("syntax error retained response-derived text")
				}
				if strings.Contains(fmt.Sprintf("%+v %#v", actual, actual), "synthetic-quote-token") {
					t.Error("syntax error metadata retained a credential")
				}
			}
			if captures != 0 {
				t.Errorf("captures = %d, want 0", captures)
			}
			assertQuoteSecretsWithheld(t, out, err, logs.String())
		})
	}
}

func TestGetQuoteRejectsReflectedResponseSynthetic(t *testing.T) {
	for _, reflected := range []string{"synthetic-quote-token-initial", "synthetic-quote-token-refreshed"} {
		for _, escaped := range []bool{false, true} {
			encoded, err := json.Marshal(reflected)
			if err != nil {
				t.Fatal(err)
			}
			echo := string(encoded)
			if escaped {
				echo = strings.ReplaceAll(echo, "s", `\u0073`)
			}
			for name, field := range map[string]string{
				"name":                 `"propertiesPrice":[{"name":` + echo + `}]`,
				"description":          `"propertiesPrice":[{"description":` + echo + `}]`,
				"unknown property":     `"propertiesPrice":[{"extra":` + echo + `}]`,
				"artifactPrices":       `"artifactPrices":[{"extra":` + echo + `}]`,
				"originalCurrentPrice": `"originalCurrentPrice":` + echo,
				"originalNewPrice":     `"originalNewPrice":` + echo,
				"unknown key":          echo + `:"ordinary value"`,
				"overwritten field":    `"propertiesPrice":[{"name":` + echo + `,"name":"safe"}]`,
			} {
				t.Run(reflected+"/"+strconv.FormatBool(escaped)+"/"+name, func(t *testing.T) {
					var logs bytes.Buffer
					var captures, sends int
					c := newQuoteSecurityClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						sends++
						if sends == 1 {
							if r.Header.Get("Authorization") != "Bearer synthetic-quote-token-initial" {
								t.Error("missing initial synthetic credential")
							}
							w.WriteHeader(http.StatusUnauthorized)
							// A future credential must not escape an earlier capture.
							_, _ = w.Write([]byte(`{"message":"synthetic-quote-token-refreshed"}`))
							return
						}
						if r.Header.Get("Authorization") != "Bearer synthetic-quote-token-refreshed" {
							t.Error("missing refreshed synthetic credential")
						}
						_, _ = fmt.Fprintf(w, `{"optimumPrice":100,%s}`, field)
					}), &logs, &captures)
					out, err := c.GetQuote(context.Background(), &GetQuoteInput{ResourceType: ResourceServer})
					var api *vngcloud.APIError
					if out != nil || !errors.As(err, &api) {
						t.Errorf("out = %+v, err = %v, want nil and APIError", out, err)
					}
					if sends != 2 || captures != 0 {
						t.Errorf("sends = %d, captures = %d, want 2 and 0", sends, captures)
					}
					assertQuoteSecretsWithheld(t, out, err, logs.String())
				})
			}
		}
	}
}

func assertQuoteSecretsWithheld(t *testing.T, out *GetQuoteOutput, err error, logs string) {
	t.Helper()
	encoded, marshalErr := json.Marshal(out)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	texts := []string{string(encoded), fmt.Sprint(err), logs}
	var api *vngcloud.APIError
	if errors.As(err, &api) {
		texts = append(texts, api.Operation, api.Code, api.Message)
	}
	for _, text := range texts {
		if strings.Contains(text, "synthetic-quote-token") || strings.Contains(text, `\u0073ynthetic-quote-token`) {
			t.Error("credential reached output, error metadata, or debug log")
		}
	}
}

func TestGetQuoteWithholdsCapturesSynthetic(t *testing.T) {
	for _, status := range []int{200, 400, 403, 404, 429, 502} {
		for name, body := range map[string]string{
			"safe unknown fields": `{"optimumPrice":100,"artifactPrices":[{"amount":1e10000}],"originalCurrentPrice":200,"originalNewPrice":100}`,
			"wrong type":          `{"optimumPrice":100,"propertiesPrice":{"synthetic-quote-token-initial":"wrong type"}}`,
			"truncated":           `{"optimumPrice":100,"synthetic-quote-token-initial":`,
			"error echo":          `{"code":"\u0073ynthetic-quote-token-initial","message":"synthetic-quote-token-initial"}`,
		} {
			t.Run(strconv.Itoa(status)+"/"+name, func(t *testing.T) {
				var logs bytes.Buffer
				var captures int
				c := newQuoteSecurityClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(status)
					_, _ = w.Write([]byte(body))
				}), &logs, &captures)
				out, err := c.GetQuote(context.Background(), &GetQuoteInput{ResourceType: ResourceServer})
				if status == 200 && name == "safe unknown fields" {
					if err != nil || out == nil || out.OptimumPrice != 100 {
						t.Fatalf("out = %+v, err = %v, want unchanged quote", out, err)
					}
				} else {
					var api *vngcloud.APIError
					if out != nil || !errors.As(err, &api) {
						t.Fatalf("out = %+v, err = %v, want nil and APIError", out, err)
					}
					if status != 200 && api.StatusCode != status {
						t.Errorf("status = %d, want %d", api.StatusCode, status)
					}
					if status == 404 && !errors.Is(err, vngcloud.ErrNotFound) {
						t.Errorf("err = %v, want ErrNotFound", err)
					}
				}
				if captures != 0 {
					t.Errorf("captures = %d, want 0", captures)
				}
				assertQuoteSecretsWithheld(t, out, err, logs.String())
			})
		}
	}
}
