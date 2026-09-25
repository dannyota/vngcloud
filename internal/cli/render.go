package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
)

const (
	outputJSON  = "json"
	outputTable = "table"
	outputText  = "text"
)

// validOutputFormats names every --output value the CLI accepts.
var validOutputFormats = map[string]bool{outputJSON: true, outputTable: true, outputText: true}

// renderOutput writes out to w in format, filtered by query when non-empty,
// per the CLI design's "Output": json with no query prints the CLI's own
// declaration-ordered encoding directly; every other combination (a query,
// or table/text with or without one) round-trips through a generic decode
// first, so those results print with sorted keys. writeSucceeded is carried
// into a runtime query failure, so its message tells the caller a write
// already went through and must not be retried.
func renderOutput(w io.Writer, format, query string, out any, writeSucceeded bool) error {
	if !validOutputFormats[format] {
		return newUsageError("--output must be json, table, or text, got %q", format)
	}

	data, err := encodeJSON(out)
	if err != nil {
		return err
	}

	jp, err := compileQuery(query)
	if err != nil {
		return err
	}

	if format == outputJSON && jp == nil {
		_, err := fmt.Fprintln(w, string(data))
		return err
	}

	result, err := runQuery(jp, data)
	if err != nil {
		return &queryFailedError{err: err, writeSucceeded: writeSucceeded}
	}

	switch format {
	case outputJSON:
		return renderJSON(w, result)
	case outputTable:
		return renderTable(w, result)
	default:
		return renderText(w, result)
	}
}

// renderJSON prints result (a query result, or the whole generic decode)
// indented, with map keys sorted: encoding/json's own default for a
// map[string]any, which is exactly what a decoded query result is built
// from.
func renderJSON(w io.Writer, result any) error {
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(w, string(data))
	return err
}

// renderText prints one tab-separated line per row of flattenRows(result).
func renderText(w io.Writer, result any) error {
	for _, row := range flattenRows(rowSource(result)) {
		if _, err := fmt.Fprintln(w, strings.Join(row, "\t")); err != nil {
			return err
		}
	}
	return nil
}

// renderTable draws flattenRows(result) as a bordered grid, headed by
// columnHeaders(result).
func renderTable(w io.Writer, result any) error {
	source := rowSource(result)
	headers := columnHeaders(source)
	rows := flattenRows(source)
	if arr, ok := source.([]any); ok && len(arr) == 0 {
		rows = nil
	}

	widths := make([]int, len(headers))
	for i, h := range headers {
		widths[i] = len([]rune(h))
	}
	for _, row := range rows {
		for i, cell := range row {
			if i < len(widths) && len([]rune(cell)) > widths[i] {
				widths[i] = len([]rune(cell))
			}
		}
	}

	var sb strings.Builder
	writeTableBorder(&sb, widths)
	writeTableRow(&sb, headers, widths)
	writeTableBorder(&sb, widths)
	for _, row := range rows {
		writeTableRow(&sb, row, widths)
	}
	writeTableBorder(&sb, widths)
	_, err := io.WriteString(w, sb.String())
	return err
}

func writeTableBorder(sb *strings.Builder, widths []int) {
	sb.WriteByte('+')
	for _, wd := range widths {
		sb.WriteString(strings.Repeat("-", wd+2))
		sb.WriteByte('+')
	}
	sb.WriteByte('\n')
}

func writeTableRow(sb *strings.Builder, cells []string, widths []int) {
	sb.WriteByte('|')
	for i, wd := range widths {
		cell := ""
		if i < len(cells) {
			cell = cells[i]
		}
		sb.WriteByte(' ')
		sb.WriteString(cell)
		sb.WriteString(strings.Repeat(" ", wd-len([]rune(cell))))
		sb.WriteByte(' ')
		sb.WriteByte('|')
	}
	sb.WriteByte('\n')
}

// rowSource picks what flattenRows and columnHeaders treat as the rows to
// render: an array result is used as is, and an object result whose own
// "Items" field is itself an array uses that array, since every SDK list
// operation names its result field Items (core.List and core.PagedList), so
// an object shaped that way is a list result with page metadata alongside
// it. Any other object is left alone and rendered as a single row.
func rowSource(v any) any {
	m, ok := v.(map[string]any)
	if !ok {
		return v
	}
	if items, ok := m["Items"].([]any); ok {
		return items
	}
	return v
}

// flattenRows turns a generic decoded value into rows for text and table
// output: one row per element for an array (the CLI design's "one row per
// list item"), or a single row for an object or a bare scalar.
func flattenRows(v any) [][]string {
	arr, ok := v.([]any)
	if !ok {
		return [][]string{scalarRow(v)}
	}
	rows := make([][]string, 0, len(arr))
	for _, item := range arr {
		rows = append(rows, scalarRow(item))
	}
	return rows
}

// scalarRow renders one item as a row: an object's values in sorted-key
// order, or a single cell for anything else (including a nested array,
// which renderCell falls back to compact JSON for).
func scalarRow(v any) []string {
	m, ok := v.(map[string]any)
	if !ok {
		return []string{cellText(v)}
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	row := make([]string, len(keys))
	for i, k := range keys {
		row[i] = cellText(m[k])
	}
	return row
}

// columnHeaders names table's columns: an object's (or a list's first
// element's) sorted keys, or the single generic column "Value" for a list of
// scalars or a bare scalar result.
func columnHeaders(v any) []string {
	sample := v
	if arr, ok := v.([]any); ok {
		if len(arr) == 0 {
			return []string{"Value"}
		}
		sample = arr[0]
	}
	m, ok := sample.(map[string]any)
	if !ok {
		return []string{"Value"}
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// cellText renders one decoded JSON value (string, float64, bool, nil, or a
// nested object/array) as one table or text cell.
func cellText(v any) string {
	switch val := v.(type) {
	case nil:
		return ""
	case string:
		return val
	case bool:
		return strconv.FormatBool(val)
	case float64:
		return strconv.FormatFloat(val, 'f', -1, 64)
	default:
		data, err := json.Marshal(val)
		if err != nil {
			return fmt.Sprint(val)
		}
		return string(data)
	}
}
