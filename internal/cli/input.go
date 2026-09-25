package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strings"
)

// maxCLIInputJSONSize caps how much a --cli-input-json file:// value reads,
// so a mistakenly huge file cannot exhaust memory decoding an Input.
const maxCLIInputJSONSize = 1 << 20 // 1 MiB

// applyCLIInputJSON decodes raw into target, which must be a pointer to an
// Input struct. raw is either a literal JSON object or, prefixed with
// file://, a path read from disk (capped at maxCLIInputJSONSize). Keys are
// the Input's own Go field names; an unrecognized key is a usage error, as
// is any read or decode failure. An empty raw is a no-op, so a command with
// no --cli-input-json flag simply skips this step.
func applyCLIInputJSON(raw string, target any) error {
	if raw == "" {
		return nil
	}

	data, err := cliInputJSONBytes(raw)
	if err != nil {
		return err
	}

	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(target); err != nil {
		return newUsageError("--cli-input-json: %s", err)
	}
	return nil
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
