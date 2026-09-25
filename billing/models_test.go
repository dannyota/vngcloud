package billing

import (
	"encoding/json"
	"testing"
)

// TestBudgetLimitAmountDecode checks the three shapes the API sends
// limitAmount in: an integral decimal (ListBudgets, GetBudget), a plain
// integer (CreateBudget), and a fractional decimal, which is rejected.
func TestBudgetLimitAmountDecode(t *testing.T) {
	t.Run("integral decimal", func(t *testing.T) {
		var b Budget
		raw := `{"uuid":"budget-1","limitAmount":9999999999.0}`
		if err := json.Unmarshal([]byte(raw), &b); err != nil {
			t.Fatalf("Unmarshal() error = %v", err)
		}
		if b.LimitAmount != 9999999999 {
			t.Fatalf("LimitAmount = %d, want 9999999999", b.LimitAmount)
		}
	})

	t.Run("integer", func(t *testing.T) {
		var b Budget
		raw := `{"uuid":"budget-1","limitAmount":2000000000}`
		if err := json.Unmarshal([]byte(raw), &b); err != nil {
			t.Fatalf("Unmarshal() error = %v", err)
		}
		if b.LimitAmount != 2000000000 {
			t.Fatalf("LimitAmount = %d, want 2000000000", b.LimitAmount)
		}
	})

	t.Run("fractional value is rejected", func(t *testing.T) {
		var b Budget
		raw := `{"uuid":"budget-1","limitAmount":1.5}`
		if err := json.Unmarshal([]byte(raw), &b); err == nil {
			t.Fatal("expected an error for a fractional limitAmount, got nil")
		}
	})
}
