package billingresources

import (
	"context"
	"errors"
	"fmt"
	"io"
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

func TestResourceSuccessfulCredentialEcho(t *testing.T) {
	for _, credential := range []string{"synthetic-first-token", "synthetic-second-token"} {
		for _, field := range []string{"artifactName", "artifactId", "status", "statusUI", "billingElements", "extra"} {
			t.Run(field+"/"+credential, func(t *testing.T) {
				value := fmt.Sprintf(`%q`, credential)
				if field == "status" || field == "statusUI" {
					value = `{"nested":` + value + `}`
				}
				if field == "billingElements" {
					value = `[{"quantity":` + value + `}]`
				}
				data := `{"data":[{"` + field + `":` + value + `}]}`
				if field == "extra" {
					data = `{"data":[],"extra":{"warningThresholds":` + value + `}}`
				}
				body := strings.ReplaceAll(`{"code":200,"data":`+data+`}`, "synthetic", `\u0073ynthetic`)
				var calls atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					if calls.Add(1) == 1 {
						w.WriteHeader(401)
						return
					}
					_, _ = w.Write([]byte(body))
				}))
				defer server.Close()
				transportClient := transport.New(transport.Config{HTTPClient: server.Client(), TokenSource: &rotatingResourceTokens{values: []string{"synthetic-first-token", "synthetic-second-token"}}})
				c := core.NewTestClient("hcm-3", "", endpoints.Set{Billing: server.URL + "/"}, transportClient)
				out, err := List(context.Background(), c)
				var api *core.APIError
				if out != nil || !errors.As(err, &api) || api.StatusCode != 200 || api.Code != "InvalidResponse" || calls.Load() != 2 {
					t.Fatal("resource credential echo returned output or lost classification")
				}
				if strings.Contains(fmt.Sprintf("%+v", err), credential) || api.Err != nil {
					t.Fatal("resource credential reached error")
				}
			})
		}
	}
}

func TestResourceMalformedResponseStatus(t *testing.T) {
	for _, truncated := range []bool{false, true} {
		t.Run(strconv.FormatBool(truncated), func(t *testing.T) {
			c := testutil.NewCoreClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if truncated {
					w.Header().Set("Content-Length", "100")
				}
				_, _ = w.Write([]byte(`{"code":`))
			}))
			out, err := List(context.Background(), c)
			var api *core.APIError
			if out != nil || !errors.As(err, &api) || api.StatusCode != 200 || api.Code != "InvalidResponse" {
				t.Fatal("malformed resource response lost status and InvalidResponse")
			}
			if truncated && !errors.Is(err, io.ErrUnexpectedEOF) {
				t.Fatal("truncated response lost safe read cause")
			}
		})
	}
}

func TestResourceDecodedCredentialEcho(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(401)
			return
		}
		_, _ = w.Write([]byte(`{"code":200,"data":{"data":[{"artifactId":"resource-1","cost":1e6}]}}`))
	}))
	defer server.Close()
	transportClient := transport.New(transport.Config{HTTPClient: server.Client(), TokenSource: &rotatingResourceTokens{values: []string{"e+06", "synthetic-refreshed-token"}}})
	c := core.NewTestClient("hcm-3", "", endpoints.Set{Billing: server.URL + "/"}, transportClient)
	out, err := List(context.Background(), c)
	var api *core.APIError
	if out != nil || !errors.As(err, &api) || api.Code != "InvalidResponse" || api.StatusCode != 200 || calls.Load() != 2 {
		t.Fatal("decoded resource credential returned data or lost classification")
	}
}
