package compute

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"

	"danny.vn/vngcloud"
)

func TestFloatingQuoteBodiesOrderAndProvisionalLines(t *testing.T) {
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
		if len(types) == 1 {
			_, _ = w.Write([]byte(quoteServerFixture))
		} else {
			raw, err := os.ReadFile("../testdata/pricing/quote_elastic_ip_zone.json")
			if err != nil {
				t.Error(err)
			}
			_, _ = w.Write(raw)
		}
	}))
	in := &CreateServerInput{ZoneID: "zone-1", FlavorID: "flavor-1", ImageID: "image-1", RootDiskSize: 20, RootDiskTypeID: "type-1", AttachFloatingIP: true, AllowOpenLogin: true, NoWait: true, MaxPrice: math.NaN(), UserData: "bootstrap"}
	out, err := c.QuoteCreateServer(context.Background(), in)
	if err != nil || out == nil {
		t.Fatalf("out=%v err=%v", out, err)
	}
	if strings.Join(types, ",") != "server,elastic-ip" || out.OptimumPrice != 467800 || out.OriginalPrice != 467800 || len(out.Properties) != 3 {
		t.Fatalf("types=%v out=%+v", types, out)
	}
	p := out.Properties[2]
	if p.Name != "FLOATING IP: Elastic IP" || p.Description != "Conservative Elastic IP estimate; extra create charge unconfirmed" || p.CurrentPrice != nil {
		t.Fatalf("property=%+v", p)
	}
}

func TestFloatingQuoteRejectsInvalidComponentsWithoutPartialOutput(t *testing.T) {
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
	} {
		t.Run(tc.name, func(t *testing.T) {
			n := 0
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				n++
				if n == 1 {
					_, _ = w.Write([]byte(quoteServerFixture))
				} else {
					_, _ = w.Write([]byte(tc.body))
				}
			}))
			in := validCreateServerInput()
			in.AttachFloatingIP = true
			out, err := c.QuoteCreateServer(context.Background(), in)
			if err == nil || out != nil {
				t.Fatalf("partial quote out=%v err=%v", out, err)
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

func TestFloatingQuoteRejectsEarlierAndLaterCredentialReflections(t *testing.T) {
	for _, later := range []bool{false, true} {
		t.Run(strconv.FormatBool(later), func(t *testing.T) {
			calls := 0
			c, logs, captures := newServerSecurityClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls++
				if calls == 1 {
					body := quoteServerFixture
					if later {
						body = strings.Replace(body, "INSTANCE TYPE", "synthetic-token-002", 1)
					}
					_, _ = w.Write([]byte(body))
					return
				}
				body := `{"optimumPrice":120000,"originalPrice":120000,"discountPrice":0,"discountPercent":0,"propertiesPrice":[]}`
				if !later {
					body = strings.TrimSuffix(body, "}") + `,"unknown":"synthetic\u002dtoken-001"}`
				}
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

func TestFloatingQuoteEachResponseBound(t *testing.T) {
	for _, part := range []int{1, 2} {
		for _, extra := range []int{0, 1} {
			t.Run(fmt.Sprint(part, extra), func(t *testing.T) {
				calls := 0
				c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					calls++
					body := quoteServerFixture
					if calls == part {
						body += strings.Repeat(" ", (1<<20)-len(body)+extra)
					}
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
			})
		}
	}
}

func TestFloatingCreateRechecksQuoteAgainstLaterCredentialsBeforeOrder(t *testing.T) {
	for _, number := range []int{5, 6, 8} {
		t.Run(strconv.Itoa(number), func(t *testing.T) {
			orders, quotes := 0, 0
			base := floatingCreateHandler(t, func() string { return floatingSafeRules }, func(w http.ResponseWriter, _ *http.Request) {
				orders++
				w.WriteHeader(202)
				_, _ = w.Write([]byte(`{"data":{"uuid":"server-1"}}`))
			}, nil)
			c, logs, captures := newServerSecurityClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/price") {
					quotes++
					if quotes == 1 {
						_, _ = w.Write([]byte(strings.Replace(quoteServerFixture, "INSTANCE TYPE", fmt.Sprintf("synthetic-token-%03d", number), 1)))
					} else {
						raw, err := os.ReadFile("../testdata/pricing/quote_elastic_ip_zone.json")
						if err != nil {
							t.Error(err)
						}
						_, _ = w.Write(raw)
					}
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
