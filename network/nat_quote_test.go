package network

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/testutil"
)

func TestNATQuoteLivePriceFixture(t *testing.T) {
	s := new(natScenario)
	c := natWriteTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/price") {
			testutil.WriteFixture(t, w, "../testdata/network/quote_create_nat.json")
			return
		}
		s.serve(t, w, r)
	}))
	out, err := c.QuoteCreateNATInstance(context.Background(), natTestInput())
	if err != nil {
		t.Fatal(err)
	}
	if out.TotalPrice != 712400 || out.MonthlyPrice != 712400 || out.Currency != "VND" || len(out.Properties) != 1 || s.orders != 0 || s.puts != 0 {
		t.Fatalf("unexpected quote %+v", out)
	}
	if out.Properties[0].CurrentPrice != nil {
		t.Fatal("currentPrice must stay nil")
	}
}

func TestNATQuoteNullablePricesRequirePresence(t *testing.T) {
	for _, tt := range []struct {
		name, old, replacement string
		valid                  bool
	}{
		{"null current price", `"currentPrice":100`, `"currentPrice":null`, true},
		{"missing current price", `"currentPrice":100,`, "", false},
		{"null property discount", `"discountPercent":0`, `"discountPercent":null`, true},
		{"missing property discount", `"discountPercent":0,`, "", false},
		{"missing quote discount", `"discountPercent":null,`, "", false},
		{"null property monthly", `"monthlyPrice":100`, `"monthlyPrice":null`, false},
		{"missing property monthly", `"monthlyPrice":100,`, "", false},
		{"null property optimum", `"propertiesPrice":[{"optimumPrice":100`, `"propertiesPrice":[{"optimumPrice":null`, false},
		{"null name", `"name":"Standard"`, `"name":null`, false},
		{"null description", `"description":"Example"`, `"description":null`, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := new(natScenario)
			c := natWriteTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/price") {
					_, _ = w.Write([]byte(strings.Replace(natTestPrice, tt.old, tt.replacement, 1)))
					return
				}
				s.serve(t, w, r)
			}))
			_, err := c.QuoteCreateNATInstance(context.Background(), natTestInput())
			if (err == nil) != tt.valid || (!tt.valid && vngcloud.ErrorCode(err) != "InvalidResponse") {
				t.Fatalf("%v", err)
			}
		})
	}
}
