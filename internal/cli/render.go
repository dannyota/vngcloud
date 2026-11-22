package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"reflect"
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
//
// redactMaps runs on out before anything is encoded, per the CLI reads
// design's "Key redaction for map-backed Outputs": every output format, and
// a --query result, are all built from the same encodeJSON(out) below, so
// one redaction pass ahead of it covers every combination.
func renderOutput(w io.Writer, format, query string, out any, writeSucceeded bool) error {
	if !validOutputFormats[format] {
		return newUsageError("--output must be json, table, or text, got %q", format)
	}

	redactMaps(reflect.ValueOf(out))

	data, err := encodeJSON(out)
	if err != nil {
		return err
	}

	jp, err := compileQuery(query)
	if err != nil {
		return err
	}

	if format == outputJSON && jp == nil {
		_, err := fmt.Fprintln(w, escapeJSONControlChars(string(data)))
		return err
	}

	result, err := runQuery(jp, data)
	if err != nil {
		return &queryFailedError{writeSucceeded: writeSucceeded}
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
	_, err = fmt.Fprintln(w, escapeJSONControlChars(string(data)))
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

func renderTable(w io.Writer, result any) error {
	source := rowSource(result)
	items, ok := source.([]any)
	if !ok {
		items = []any{source}
	}
	keys := make(map[string]bool)
	for _, item := range items {
		if m, ok := item.(map[string]any); ok {
			for key := range m {
				keys[key] = true
				if len(keys) > 100 || len(items) > 1000000/len(keys) {
					return newUsageError("table output exceeds 100 columns or 1000000 cells; use --output json")
				}
			}
		} else {
			keys["Value"] = true
		}
	}
	if len(keys) == 0 && len(items) == 0 {
		keys["Value"] = true
	}
	if len(keys) > 0 && len(items) > 1000000/len(keys) {
		return newUsageError("table output exceeds 100 columns or 1000000 cells; use --output json")
	}
	columns := make([]string, 0, len(keys))
	for key := range keys {
		columns = append(columns, key)
	}
	sort.Strings(columns)
	headers := make([]string, len(columns))
	for i, key := range columns {
		headers[i] = escapeControlChars(key)
	}
	widths := make([]int, len(headers))
	indices := make(map[string]int, len(columns))
	for i, h := range headers {
		widths[i] = len([]rune(h))
		indices[columns[i]] = i
	}
	for _, item := range items {
		if m, ok := item.(map[string]any); ok {
			for key, value := range m {
				i := indices[key]
				if width := len([]rune(cellText(value))); width > widths[i] {
					widths[i] = width
				}
			}
		} else {
			i := indices["Value"]
			if width := len([]rune(cellText(item))); width > widths[i] {
				widths[i] = width
			}
		}
	}

	var sb strings.Builder
	flush := func() error {
		_, err := io.WriteString(w, sb.String())
		sb.Reset()
		return err
	}
	writeTableBorder(&sb, widths)
	writeTableRow(&sb, headers, widths)
	writeTableBorder(&sb, widths)
	if err := flush(); err != nil {
		return err
	}
	row := make([]string, len(columns))
	for _, item := range items {
		clear(row)
		if m, ok := item.(map[string]any); ok {
			for key, value := range m {
				row[indices[key]] = cellText(value)
			}
		} else {
			row[indices["Value"]] = cellText(item)
		}
		writeTableRow(&sb, row, widths)
		if err := flush(); err != nil {
			return err
		}
	}
	writeTableBorder(&sb, widths)
	return flush()
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

// cellText renders one decoded JSON value (string, float64, bool, nil, or a
// nested object/array) as one table or text cell.
func cellText(v any) string {
	switch val := v.(type) {
	case nil:
		return ""
	case string:
		return escapeControlChars(val)
	case bool:
		return strconv.FormatBool(val)
	case float64:
		return strconv.FormatFloat(val, 'f', -1, 64)
	default:
		data, err := json.Marshal(val)
		if err != nil {
			return fmt.Sprint(val)
		}
		return escapeJSONControlChars(string(data))
	}
}

// escapeControlChars makes terminal controls visible in text and table cells.
func escapeControlChars(s string) string {
	if !strings.ContainsFunc(s, isControlRune) {
		return s
	}
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '\t':
			b.WriteString(`\t`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		default:
			if isControlRune(r) {
				if r <= 0xff {
					fmt.Fprintf(&b, `\x%02x`, r)
				} else {
					fmt.Fprintf(&b, `\u%04x`, r)
				}
			} else {
				b.WriteRune(r)
			}
		}
	}
	return b.String()
}

func isControlRune(r rune) bool {
	return r < 0x20 || (r >= 0x7f && r <= 0x9f) ||
		r == 0x061c || r == 0x200e || r == 0x200f ||
		(r >= 0x202a && r <= 0x202e) || (r >= 0x2066 && r <= 0x2069)
}

// escapeJSONControlChars processes encoded JSON. C0 controls inside strings
// are already escaped; literal newlines and tabs belong to JSON whitespace.
func escapeJSONControlChars(s string) string {
	unsafe := func(r rune) bool { return r >= 0x7f && isControlRune(r) }
	if !strings.ContainsFunc(s, unsafe) {
		return s
	}
	var b strings.Builder
	for _, r := range s {
		if unsafe(r) {
			fmt.Fprintf(&b, `\u%04x`, r)
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}
