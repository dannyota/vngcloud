package billing

import "danny.vn/vngcloud/internal/core"

const (
	// TransactionPurchase names observed transaction code 1.
	TransactionPurchase = "PURCHASE"
	// TransactionRefund names observed transaction code 2.
	TransactionRefund = "REFUND"
	// TransactionCharge names observed transaction code 5.
	TransactionCharge = "CHARGE"
	// DirectionCredit marks a wallet credit.
	DirectionCredit = "CREDIT"
	// DirectionDebit marks a wallet debit.
	DirectionDebit = "DEBIT"
	// GatewayBalance is the observed wallet payment gateway.
	GatewayBalance = "use_balance"
)

// Transaction is a wallet ledger row. See docs/design/billing-history-api.md.
type Transaction struct {
	ID                   int64
	Type                 string
	TypeCode             int
	Direction            string
	Amount               float64
	Balance              float64
	SourceReference      *string
	Description          *string
	UserServiceReference *string
	InvoiceID            *int64
	CreatedAt            string
}

// Payment is a payment history row. See docs/design/billing-history-api.md.
type Payment struct {
	ID            int64
	TypeCode      int
	Amount        float64
	TransactionID string
	Gateway       string
	Status        string
	CreatedAt     string
	Description   *string
}

// ListTransactionsInput selects an inclusive wallet history date range.
type ListTransactionsInput struct {
	// StartDate is required in YYYY-MM-DD form, in fixed UTC+07:00.
	StartDate string `vngcloud:"required"`
	// EndDate is inclusive; empty means today in fixed UTC+07:00.
	EndDate string
	// Page is 1-based; nonpositive means 1.
	Page int
	// Size is the page size; nonpositive or above 50 means 50.
	Size int
	// All walks the complete range from page 1; Page above 1 is invalid.
	All bool
}

// ListTransactionsOutput holds wallet rows and 1-based paging metadata.
type ListTransactionsOutput struct{ core.PagedList[Transaction] }

// ListPaymentsInput selects an inclusive payment history date range.
type ListPaymentsInput struct {
	// StartDate is required in YYYY-MM-DD form, in fixed UTC+07:00.
	StartDate string `vngcloud:"required"`
	// EndDate is inclusive; empty means today in fixed UTC+07:00.
	EndDate string
	// Page is 1-based; nonpositive means 1.
	Page int
	// Size is the page size; nonpositive or above 50 means 50.
	Size int
	// All walks the complete range from page 1; Page above 1 is invalid.
	All bool
}

// ListPaymentsOutput holds payment rows and 1-based paging metadata.
type ListPaymentsOutput struct{ core.PagedList[Payment] }
