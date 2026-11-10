package compute

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/pricing"
)

func TestFloatingQuoteServerBodyAndIncludedLine(t *testing.T) {
	var types []string
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/price" {
			t.Errorf("quote made exposure or resource read %s", r.URL.Path)
		}
		body := decodeComputeBody(t, r)
		types = append(types, body["resourceType"].(string))
		info := body["resourceInfo"].(map[string]any)
		if info["zoneId"] != "zone-1" || info["period"] != float64(1) || info["isPoc"] != false {
			t.Errorf("info=%v", info)
		}
		raw, _ := json.Marshal(body)
		if strings.Contains(string(raw), "bootstrap") || strings.Contains(string(raw), base64.StdEncoding.EncodeToString([]byte("bootstrap"))) {
			t.Error("quote leaked user data")
		}
		_, _ = w.Write([]byte(quoteServerFixture))
	}))
	in := &CreateServerInput{ZoneID: "zone-1", FlavorID: "flavor-1", ImageID: "image-1", RootDiskSize: 20, RootDiskTypeID: "type-1", AttachFloatingIP: true, AllowOpenLogin: true, NoWait: true, MaxPrice: math.NaN(), UserData: "bootstrap"}
	out, err := c.QuoteCreateServer(context.Background(), in)
	if err != nil || out == nil {
		t.Fatalf("out=%v err=%v", out, err)
	}
	if strings.Join(types, ",") != "server" || out.OptimumPrice != 347800 || out.OriginalPrice != 347800 || out.DiscountPrice != 0 || out.DiscountPercent != 0 || len(out.Properties) != 3 {
		t.Fatalf("types=%v out=%+v", types, out)
	}
	want := []pricing.PriceProperty{
		{Name: "INSTANCE TYPE", OptimumPrice: 283800, MonthlyPrice: 283800},
		{Name: "ROOT DISK", OptimumPrice: 64000, MonthlyPrice: 64000},
		{Name: "FLOATING IP", Description: "Included in server order for the verified configuration"},
	}
	if !reflect.DeepEqual(out.Properties, want) {
		t.Fatalf("properties=%+v, want %+v", out.Properties, want)
	}
}

func TestFloatingQuotePreservesServerValuesAndRequest(t *testing.T) {
	for _, properties := range []string{`[]`, `[{"name":"ROOT DISK","description":"server disk","optimumPrice":90,"monthlyPrice":100,"currentPrice":80,"discountPercent":10}]`} {
		t.Run(properties, func(t *testing.T) {
			var bodies []map[string]any
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				bodies = append(bodies, decodeComputeBody(t, r))
				_, _ = fmt.Fprintf(w, `{"optimumPrice":90,"originalPrice":100,"discountPrice":10,"discountPercent":10,"propertiesPrice":%s}`, properties)
			}))
			in := pricedServerInput()
			server, err := c.QuoteCreateServer(context.Background(), in)
			if err != nil {
				t.Fatal(err)
			}
			in.AttachFloatingIP = true
			out, err := c.QuoteCreateServer(context.Background(), in)
			if err != nil || out == nil {
				t.Fatalf("out=%v err=%v", out, err)
			}
			if len(bodies) != 2 || !reflect.DeepEqual(bodies[0], bodies[1]) {
				t.Fatalf("quote bodies=%v", bodies)
			}
			if out.OptimumPrice != 90 || out.OriginalPrice != 100 || out.DiscountPrice != 10 || out.DiscountPercent != 10 || len(out.Properties) != len(server.Properties)+1 {
				t.Fatalf("out=%+v", out)
			}
			if !reflect.DeepEqual(out.Properties[:len(server.Properties)], server.Properties) {
				t.Fatalf("server properties changed: %+v", out.Properties)
			}
			want := pricing.PriceProperty{Name: "FLOATING IP", Description: "Included in server order for the verified configuration"}
			if got := out.Properties[len(server.Properties)]; !reflect.DeepEqual(got, want) {
				t.Fatalf("included line=%+v, want %+v", got, want)
			}
		})
	}
}

