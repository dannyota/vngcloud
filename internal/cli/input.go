package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"reflect"
	"strings"
)

// maxCLIInputJSONSize caps how much a --cli-input-json file:// value reads,
// so a mistakenly huge file cannot exhaust memory decoding an Input.
const maxCLIInputJSONSize = 1 << 20 // 1 MiB

// applyCLIInputJSON decodes raw into target, which must be a pointer to an
// Input struct. raw is either a literal JSON object or, prefixed with
// file://, a path read from disk (capped at maxCLIInputJSONSize). Keys are
// the Input's own Go field names, matched exactly (case-sensitively): unlike
// encoding/json's own struct decoding, which matches a field
// case-insensitively when no exact match exists, "id" must never set a field
// named ID. Trailing, non-whitespace data after the first JSON value is
// itself a usage error, so a mistake such as `{"Name":"n"}}` (an extra
// closing brace) or two concatenated objects fails loudly instead of the
// first value winning silently. An empty raw is a no-op, so a command with no
// --cli-input-json flag simply skips this step.
func applyCLIInputJSON(raw string, target any) error {
	if raw == "" {
		return nil
	}

	data, err := cliInputJSONBytes(raw)
	if err != nil {
		return err
	}

	dec := json.NewDecoder(bytes.NewReader(data))
	var fields map[string]json.RawMessage
	if err := dec.Decode(&fields); err != nil {
		return newUsageError("--cli-input-json: %s", err)
	}
	if err := rejectTrailingJSON(dec); err != nil {
		return err
	}

	names := exportedFieldNames(target)
	for key := range fields {
		if !names[key] {
			return newUsageError("--cli-input-json: %q is not a field of this operation's input", key)
		}
	}

	body, err := json.Marshal(fields)
	if err != nil {
		return newUsageError("--cli-input-json: %s", err)
	}
	// Every key in body is now known to match a field name exactly, so this
	// final decode cannot fall back to encoding/json's case-insensitive
	// matching for any key that survived the check above.
	if err := json.Unmarshal(body, target); err != nil {
		return newUsageError("--cli-input-json: %s", err)
	}
	return nil
}

// rejectTrailingJSON reports a usage error when dec still has non-whitespace
// data left after its first Decode call: either a second JSON value or plain
// garbage, both of which mean raw was not exactly one JSON object.
func rejectTrailingJSON(dec *json.Decoder) error {
	var extra json.RawMessage
	switch err := dec.Decode(&extra); {
	case errors.Is(err, io.EOF):
		return nil
	case err == nil:
		return newUsageError("--cli-input-json: unexpected data after the JSON value")
	default:
		return newUsageError("--cli-input-json: unexpected data after the JSON value: %s", err)
	}
}

// exportedFieldNames lists every exported top-level field name of the struct
// targetPtr points to, the exact set of keys --cli-input-json may set.
func exportedFieldNames(targetPtr any) map[string]bool {
	t := reflect.TypeOf(targetPtr).Elem()
	names := make(map[string]bool, t.NumField())
	for i := range t.NumField() {
		if f := t.Field(i); f.IsExported() {
			names[f.Name] = true
		}
	}
	return names
}

func cliInputJSONBytes(raw string) ([]byte, error) {
	path, isFile := strings.CutPrefix(raw, "file://")
	if !isFile {
		return []byte(raw), nil
	}

	f, err := os.Open(path) //nolint:gosec // the path comes from a flag the operator typed, exactly like a shell command reading its own argument
	if err != nil {
		return nil, newUsageError("--cli-input-json: %s", err)
	}
	defer func() { _ = f.Close() }()

	limited := io.LimitReader(f, maxCLIInputJSONSize+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, newUsageError("--cli-input-json: %s: %s", path, err)
	}
	if len(data) > maxCLIInputJSONSize {
		return nil, newUsageError("--cli-input-json: %s is larger than %d bytes", path, maxCLIInputJSONSize)
	}
	return data, nil
}
