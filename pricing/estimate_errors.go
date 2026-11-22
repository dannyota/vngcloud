package pricing

import (
	"errors"
	"strconv"

	"danny.vn/vngcloud/internal/core"
)

// ErrEstimateLimit marks an exhausted runtime request or collected-row budget.
// Input bounds instead match vngcloud.ErrInvalidInput. The default request limit
// is 500, its maximum is 2000, and the collected-row maximum is 10000.
var ErrEstimateLimit = errors.New("estimate limit exceeded")

// ErrEstimateOverflow marks non-finite local quantity multiplication or subtotal
// addition. It is neither an input error nor an API refusal.
var ErrEstimateOverflow = errors.New("estimate arithmetic overflow")

// EstimateArithmeticError identifies non-finite local estimate arithmetic without
// exposing amounts. Partial multiplication failures clear the affected row's
// amounts; partial subtotal failures retain rows but clear both sums. See
// docs/design/cost-estimate-api.md for completeness and error semantics.
type EstimateArithmeticError struct {
	// Stage is multiplication or subtotal.
	Stage string
	// ItemKey is the component key for multiplication and empty for subtotal.
	ItemKey string
}

// Error reports the failure without input values or monetary amounts.
func (e *EstimateArithmeticError) Error() string { return "estimate arithmetic overflow" }

// Unwrap exposes ErrEstimateOverflow for errors.Is, including in DiscoveryError.
func (e *EstimateArithmeticError) Unwrap() error { return ErrEstimateOverflow }

func estimateFailureCode(api *core.APIError) string {
	if api.Code == "InvalidResponse" {
		return api.Code
	}
	if api.StatusCode == 200 {
		if code, err := strconv.Atoi(api.Code); err == nil && (code == 112 || code >= 400 && code <= 599) {
			return strconv.Itoa(code)
		}
	}
	return core.ResolvedCode(api.StatusCode, "")
}
