package core

import (
	"fmt"
	"math"
)

// CheckMaxPrice returns an error wrapping ErrInvalidInput when maxPrice is
// NaN, +Inf, -Inf, or negative: a paid write's price guard
// (quoted > maxPrice) cannot compare any of those safely. NaN compares
// false against every quote, which would silently disable the guard rather
// than block an overpriced order; +Inf and -Inf are never a real budget;
// and a negative value can never be exceeded by a live quote, the same
// effective hole as NaN. Every paid write calls this before any request,
// including its own quote.
func CheckMaxPrice(op string, maxPrice float64) error {
	if math.IsNaN(maxPrice) || math.IsInf(maxPrice, 0) || maxPrice < 0 {
		return fmt.Errorf("%w: %s: MaxPrice must be a non-negative, finite number, got %v", ErrInvalidInput, op, maxPrice)
	}
	return nil
}

// CheckPriceAboveMax returns an error wrapping ErrPriceAboveMax naming both
// amounts when quoted is above maxPrice. Nothing is sent when this returns
// non-nil.
func CheckPriceAboveMax(op string, quoted, maxPrice float64) error {
	if quoted > maxPrice {
		return fmt.Errorf("%w: %s: quote %.0f VND exceeds MaxPrice %.0f VND", ErrPriceAboveMax, op, quoted, maxPrice)
	}
	return nil
}
