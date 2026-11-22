package pricing

import (
	"encoding/json"
	"math"

	"danny.vn/vngcloud/internal/pricequote"
	"danny.vn/vngcloud/internal/transport"
)

type estimateQuoteResult struct {
	result pricequote.Result
	err    error
}

func estimateRequestKey(req *transport.Request) string {
	body, _ := json.Marshal(req.Body)
	headers, _ := json.Marshal(req.Headers)
	return req.Method + " " + req.URL + " " + string(body) + " " + string(headers)
}

func (d *estimateRun) distinctQuotes() int {
	seen := map[string]bool{}
	for _, req := range d.requests {
		if req != nil {
			seen[estimateRequestKey(req)] = true
		}
	}
	return len(seen)
}

func (d *estimateRun) quote() error {
	memo := map[string]estimateQuoteResult{}
	for i, req := range d.requests {
		if req == nil {
			continue
		}
		key := estimateRequestKey(req)
		cached, ok := memo[key]
		if !ok {
			result, err := pricequote.Quote(d.client, d.session, *req, d.output.Items[i].Kind == "object-storage")
			cached = estimateQuoteResult{result: result, err: err}
			memo[key] = cached
		}
		err := cached.err
		if err == nil {
			err = d.accountItem(i, cached.result)
		}
		if err != nil {
			if !d.input.AllowPartial {
				return err
			}
			d.record(d.output.Items[i].Key, err)
			if d.terminal(err) {
				d.stopped = true
				break
			}
		}
	}
	return nil
}

func (d *estimateRun) accountItem(i int, result pricequote.Result) error {
	item := &d.output.Items[i]
	amount := *result.MonthlyPrice * float64(item.Quantity)
	if math.IsInf(amount, 0) || math.IsNaN(amount) {
		item.Price = DiscoveryPrice{Status: "failed", Source: estimateOperation}
		item.MonthlyAmount = nil
		item.Breakdown = []EstimateCharge{}
		return &EstimateArithmeticError{Stage: "multiplication", ItemKey: item.Key}
	}
	item.Price = DiscoveryPrice(result.Price)
	item.MonthlyAmount = &amount
	item.Breakdown = make([]EstimateCharge, 0, len(result.Breakdown))
	for _, line := range result.Breakdown {
		item.Breakdown = append(item.Breakdown, EstimateCharge{Name: line.Name, MonthlyPrice: line.MonthlyPrice})
	}
	if result.BreakdownMismatch {
		d.output.Metadata.Notes = append(d.output.Metadata.Notes, PricingNote{Topic: "breakdown-mismatch", Scope: item.Key, Text: "breakdown-mismatch", Evidence: "observed"})
	}
	return nil
}
