package pricing_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/endpoints"
	"danny.vn/vngcloud/internal/transport"
	"danny.vn/vngcloud/pricing"
	"danny.vn/vngcloud/volume"
)

type syntheticQuoteCredentials struct {
	values []string
	calls  atomic.Int32
}

func (s *syntheticQuoteCredentials) Token(context.Context) (transport.Token, error) {
	index := min(int(s.calls.Add(1))-1, len(s.values)-1)
	return transport.Token{
		AccessToken: s.values[index],
		ExpiresAt:   time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC),
	}, nil
}

func (*syntheticQuoteCredentials) Invalidate(string) {}

func numericCredentialConfig(t *testing.T, handler http.Handler, credentials ...string) vngcloud.Config {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	tc := transport.New(transport.Config{
		HTTPClient:  server.Client(),
		TokenSource: &syntheticQuoteCredentials{values: credentials},
	})
	return core.NewTestConfig("hcm-3", "project-1", endpoints.Set{
		Portal: server.URL + "/", VServer: server.URL + "/",
	}, tc)
}

func assertQuoteCredentialRefusal(t *testing.T, err error, credentials ...string) {
	t.Helper()
	var api *vngcloud.APIError
	if !errors.As(err, &api) || api.Code != "InvalidResponse" || api.Message != "invalid quote response; body withheld" || api.Err != nil {
		t.Errorf("err = %v, want fixed InvalidResponse APIError", err)
	}
	var visit func(error)
	visit = func(cause error) {
		if cause == nil {
			return
		}
		for _, credential := range credentials {
			if strings.Contains(cause.Error(), strings.TrimSpace(credential)) {
				t.Error("credential reached error text")
			}
		}
		switch wrapped := cause.(type) { //nolint:errorlint // Check the text of every cause, including joined errors.
		case interface{ Unwrap() []error }:
			for _, child := range wrapped.Unwrap() {
				visit(child)
			}
		case interface{ Unwrap() error }:
			visit(wrapped.Unwrap())
		}
	}
	visit(err)
}

func TestGetQuoteRejectsNumericCredentialsSynthetic(t *testing.T) {
	for _, credential := range []string{
		"1235", "+1235", "-1235", "1235.", ".5", "-.5", "+.5",
		"1e3", "-1E-3", "1e99999", "01", "0", "  +1.25e+03  ",
	} {
		for _, body := range []string{`{"optimumPrice":1234.6}`, `{"optimumPrice":-1234.6}`} {
			t.Run(credential+"/"+body, func(t *testing.T) {
				cfg := numericCredentialConfig(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method != http.MethodPost || r.URL.Path != "/v1/price" {
						t.Error("unexpected quote request")
					}
					_, _ = w.Write([]byte(body))
				}), credential)
				out, err := pricing.New(cfg).GetQuote(context.Background(), &pricing.GetQuoteInput{ResourceType: pricing.ResourceVolume})
				if out != nil {
					t.Error("numeric credential returned quote output")
				}
				assertQuoteCredentialRefusal(t, err, credential)
			})
		}
	}
}

func TestGetQuoteRejectsNumericCredentialsAcrossRefreshSynthetic(t *testing.T) {
	for _, credentials := range [][]string{{"1235", "synthetic-refreshed"}, {"synthetic-initial", "1235"}} {
		for _, status := range []int{http.StatusOK, http.StatusBadRequest} {
			t.Run(strings.Join(credentials, "/")+"/"+http.StatusText(status), func(t *testing.T) {
				var sends atomic.Int32
				cfg := numericCredentialConfig(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					if sends.Add(1) == 1 {
						w.WriteHeader(http.StatusUnauthorized)
						return
					}
					w.WriteHeader(status)
					_, _ = w.Write([]byte(`{"optimumPrice":1234.6,"message":"unusable price"}`))
				}), credentials...)
				out, err := pricing.New(cfg).GetQuote(context.Background(), &pricing.GetQuoteInput{ResourceType: pricing.ResourceVolume})
				if out != nil || sends.Load() != 2 {
					t.Errorf("out = %+v, sends = %d, want nil and 2", out, sends.Load())
				}
				assertQuoteCredentialRefusal(t, err, "1235")
			})
		}
	}
}

func TestCreateVolumeNumericCredentialStopsBeforePriceGuardSynthetic(t *testing.T) {
	var mutations, quotes, lists atomic.Int32
	cfg := numericCredentialConfig(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer 1235" {
			t.Error("missing synthetic numeric credential")
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v2/project-1/volumes":
			lists.Add(1)
			_, _ = w.Write([]byte(`{"listData":[],"page":1,"pageSize":10000,"totalPage":0,"totalItem":0}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/price":
			quotes.Add(1)
			_, _ = w.Write([]byte(`{"optimumPrice":1234.6}`))
		default:
			mutations.Add(1)
			w.WriteHeader(http.StatusBadRequest)
		}
	}), "1235")
	out, err := volume.New(cfg).CreateVolume(context.Background(), &volume.CreateVolumeInput{
		Name: "volume-1", ZoneID: "zone-1", Size: 10, VolumeTypeID: "type-1", MaxPrice: 0,
	})
	if out != nil || lists.Load() != 1 || quotes.Load() != 1 || mutations.Load() != 0 {
		t.Errorf("out = %+v, lists = %d, quotes = %d, mutations = %d", out, lists.Load(), quotes.Load(), mutations.Load())
	}
	if errors.Is(err, vngcloud.ErrPriceAboveMax) {
		t.Error("numeric credential reached the price guard")
	}
	assertQuoteCredentialRefusal(t, err, "1235")
}

func TestGetQuoteOpaqueCredentialsKeepResponseChecksSynthetic(t *testing.T) {
	for _, tc := range []struct {
		credential, body string
		refused          bool
	}{
		{"synthetic-opaque-1235", `{"optimumPrice":1234.6}`, false},
		{"1235-tail", `{"optimumPrice":1234.6}`, false},
		{"e+06", `{"optimumPrice":1e6}`, true},
		{"synthetic-opaque", `{"optimumPrice":100,"propertiesPrice":[{"name":"\u0073ynthetic-opaque"}]}`, true},
	} {
		t.Run(tc.credential, func(t *testing.T) {
			cfg := numericCredentialConfig(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(tc.body))
			}), tc.credential)
			out, err := pricing.New(cfg).GetQuote(context.Background(), &pricing.GetQuoteInput{ResourceType: pricing.ResourceVolume})
			if tc.refused {
				if out != nil {
					t.Error("reflected opaque credential returned quote output")
				}
				assertQuoteCredentialRefusal(t, err, tc.credential)
			} else if err != nil || out == nil || out.OptimumPrice != 1234.6 {
				t.Errorf("out = %+v, err = %v, want unchanged opaque-token quote", out, err)
			}
		})
	}
}
