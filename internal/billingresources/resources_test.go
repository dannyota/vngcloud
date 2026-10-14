package billingresources

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/endpoints"
	"danny.vn/vngcloud/internal/testutil"
	"danny.vn/vngcloud/internal/transport"
)

type rotatingResourceTokens struct{ count atomic.Int64 }

func (s *rotatingResourceTokens) Token(context.Context) (transport.Token, error) {
	return transport.Token{AccessToken: fmt.Sprintf("synthetic-token-%d", s.count.Add(1)), ExpiresAt: time.Now().Add(time.Hour)}, nil
}

func (*rotatingResourceTokens) Invalidate(string) {}

func TestBillingWithholdsEarlierAttemptCredential(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusBadRequest} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			var sent []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				sent = append(sent, strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
				if len(sent) == 1 {
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
				w.WriteHeader(status)
				_, _ = fmt.Fprintf(w, `{"code":%q,"message":%q}`, sent[0], "bad "+strings.Join(sent, " "))
			}))
			defer server.Close()
			captures := 0
			tc := transport.New(transport.Config{HTTPClient: server.Client(), TokenSource: &rotatingResourceTokens{}, Capture: func(transport.Capture) { captures++ }})
			c := core.NewTestClient("hcm-3", "", endpoints.Set{Region: "hcm-3", Billing: server.URL + "/"}, tc)
			data, err := List(context.Background(), c)
			var api *core.APIError
			if len(sent) != 2 || sent[0] == sent[1] || !errors.As(err, &api) || data != nil || captures != 0 {
				t.Fatalf("expected refresh and withheld response, got %v", err)
			}
			for _, credential := range sent {
				if strings.Contains(api.Message, credential) || strings.Contains(api.Code, credential) || strings.Contains(api.Error(), credential) {
					t.Error("billing response leaked an earlier credential")
				}
			}
		})
	}
}

func TestAccountStrict(t *testing.T) {
	for _, field := range []string{"accountId", "userId"} {
		for _, tc := range []struct{ value, want string }{{"12345", "12345"}, {"12345.0", "12345"}, {"9223372036854775807", "9223372036854775807"}, {"0", ""}, {"-1", ""}, {"1.5", ""}, {"null", ""}, {`"12345"`, ""}, {"9223372036854775808", ""}} {
			t.Run(field+"/"+tc.value, func(t *testing.T) {
				c := testutil.NewCoreClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path != "/gateway/api/v1/home/user-info" || r.Header.Get("portal-user-id") != "" {
						t.Error("account request")
					}
					_, _ = w.Write([]byte(`{"code":200,"data":{"` + field + `":` + tc.value + `}}`))
				}))
				got, err := Account(context.Background(), c)
				if got != tc.want || (err == nil) != (tc.want != "") {
					t.Fatalf("account value %q error %v", got, err)
				}
			})
		}
	}
}

func TestListAllTypedFields(t *testing.T) {
	for _, data := range []string{`{"data":[null]}`, `{"data":[{"cost":"NaN"}]}`, `{"data":[],"totalAutoRenews":"1"}`, `{"data":[{"billingElements":[{"sku":1}]}]}`} {
		c := testutil.NewCoreClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"code":200,"data":` + data + `}`))
		}))
		_, err := List(context.Background(), c)
		var api *core.APIError
		if !errors.As(err, &api) {
			t.Fatal("malformed typed list accepted")
		}
	}
}

func TestListNumericCode(t *testing.T) {
	c := testutil.NewCoreClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"code":200.0,"data":{"data":[]}}`))
	}))
	if _, err := List(context.Background(), c); err != nil {
		t.Fatal("numeric code 200 rejected")
	}
}

func TestExactBillingNames(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		kind       string
	}{
		{"envelope code", `{"code":200,"Code":200,"data":{"data":[]}}`, "list"},
		{"envelope data", `{"code":200,"data":{"data":[]},"Data":{"data":[]}}`, "list"},
		{"list data", `{"code":200,"data":{"Data":[]}}`, "list"},
		{"list row", `{"code":200,"data":{"data":[{"artifactId":"nat-1","ArtifactId":"nat-2"}]}}`, "list"},
		{"list element", `{"code":200,"data":{"data":[{"billingElements":[{"sku":"nat.s-standard","SKU":"other"}]}]}}`, "list"},
		{"account alias", `{"code":200,"data":{"accountId":111,"AccountId":123456}}`, "account"},
		{"account duplicate", `{"code":200,"data":{"accountId":111,"accountId":123456}}`, "account"},
		{"PUT aliases", `{"code":200,"data":{"successAll":false,"SuccessAll":true,"errorAutoRenewResources":[{}],"ErrorAutoRenewResources":[]}}`, "put"},
		{"PUT duplicate", `{"code":200,"data":{"successAll":false,"successAll":true,"errorAutoRenewResources":[]}}`, "put"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := testutil.NewCoreClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(tc.body)) }))
			var err error
			switch tc.kind {
			case "list":
				_, err = List(context.Background(), c)
			case "account":
				_, err = Account(context.Background(), c)
			case "put":
				err = Put(context.Background(), c, "put-test", "123456", Setting{})
			}
			var api *core.APIError
			if !errors.As(err, &api) || api.Code != "InvalidResponse" {
				t.Fatalf("hostile billing reply accepted: %v", err)
			}
		})
	}
}

func TestBillingResponseBound(t *testing.T) {
	c := testutil.NewCoreClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"code":200,"data":{"data":[],"padding":"` + strings.Repeat("x", 5<<20) + `"}}`))
	}))
	if _, err := List(context.Background(), c); err == nil {
		t.Fatal("oversized billing response accepted")
	}
}
