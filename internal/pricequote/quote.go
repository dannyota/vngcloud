//revive:disable:exported Internal contracts live in docs/design/cost-estimate.md.
package pricequote

import (
	"encoding/json"
	"math"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/discovery"
	"danny.vn/vngcloud/internal/pricecatalog"
	"danny.vn/vngcloud/internal/transport"
)

type Price struct {
	Status, Basis, Currency, Tax, Source                        string
	MonthlyPrice, OriginalPrice, DiscountPrice, DiscountPercent *float64
}

type Charge struct {
	Name         string
	MonthlyPrice *float64
}

type Result struct {
	Price
	Breakdown         []Charge
	BreakdownMismatch bool
}

func invalid(op string, status int) error {
	return &core.APIError{Operation: op, StatusCode: status, Code: "InvalidResponse", Message: "invalid discovery response; body withheld"}
}

func DecodeMonthly(op string, status int, raw json.RawMessage) (Price, error) {
	if err := ValidateResponse(op, status, raw); err != nil {
		return Price{}, err
	}
	var value struct {
		Optimum  *float64 `json:"optimumPrice"`
		Original *float64 `json:"originalPrice"`
		Discount *float64 `json:"discountPrice"`
		Percent  *float64 `json:"discountPercent"`
		Currency string   `json:"currency"`
	}
	if json.Unmarshal(raw, &value) != nil || value.Optimum == nil {
		return Price{}, invalid(op, status)
	}
	for _, p := range []*float64{value.Optimum, value.Original, value.Discount, value.Percent} {
		if p != nil && (*p < 0 || math.IsNaN(*p) || math.IsInf(*p, 0)) {
			return Price{}, invalid(op, status)
		}
	}
	if *value.Optimum <= 0 {
		return Price{}, invalid(op, status)
	}
	if value.Currency == "" {
		value.Currency = "VND"
	}
	return Price{Status: "quoted", Basis: "monthly-total", Currency: value.Currency, Tax: "as-returned", Source: op, MonthlyPrice: value.Optimum, OriginalPrice: value.Original, DiscountPrice: value.Discount, DiscountPercent: value.Percent}, nil
}

func DecodeInstance(op string, status int, raw json.RawMessage) (Price, error) {
	if err := ValidateResponse(op, status, raw); err != nil {
		return Price{}, err
	}
	if err := validateEnvelope(op, status, raw); err != nil {
		return Price{}, err
	}
	var value struct {
		Properties []struct {
			Name    string   `json:"name"`
			Monthly *float64 `json:"monthlyPrice"`
			Optimum *float64 `json:"optimumPrice"`
			Percent *float64 `json:"discountPercent"`
		} `json:"propertiesPrice"`
	}
	if json.Unmarshal(raw, &value) != nil {
		return Price{}, invalid(op, status)
	}
	price := Price{Status: "quoted", Basis: "instance-type-line", Currency: "VND", Tax: "as-returned", Source: op}
	matches := 0
	for _, line := range value.Properties {
		if line.Name != "INSTANCE TYPE" {
			continue
		}
		matches++
		if line.Monthly == nil || line.Optimum == nil || *line.Monthly <= 0 || *line.Monthly != *line.Optimum {
			return Price{}, invalid(op, status)
		}
		for _, amount := range []*float64{line.Monthly, line.Optimum, line.Percent} {
			if amount != nil && (*amount < 0 || math.IsNaN(*amount) || math.IsInf(*amount, 0)) {
				return Price{}, invalid(op, status)
			}
		}
		price.MonthlyPrice = line.Monthly
		price.DiscountPercent = line.Percent
	}
	if matches != 1 {
		return Price{}, invalid(op, status)
	}
	return price, nil
}

func validateEnvelope(op string, status int, raw json.RawMessage) error {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		return invalid(op, status)
	}
	for _, name := range []string{"page", "paging", "pagination", "pageSize", "totalPage", "totalPages", "totalItem", "next", "nextPage", "isNext", "hasMore", "continuationToken"} {
		if _, ok := fields[name]; ok {
			return invalid(op, status)
		}
	}
	if success, ok := fields["success"]; ok && string(success) != "true" {
		return invalid(op, status)
	}
	for _, name := range []string{"errorCode", "errorMsg"} {
		if value, ok := fields[name]; ok && string(value) != "null" && string(value) != `""` {
			return invalid(op, status)
		}
	}
	return nil
}

