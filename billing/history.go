package billing

import (
	"context"
	"fmt"
	"time"

	"danny.vn/vngcloud/internal/core"
)

// ErrHistoryIncomplete means a complete history walk could not be established.
var ErrHistoryIncomplete = core.ErrHistoryIncomplete

// ListTransactions reads wallet transactions in API order.
func (c *Client) ListTransactions(ctx context.Context, in *ListTransactionsInput) (*ListTransactionsOutput, error) {
	const op = "billing.ListTransactions"
	if in == nil {
		return nil, fmt.Errorf("%w: %s requires StartDate", core.ErrInvalidInput, op)
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	page, err := listHistory(ctx, c, op, "transactions", *in, decodeHistoryTransaction, func(item Transaction) int64 { return item.ID }, sanitizeHistoryTransaction)
	if err != nil {
		return nil, err
	}
	return &ListTransactionsOutput{PagedList: *page}, nil
}

// ListPayments reads payments in API order without joining wallet transactions.
func (c *Client) ListPayments(ctx context.Context, in *ListPaymentsInput) (*ListPaymentsOutput, error) {
	const op = "billing.ListPayments"
	if in == nil {
		return nil, fmt.Errorf("%w: %s requires StartDate", core.ErrInvalidInput, op)
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	page, err := listHistory(ctx, c, op, "payments", ListTransactionsInput(*in), decodeHistoryPayment, func(item Payment) int64 { return item.ID }, sanitizeHistoryPayment)
	if err != nil {
		return nil, err
	}
	return &ListPaymentsOutput{PagedList: *page}, nil
}
