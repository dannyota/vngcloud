package core

import (
	"errors"
	"math"
	"testing"
)

func TestCheckMaxPriceRejectsUnsafeValues(t *testing.T) {
	for _, tt := range []struct {
		name string
		v    float64
	}{
		{"NaN", math.NaN()},
		{"positive infinity", math.Inf(1)},
		{"negative infinity", math.Inf(-1)},
		{"negative", -1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if err := CheckMaxPrice("op", tt.v); !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("CheckMaxPrice(%v) = %v, want ErrInvalidInput", tt.v, err)
			}
		})
	}
}

func TestCheckMaxPriceAllowsZeroAndPositive(t *testing.T) {
	for _, v := range []float64{0, 1, 1000000} {
		if err := CheckMaxPrice("op", v); err != nil {
			t.Fatalf("CheckMaxPrice(%v) = %v, want nil", v, err)
		}
	}
}

func TestCheckPriceAboveMax(t *testing.T) {
	if err := CheckPriceAboveMax("op", 100, 100); err != nil {
		t.Fatalf("equal price: err = %v, want nil", err)
	}
	if err := CheckPriceAboveMax("op", 99, 100); err != nil {
		t.Fatalf("below max: err = %v, want nil", err)
	}
	err := CheckPriceAboveMax("op", 101, 100)
	if !errors.Is(err, ErrPriceAboveMax) {
		t.Fatalf("above max: err = %v, want ErrPriceAboveMax", err)
	}
	if err.Error() == "" {
		t.Fatal("expected a non-empty message naming both amounts")
	}
}
