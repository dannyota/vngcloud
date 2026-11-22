package billing

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/endpoints"
	"danny.vn/vngcloud/internal/testutil"
	"danny.vn/vngcloud/internal/transport"
)

func TestBudgetWriteNoReplay(t *testing.T) {
	for _, tc := range billingWriteTestCases() {
		for _, status := range []int{0, 400, 401, 403, 404, 429, 500, 502, 503, 307, 308} {
			t.Run(tc.name+"/"+strconv.Itoa(status), func(t *testing.T) {
				var calls atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					calls.Add(1)
					if status == 0 {
						conn, _, err := http.NewResponseController(w).Hijack()
						if err != nil {
							t.Error(err)
							return
						}
						_ = conn.Close()
						return
					}
					w.Header().Set("Location", "/mutate")
					w.WriteHeader(status)
				}))
				defer server.Close()
				source := &credentialTestTokens{values: []string{"synthetic-initial-token", "synthetic-refresh-token"}}
				transportClient := transport.New(transport.Config{HTTPClient: server.Client(), TokenSource: source, RetryCount: 3})
				c := New(core.NewTestConfig("hcm-3", "", endpoints.Set{Billing: server.URL + "/"}, transportClient))
				out, err := tc.call(context.Background(), c)
				if out != nil || err == nil || calls.Load() != 1 {
					t.Fatal("budget write was replayed or returned success")
				}
				if status == 401 && source.calls.Load() != 1 {
					t.Fatal("budget write refreshed and resent credentials")
				}
				if status < 400 || status >= 500 {
					var api *core.APIError
					if !errors.As(err, &api) || api.Retryable || !strings.Contains(api.Message, "unconfirmed") {
						t.Fatal("ambiguous budget write lost uncertainty")
					}
				}
			})
		}
	}
}

func TestBudgetWriteCredentialEcho(t *testing.T) {
	for _, tc := range billingWriteTestCases() {
		t.Run(tc.name, func(t *testing.T) {
			body := `{"code":200,"data":null}`
			if tc.fixture != "" {
				body = testutil.FixtureBody(t, "../testdata/billing/"+tc.fixture+".json")
			}
			body = `{"name":"synthetic-token",` + strings.TrimSpace(body)[1:]
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) }))
			defer server.Close()
			tcTransport := transport.New(transport.Config{HTTPClient: server.Client(), TokenSource: &credentialTestTokens{values: []string{"synthetic-token"}}})
			c := New(core.NewTestConfig("hcm-3", "", endpoints.Set{Billing: server.URL + "/"}, tcTransport))
			out, err := tc.call(context.Background(), c)
			assertBillingInvalid(t, out, err)
		})
	}
}
