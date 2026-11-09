package pricing

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/endpoints"
	"danny.vn/vngcloud/internal/transport"
)

// All tokens and response metadata here are synthetic.
type creditTokenSource struct{ token string }

func (s *creditTokenSource) Token(context.Context) (transport.Token, error) {
	return transport.Token{AccessToken: s.token, ExpiresAt: time.Now().Add(time.Hour)}, nil
}
func (*creditTokenSource) Invalidate(string) {}

func TestGetQuoteCredentialEchoSynthetic(t *testing.T) {
	const token = "synthetic-credit-token"
	for _, status := range []int{200, 400} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			var logs bytes.Buffer
			var captures []byte
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer "+token {
					t.Error("missing synthetic authorization")
				}
				w.WriteHeader(status)
				_, _ = fmt.Fprintf(w, `{"optimumPrice":100,"message":%q,"propertiesPrice":[{"name":%q,"description":%q}]}`, token, token, token)
			}))
			defer srv.Close()
			tc := transport.New(transport.Config{HTTPClient: srv.Client(), TokenSource: &creditTokenSource{token: token},
				Logger:  slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
				Capture: func(c transport.Capture) { captures = append(captures, c.Body...) },
			})
			c := New(core.NewTestConfig("hcm-3", "project-1", endpoints.Set{Portal: srv.URL + "/"}, tc))
			out, err := c.GetQuote(context.Background(), &GetQuoteInput{ResourceType: ResourceServer, Action: ActionResize})
			if status == 200 && (err != nil || out == nil || out.OptimumPrice != 100) {
				t.Fatalf("out = %+v, err = %v", out, err)
			}
			if status == 400 && (err == nil || out != nil) {
				t.Fatalf("out = %+v, err = %v", out, err)
			}
			encoded, marshalErr := json.Marshal(out)
			if marshalErr != nil {
				t.Fatal(marshalErr)
			}
			for name, text := range map[string]string{"output": string(encoded), "error": fmt.Sprint(err), "debug": logs.String(), "capture": string(captures)} {
				if strings.Contains(text, token) {
					t.Errorf("credential echoed in %s", name)
				}
			}
		})
	}
}
