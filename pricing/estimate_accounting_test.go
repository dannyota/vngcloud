package pricing

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"testing"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/testutil"
)

func TestEstimateArithmeticOverflow(t *testing.T) {
	for _, partial := range []bool{false, true} {
		for _, stage := range []string{"multiplication", "subtotal"} {
			for _, extra := range []bool{false, true} {
				t.Run(stage+strconv.FormatBool(partial)+strconv.FormatBool(extra), func(t *testing.T) {
					calls := 0
					cfg := testutil.NewConfig(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						calls++
						var body struct {
							Info struct {
								Type string `json:"type"`
							} `json:"resourceInfo"`
						}
						_ = json.NewDecoder(r.Body).Decode(&body)
						price := 1e308
						if body.Info.Type == "public-mkp" {
							price = 10
						}
						_ = json.NewEncoder(w).Encode(map[string]any{"optimumPrice": price})
					}))
					rows := []EstimatePublicIP{{Key: "large", Kind: "public-vip", Type: "public", Quantity: 2}}
					if stage == "subtotal" {
						rows[0].Quantity = 1
						rows = append(rows, EstimatePublicIP{Key: "large2", Kind: "public-vip", Type: "public-vm", Quantity: 1})
					}
					if extra {
						rows = append(rows, EstimatePublicIP{Key: "small", Kind: "public-vip", Type: "public-mkp", Quantity: 1})
					}
					out, err := New(cfg).Estimate(estimateContext(), &EstimateInput{AllowPartial: partial, PublicIPs: rows})
					var arithmetic *EstimateArithmeticError
					var api *vngcloud.APIError
					if !errors.Is(err, ErrEstimateOverflow) || !errors.As(err, &arithmetic) || arithmetic.Stage != stage || errors.Is(err, vngcloud.ErrInvalidInput) || errors.As(err, &api) {
						t.Fatalf("error %v", err)
					}
					if !partial || (stage == "multiplication" && !extra) {
						if out != nil {
							t.Fatal("unexpected partial output")
						}
						return
					}
					if out == nil || !errors.Is(err, ErrDiscoveryIncomplete) || out.Metadata.Complete || out.Total.RequestedComplete || out.Total.CostComplete || out.Total.RequestedMonthlyTotal != nil {
						t.Fatalf("output %v error %v", out, err)
					}
					if stage == "subtotal" {
						if out.Total.KnownMonthlySubtotal != nil || out.Items[0].Price.Status != "quoted" || out.Items[1].MonthlyAmount == nil {
							t.Fatal("lost subtotal rows")
						}
					} else {
						if *out.Total.KnownMonthlySubtotal != 10 || out.Items[0].Price.MonthlyPrice != nil || len(out.Items[0].Breakdown) != 0 {
							t.Fatal("failed multiplication retained amounts")
						}
					}
					if _, err := json.Marshal(out); err != nil {
						t.Fatal(err)
					}
					if calls != len(rows) {
						t.Fatalf("calls %d", calls)
					}
				})
			}
		}
	}
}
