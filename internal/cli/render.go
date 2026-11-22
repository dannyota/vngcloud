package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
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
		return tableWriteError(renderTable(w, result), writeSucceeded)
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

type tableLimitError struct{}

func (tableLimitError) Error() string {
	return "table output exceeds 100 columns, 1000000 cells, or 64 MiB; use --output json"
}

func (e tableLimitError) Unwrap() error { return newUsageError("%s", e.Error()) }

func tableWriteError(err error, writeSucceeded bool) error {
	var limit tableLimitError
	if writeSucceeded && errors.As(err, &limit) {
		return &queryFailedError{withheldMessage: "table output exceeds the limit after the write succeeded; do not rerun the write; use --output json"}
	}
	return err
}

func renderTable(w io.Writer, result any) error {
	source := rowSource(result)
	items, ok := source.([]any)
	if !ok {
		items = []any{source}
	}
	type columnSize struct{ width, extra int }
	sizes := make(map[string]columnSize)
	measure := func(key string, value any) {
		size, exists := sizes[key]
		if !exists {
			header := escapeControlChars(key)
			size.width = utf8.RuneCountInString(header)
			size.extra = len(header) - size.width
		}
		cell := cellText(value)
		width := utf8.RuneCountInString(cell)
		if width > size.width {
			size.width = width
		}
		if extra := len(cell) - width; extra > size.extra {
			size.extra = extra
		}
		sizes[key] = size
	}
	for _, item := range items {
		if m, ok := item.(map[string]any); ok {
			for key, value := range m {
				measure(key, value)
				if len(sizes) > 100 {
					return tableLimitError{}
				}
			}
		} else {
			measure("Value", item)
			if len(sizes) > 100 {
				return tableLimitError{}
			}
		}
	}
	if len(sizes) == 0 && len(items) == 0 {
		measure("Value", nil)
	}
	if len(sizes) > 0 && len(items) > 1000000/len(sizes) {
		return tableLimitError{}
	}
	const maxBytes = 64 << 20
	rowBytes := 2 + 3*len(sizes)
	for _, size := range sizes {
		if size.width > maxBytes-rowBytes || size.extra > maxBytes-rowBytes-size.width {
			return tableLimitError{}
		}
		rowBytes += size.width + size.extra
	}
	if len(items) > maxBytes/rowBytes {
		return tableLimitError{}
	}
	columns := make([]string, 0, len(sizes))
	for key := range sizes {
		columns = append(columns, key)
	}
	sort.Strings(columns)
	headers := make([]string, len(columns))
	widths := make([]int, len(columns))
	indices := make(map[string]int, len(columns))
	for i, key := range columns {
		headers[i] = escapeControlChars(key)
		widths[i] = sizes[key].width
		indices[key] = i
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
