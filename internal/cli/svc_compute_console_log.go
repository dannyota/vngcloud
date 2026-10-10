package cli

import (
	"io"
	"strings"

	"danny.vn/vngcloud/compute"
)

func serverConsoleLogOp() Op[compute.Client] {
	op := Read[compute.Client, compute.GetServerConsoleLogInput, compute.GetServerConsoleLogOutput](
		kebab("GetServerConsoleLog"), (*compute.Client).GetServerConsoleLog)
	op.short = "Read the server console log. Logs can hold passwords and keys."
	op.render = renderServerConsoleLog
	return op
}

// Revealing the log belongs only to this command's stdout path. Query errors
// can quote input values, so retain no underlying error after a failed query.
func renderServerConsoleLog(w io.Writer, format, query string, out any) error {
	log := out.(*compute.GetServerConsoleLogOutput).Log.Reveal()
	if format == outputText && query == "" {
		return writeConsoleLogText(w, log)
	}
	data, err := encodeJSON(struct{ Log string }{Log: log})
	if err != nil {
		return err
	}
	jp, err := compileQuery(query)
	if err != nil {
		return err
	}
	result, err := runQuery(jp, data)
	if err != nil {
		return &queryFailedError{withheldMessage: "console log query failed; result withheld"}
	}
	switch format {
	case outputJSON:
		return renderJSON(w, result)
	case outputTable:
		return renderTable(w, result)
	default:
		if text, ok := result.(string); ok {
			return writeConsoleLogText(w, text)
		}
		return renderText(w, result)
	}
}

func writeConsoleLogText(w io.Writer, log string) error {
	if isTerminal(w) {
		var b strings.Builder
		for _, r := range log {
			if r != '\n' && r != '\t' && isControlRune(r) {
				b.WriteString(escapeControlChars(string(r)))
			} else {
				b.WriteRune(r)
			}
		}
		log = b.String()
	}
	_, err := io.WriteString(w, log)
	return err
}

func init() {
	docOpNotes["compute get-server-console-log"] = "Logs can hold passwords and keys. This read works in read-only profiles and\n" +
		"needs no `--yes`. Output defaults to the profile's setting, otherwise JSON.\n\n" +
		"JSON prints a `Log` string; table output has one Log column. `--output text`\n" +
		"without a query writes the log with no label, quotes, or added newline. Pipes\n" +
		"and files receive the bytes unchanged, including control characters. On a\n" +
		"terminal, controls other than newline and tab are escaped. An empty log\n" +
		"writes zero text bytes.\n\n" +
		"`--query` runs on the revealed `{\"Log\":\"...\"}` object. A text query returning\n" +
		"a string follows the same byte and terminal rules; other results use normal\n" +
		"rendering. Runtime query failures exit 1 with `QueryFailed` and the message\n" +
		"`console log query failed; result withheld`. Logs never reach stderr, errors,\n" +
		"or debug output."
}