func TestFloatingQuoteRejectsInvalidServerWithoutPartialOutput(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		unpriced   bool
	}{
		{"absent optimum", `{"originalPrice":120000,"discountPrice":0,"discountPercent":0}`, false},
		{"null optimum", `{"optimumPrice":null,"originalPrice":120000,"discountPrice":0,"discountPercent":0}`, false},
		{"zero", `{"optimumPrice":0,"originalPrice":0,"discountPrice":0,"discountPercent":0}`, true},
		{"negative", `{"optimumPrice":-1,"originalPrice":0,"discountPrice":1,"discountPercent":0}`, true},
		{"missing totals", `{"optimumPrice":120000}`, false},
		{"inconsistent", `{"optimumPrice":120000,"originalPrice":120001,"discountPrice":0,"discountPercent":0}`, false},
		{"negative discount", `{"optimumPrice":120000,"originalPrice":120000,"discountPrice":-1,"discountPercent":0}`, false},
		{"overflow", `{"optimumPrice":1e309,"originalPrice":1e309,"discountPrice":0,"discountPercent":0}`, false},
		{"large discount mismatch", `{"optimumPrice":1,"originalPrice":1e12,"discountPrice":999999999998,"discountPercent":99.9999999999}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n := 0
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				n++
				_, _ = w.Write([]byte(tc.body))
			}))
			in := validCreateServerInput()
			in.AttachFloatingIP = true
			out, err := c.QuoteCreateServer(context.Background(), in)
			if err == nil || out != nil {
				t.Fatalf("partial quote out=%v err=%v", out, err)
			}
			if n != 1 {
				t.Fatalf("quotes=%d, want one server quote", n)
			}
			if tc.unpriced && !errors.Is(err, vngcloud.ErrUnpriced) {
				t.Fatalf("err=%v, want Unpriced", err)
			}
			if !tc.unpriced {
				var api *vngcloud.APIError
				if !errors.As(err, &api) || api.Code != "InvalidResponse" {
					t.Fatalf("err=%v, want InvalidResponse", err)
				}
			}
		})
	}
}

func TestFloatingServerQuoteFailureIdentity(t *testing.T) {
	for _, create := range []bool{false, true} {
		for _, status := range []int{200, 401, 500} {
			t.Run(fmt.Sprint(create, status), func(t *testing.T) {
				quotes, orders := 0, 0
				base := floatingCreateHandler(t, func() string { return floatingSafeRules }, func(http.ResponseWriter, *http.Request) { orders++ }, nil)
				c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path != "/v1/price" {
						base.ServeHTTP(w, r)
						return
					}
					quotes++
					w.WriteHeader(status)
					if status == 200 {
						_, _ = w.Write([]byte(`{"optimumPrice":0,"originalPrice":0,"discountPrice":0,"discountPercent":0}`))
					} else {
						_, _ = w.Write([]byte(`{"code":"QuoteUnavailable","message":"synthetic failure"}`))
					}
				}))
				in := validCreateServerInput()
				in.AttachFloatingIP = true
				in.MaxPrice = 347800
				if create {
					out, err := c.CreateServer(context.Background(), in)
					if out != nil || err == nil {
						t.Fatalf("out=%v err=%v", out, err)
					}
					assertFloatingQuoteError(t, err, status)
				} else {
					out, err := c.QuoteCreateServer(context.Background(), in)
					if out != nil || err == nil {
						t.Fatalf("out=%v err=%v", out, err)
					}
					assertFloatingQuoteError(t, err, status)
				}
				if quotes != 1 || orders != 0 {
					t.Fatalf("quotes=%d orders=%d", quotes, orders)
				}
			})
		}
	}
}

func assertFloatingQuoteError(t *testing.T, err error, status int) {
	t.Helper()
	if status == 200 {
		if !errors.Is(err, vngcloud.ErrUnpriced) {
			t.Fatalf("err=%v, want Unpriced", err)
		}
		return
	}
	var api *vngcloud.APIError
	if !errors.As(err, &api) || api.StatusCode != status || api.Code != "QuoteUnavailable" || api.Operation != "pricing.GetQuote" {
		t.Fatalf("lost quote error identity: %v", err)
	}
}

func TestFloatingQuoteRejectsCredentialReflections(t *testing.T) {
	for _, body := range []string{
		strings.Replace(quoteServerFixture, "INSTANCE TYPE", "synthetic-token-001", 1),
		strings.TrimSuffix(quoteServerFixture, "}") + `,"unknown":"synthetic\u002dtoken-001"}`,
	} {
		t.Run(body, func(t *testing.T) {
			c, logs, captures := newServerSecurityClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(body))
			}), 0)
			in := validCreateServerInput()
			in.AttachFloatingIP = true
			out, err := c.QuoteCreateServer(context.Background(), in)
			assertNoUnsafeServerOutput(t, out, err)
			if strings.Contains(logs.String(), "synthetic-token-") || captures.Load() != 0 {
				t.Fatal("credential escaped through logs or captures")
			}
		})
	}
}

func TestFloatingQuoteServerResponseBound(t *testing.T) {
	for _, extra := range []int{0, 1} {
		t.Run(strconv.Itoa(extra), func(t *testing.T) {
			calls := 0
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls++
				body := quoteServerFixture
				body += strings.Repeat(" ", (1<<20)-len(body)+extra)
				_, _ = w.Write([]byte(body))
			}))
			in := validCreateServerInput()
			in.AttachFloatingIP = true
			out, err := c.QuoteCreateServer(context.Background(), in)
			if extra == 0 {
				if err != nil || out == nil {
					t.Fatal(err)
				}
			} else if err == nil || out != nil {
				t.Fatalf("oversized quote out=%v err=%v", out, err)
			}
			if calls != 1 {
				t.Fatalf("quotes=%d, want one server quote", calls)
			}
		})
	}
}

func TestFloatingCreateRechecksQuoteAgainstLaterCredentialsBeforeOrder(t *testing.T) {
	for _, number := range []int{1, 4, 5, 6, 7} {
		t.Run(strconv.Itoa(number), func(t *testing.T) {
			orders := 0
			base := floatingCreateHandler(t, func() string { return floatingSafeRules }, func(w http.ResponseWriter, _ *http.Request) {
				orders++
				w.WriteHeader(202)
				_, _ = w.Write([]byte(`{"data":{"uuid":"server-1"}}`))
			}, nil)
			c, logs, captures := newServerSecurityClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/price") {
					_, _ = w.Write([]byte(strings.Replace(quoteServerFixture, "INSTANCE TYPE", fmt.Sprintf("synthetic-token-%03d", number), 1)))
					return
				}
				base.ServeHTTP(w, r)
			}), 0)
			in := validCreateServerInput()
			in.AttachFloatingIP = true
			in.MaxPrice = 467800
			in.NoWait = true
			out, err := c.CreateServer(context.Background(), in)
			assertNoUnsafeServerOutput(t, out, err)
			if orders != 0 || captures.Load() != 0 || strings.Contains(logs.String(), "synthetic-token-") {
				t.Fatalf("orders=%d captures=%d", orders, captures.Load())
			}
		})
	}
}
