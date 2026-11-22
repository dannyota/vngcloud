package billing

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"danny.vn/vngcloud/internal/testutil"
)

func TestBudgetAmountExact(t *testing.T) {
	for _, tc := range []struct {
		amount string
		want   int64
		valid  bool
	}{
		{"9007199254740993", 9007199254740993, true},
		{"9223372036854775807.0", 9223372036854775807, true},
		{"-9223372036854775808", -9223372036854775808, true},
		{"1e3", 1000, true},
		{"9223372036854775808", 0, false},
		{"-9223372036854775809", 0, false},
		{"1.00000000000000001", 0, false},
		{"9007199254740993.1", 0, false},
		{"1e999999999", 0, false},
		{"null", 0, false},
	} {
		t.Run(tc.amount, func(t *testing.T) {
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if tc.amount == "9007199254740993" {
					testutil.WriteFixture(t, w, "../testdata/billing/GetBudgetExactAmountSynthetic.json")
					return
				}
				_, _ = w.Write([]byte(`{"code":200,"data":{"uuid":"budget-1","limitAmount":` + tc.amount + `}}`))
			}))
			out, err := c.GetBudget(context.Background(), &GetBudgetInput{BudgetUUID: "budget-1"})
			if !tc.valid {
				if err == nil || out != nil {
					t.Fatal("invalid amount returned a budget")
				}
				return
			}
			if err != nil || out == nil || out.Budget.LimitAmount != tc.want {
				t.Fatal("integral amount was rejected or rounded")
			}
		})
	}
}

func TestBalanceRejectsNonfinite(t *testing.T) {
	for _, amount := range []string{`"NaN"`, `"Inf"`, `"+Inf"`, `"-Inf"`, `"Infinity"`, `"1e999"`} {
		t.Run(amount, func(t *testing.T) {
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(`{"code":200,"data":{"cash":` + amount + `,"poc":null,"cashAvailable":null,"cashHolding":null,"pocHolding":null}}`))
			}))
			out, err := c.GetBalances(context.Background(), nil)
			if err == nil || out != nil {
				t.Fatal("nonfinite balance returned output")
			}
		})
	}
}

func TestAlertRecipientsReplaceState(t *testing.T) {
	var alert Alert
	for _, body := range []string{
		`{"recipients":"[\"synthetic@example.invalid\"]"}`,
		`{"recipients":"unrecognized"}`,
		`{"recipients":"[\"other@example.invalid\"]"}`,
		`{}`,
	} {
		if err := json.Unmarshal([]byte(body), &alert); err != nil {
			t.Fatal(err)
		}
		switch body {
		case `{"recipients":"unrecognized"}`:
			if alert.Recipients != nil || alert.RecipientsRaw != "unrecognized" {
				t.Fatal("fallback retained old recipients")
			}
		case `{}`:
			if alert.Recipients != nil || alert.RecipientsRaw != "" {
				t.Fatal("missing recipients retained old state")
			}
		default:
			if len(alert.Recipients) != 1 || alert.RecipientsRaw != "" {
				t.Fatal("decoded recipients retained old fallback")
			}
		}
	}
}
