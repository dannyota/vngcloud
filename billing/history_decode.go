package billing

import (
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"time"

	"danny.vn/vngcloud/internal/transport"
)

var errHistoryShape = errors.New("billing history fields are invalid")

type historyEnvelope struct {
	Code json.RawMessage `json:"code"`
	Data json.RawMessage `json:"data"`
}

type historyMetadata struct {
	Number *int `json:"number"`
	Size   *int `json:"size"`
	Pages  *int `json:"totalPages"`
	Total  *int `json:"totalElements"`
}

type historyPage struct {
	Content  []json.RawMessage `json:"content"`
	Metadata *historyMetadata  `json:"pageable"`
}

func decodeHistoryEnvelope(raw json.RawMessage) (json.RawMessage, string, error) {
	var env historyEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, "", transport.SanitizeDecodeError(err)
	}
	if len(env.Code) == 0 {
		return nil, "", errHistoryShape
	}
	if isJSONNull(env.Code) {
		return env.Data, "", nil
	}
	text, ok := transport.ParseErrorCode(env.Code)
	if !ok {
		return nil, "", errHistoryShape
	}
	code, err := strconv.Atoi(text)
	if err != nil {
		return nil, "", errHistoryShape
	}
	if code < 200 || code > 299 {
		return nil, strconv.Itoa(code), nil
	}
	return env.Data, "", nil
}

func decodeHistoryPage(raw json.RawMessage) (*historyPage, error) {
	var page historyPage
	if err := json.Unmarshal(raw, &page); err != nil {
		return nil, transport.SanitizeDecodeError(err)
	}
	if page.Content == nil || page.Metadata == nil {
		return nil, errHistoryShape
	}
	m := page.Metadata
	if m.Number == nil || m.Size == nil || m.Pages == nil || m.Total == nil {
		return nil, errHistoryShape
	}
	return &page, nil
}

func historyRequiredRow(raw json.RawMessage, fields ...string) error {
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(raw, &keys); err != nil {
		return transport.SanitizeDecodeError(err)
	}
	for _, field := range fields {
		if len(keys[field]) == 0 {
			return errHistoryShape
		}
	}
	return nil
}

func historyAmountValid(amount float64) bool { return amount >= 0 && historyFinite(amount) }
func historyFinite(amount float64) bool      { return !math.IsNaN(amount) && !math.IsInf(amount, 0) }
func historyTimestampValid(value string) bool {
	_, err := time.Parse(time.RFC3339Nano, value)
	return err == nil
}

// Presence-aware wire rows follow docs/design/billing-history-api.md.
type historyTransactionWire struct {
	ID          *int64   `json:"transactionID"`
	Type        *int     `json:"transactionType"`
	Credit      *bool    `json:"creditStatus"`
	Amount      *float64 `json:"transactionValue"`
	Balance     *float64 `json:"cumulativeValue"`
	Source      *string  `json:"sourceReference"`
	Description *string  `json:"description"`
	Service     *string  `json:"userServiceReference"`
	Invoice     *int64   `json:"invoiceId"`
	CreatedAt   *string  `json:"createTimeStamp"`
}

func decodeHistoryTransaction(raw json.RawMessage) (Transaction, error) {
	var item Transaction
	if err := historyRequiredRow(raw, "transactionID", "transactionType", "creditStatus", "transactionValue", "cumulativeValue", "sourceReference", "description", "userServiceReference", "invoiceId", "createTimeStamp"); err != nil {
		return item, err
	}
	var wire historyTransactionWire
	if err := json.Unmarshal(raw, &wire); err != nil {
		return item, transport.SanitizeDecodeError(err)
	}
	if wire.ID == nil || wire.Type == nil || wire.Credit == nil || wire.Amount == nil || wire.Balance == nil || wire.CreatedAt == nil {
		return item, errHistoryShape
	}
	if *wire.ID <= 0 || !historyAmountValid(*wire.Amount) || !historyFinite(*wire.Balance) || !historyTimestampValid(*wire.CreatedAt) {
		return item, errHistoryShape
	}
	item = Transaction{ID: *wire.ID, TypeCode: *wire.Type, Amount: *wire.Amount, Balance: *wire.Balance, SourceReference: wire.Source, Description: wire.Description, UserServiceReference: wire.Service, InvoiceID: wire.Invoice, CreatedAt: *wire.CreatedAt, Direction: DirectionDebit}
	if *wire.Credit {
		item.Direction = DirectionCredit
	}
	switch item.TypeCode {
	case 1:
		item.Type = TransactionPurchase
	case 2:
		item.Type = TransactionRefund
	case 5:
		item.Type = TransactionCharge
	}
	if ((item.TypeCode == 1 || item.TypeCode == 2) && !*wire.Credit) || (item.TypeCode == 5 && *wire.Credit) {
		return Transaction{}, errHistoryShape
	}
	return item, nil
}

type historyPaymentWire struct {
	ID            *int64   `json:"paymentID"`
	TransactionID *string  `json:"transactionID"`
	Gateway       *string  `json:"payGate"`
	Status        *string  `json:"paymentStatus"`
	Amount        *float64 `json:"paymentAmount"`
	Type          *int     `json:"paymentType"`
	Description   *string  `json:"description"`
	CreatedAt     *string  `json:"createTimeStamp"`
}

func decodeHistoryPayment(raw json.RawMessage) (Payment, error) {
	var item Payment
	if err := historyRequiredRow(raw, "paymentID", "transactionID", "payGate", "paymentStatus", "paymentAmount", "paymentType", "description", "createTimeStamp"); err != nil {
		return item, err
	}
	var wire historyPaymentWire
	if err := json.Unmarshal(raw, &wire); err != nil {
		return item, transport.SanitizeDecodeError(err)
	}
	if wire.ID == nil || wire.TransactionID == nil || wire.Gateway == nil || wire.Status == nil || wire.Amount == nil || wire.Type == nil || wire.CreatedAt == nil {
		return item, errHistoryShape
	}
	if *wire.ID <= 0 || !historyAmountValid(*wire.Amount) || !historyTimestampValid(*wire.CreatedAt) {
		return item, errHistoryShape
	}
	return Payment{ID: *wire.ID, TypeCode: *wire.Type, Amount: *wire.Amount, TransactionID: *wire.TransactionID, Gateway: *wire.Gateway, Status: *wire.Status, Description: wire.Description, CreatedAt: *wire.CreatedAt}, nil
}