type estimateFields struct {
	pricecatalog.EnvelopeFields
	Optimum    *float64        `json:"optimumPrice"`
	Original   *float64        `json:"originalPrice"`
	Discount   *float64        `json:"discountPrice"`
	Percent    *float64        `json:"discountPercent"`
	Currency   string          `json:"currency"`
	Success    *bool           `json:"success"`
	Data       *estimateFields `json:"data"`
	Properties []struct {
		Name        string          `json:"name"`
		Monthly     *float64        `json:"monthlyPrice"`
		Optimum     json.RawMessage `json:"optimumPrice"`
		Current     json.RawMessage `json:"currentPrice"`
		Percent     json.RawMessage `json:"discountPercent"`
		Description json.RawMessage `json:"description"`
	} `json:"propertiesPrice"`
}

func DecodeEstimate(op string, status int, raw json.RawMessage, storage bool) (Result, error) {
	fail := func() (Result, error) { return Result{}, invalid(op, status) }
	if pricecatalog.ValidateFields(raw, &estimateFields{}) != nil {
		return fail()
	}
	if storage {
		var envelope struct {
			Success *bool           `json:"success"`
			Data    json.RawMessage `json:"data"`
			IsNext  bool            `json:"isNext"`
		}
		if json.Unmarshal(raw, &envelope) != nil || envelope.Success == nil || !*envelope.Success || envelope.IsNext {
			return fail()
		}
		var fields map[string]json.RawMessage
		if json.Unmarshal(raw, &fields) != nil {
			return fail()
		}
		for _, name := range []string{"page", "paging", "pagination", "pageSize", "totalPage", "totalPages", "totalItem", "next", "nextPage", "hasMore", "continuationToken"} {
			if _, ok := fields[name]; ok {
				return fail()
			}
		}
		raw = envelope.Data
	}
	if err := validateEnvelope(op, status, raw); err != nil {
		return Result{}, err
	}
	price, err := DecodeMonthly(op, status, raw)
	if err != nil {
		return Result{}, err
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return fail()
	}
	if currency, ok := fields["currency"]; ok {
		var value string
		if json.Unmarshal(currency, &value) != nil || value != "VND" {
			return fail()
		}
	}
	var rows []json.RawMessage
	if value, ok := fields["propertiesPrice"]; ok && json.Unmarshal(value, &rows) != nil {
		return fail()
	}
	result := Result{Price: price, Breakdown: make([]Charge, 0, len(rows))}
	sum := 0.0
	known := false
	for _, row := range rows {
		var line *struct {
			Name    string   `json:"name"`
			Monthly *float64 `json:"monthlyPrice"`
		}
		if json.Unmarshal(row, &line) != nil || line == nil {
			return fail()
		}
		if line.Monthly != nil {
			if *line.Monthly < 0 || math.IsNaN(*line.Monthly) || math.IsInf(*line.Monthly, 0) {
				return fail()
			}
			known = true
			sum += *line.Monthly
		}
		result.Breakdown = append(result.Breakdown, Charge{Name: line.Name, MonthlyPrice: line.Monthly})
	}
	result.BreakdownMismatch = known && sum != *price.MonthlyPrice
	return result, nil
}

func Quote(c *core.Client, s *discovery.Session, req transport.Request, storage bool) (Result, error) {
	var result Result
	_, err := s.DoJSON(c, req, nil, func(status int, raw json.RawMessage) error {
		if storage {
			var env pricecatalog.StorageEnvelope
			if pricecatalog.ValidateFields(raw, &estimateFields{}) != nil || json.Unmarshal(raw, &env) != nil || env.Success == nil {
				return invalid(req.Operation, status)
			}
			if !*env.Success {
				return pricecatalog.SafeStorageRefusal(c, req.Operation, status, &env)
			}
		}
		var err error
		result, err = DecodeEstimate(req.Operation, status, raw, storage)
		if err != nil {
			return err
		}
		return s.AddRows(len(result.Breakdown))
	})
	if err != nil {
		return Result{}, err
	}
	return result, nil
}

func ValidateResponse(op string, status int, raw json.RawMessage) error {
	if pricecatalog.ValidateFields(raw, &estimateFields{}) != nil {
		return invalid(op, status)
	}
	return nil
}
