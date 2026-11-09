package cli

import (
	"bytes"
	"io"
	"strings"

	"danny.vn/vngcloud/billing"
)

func billingTransactionsOp() Op[billing.Client] {
	op := Read[billing.Client, billing.ListTransactionsInput, billing.ListTransactionsOutput]("list-transactions", (*billing.Client).ListTransactions)
	op.short = "Read wallet transactions. Output can contain account data."
	op.render = renderBillingHistory
	return op
}

func billingPaymentsOp() Op[billing.Client] {
	op := Read[billing.Client, billing.ListPaymentsInput, billing.ListPaymentsOutput]("list-payments", (*billing.Client).ListPayments)
	op.short = "Read payment history. Output can contain account data."
	op.render = renderBillingHistory
	return op
}

// Buffer the whole report and discard query causes, which can quote account
// values. The SDK's exported output remains the root of explicit queries.
func renderBillingHistory(w io.Writer, format, query string, out any) error {
	var buffer bytes.Buffer
	var err error
	if format == outputTable && query == "" {
		err = renderBillingHistoryTable(&buffer, out)
	} else {
		err = renderOutput(&buffer, format, query, out, false)
	}
	if err != nil {
		return &queryFailedError{withheldMessage: "billing history query failed; result withheld"}
	}
	_, err = w.Write(buffer.Bytes())
	return err
}

func renderBillingHistoryTable(w io.Writer, out any) error {
	data, err := encodeJSON(out)
	if err != nil {
		return err
	}
	value, err := runQuery(nil, data)
	if err != nil {
		return err
	}
	headers := []string{"CreatedAt", "Type", "TypeCode", "Direction", "Amount", "Balance"}
	if _, ok := out.(*billing.ListPaymentsOutput); ok {
		headers = []string{"CreatedAt", "Gateway", "Status", "TypeCode", "Amount"}
	}
	rows := make([][]string, 0)
	if items, ok := rowSource(value).([]any); ok {
		for _, item := range items {
			object, ok := item.(map[string]any)
			if !ok {
				continue
			}
			row := make([]string, len(headers))
			for i, key := range headers {
				row[i] = cellText(object[key])
			}
			rows = append(rows, row)
		}
	}
	widths := make([]int, len(headers))
	for i, key := range headers {
		widths[i] = len([]rune(key))
	}
	for _, row := range rows {
		for i, cell := range row {
			widths[i] = max(widths[i], len([]rune(cell)))
		}
	}
	var table strings.Builder
	writeTableBorder(&table, widths)
	writeTableRow(&table, headers, widths)
	writeTableBorder(&table, widths)
	for _, row := range rows {
		writeTableRow(&table, row, widths)
	}
	writeTableBorder(&table, widths)
	_, err = io.WriteString(w, table.String())
	return err
}
