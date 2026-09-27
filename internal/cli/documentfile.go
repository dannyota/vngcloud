package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"danny.vn/vngcloud/iam"
)

// policyDocumentFlagName is the flag create-policy and update-policy
// register through Op.extraFlags: it carries no Input field of its own,
// since Statements is a slice of structs, a type flagSpecsFor never derives a
// flag for.
const policyDocumentFlagName = "document-file"

// maxPolicyDocumentFileSize caps how large a --document-file may be, per the
// IAM writes design: 64 KiB is far more than any real policy document needs,
// so a larger file is refused before it is even parsed.
const maxPolicyDocumentFileSize = 64 * 1024

// registerDocumentFileFlag adds --document-file to cmd, for create-policy's
// and update-policy's Op.extraFlags.
func registerDocumentFileFlag(cmd *cobra.Command) {
	cmd.Flags().String(policyDocumentFlagName, "",
		`path to a JSON policy document: {"statements": [{"effect": "allow", "actions": [...], "resources": [...]}]}`)
}

// policyDocument is --document-file's own JSON shape: the console's JSON
// editor form, {"statements": [...]}. Decoding it with unknown fields
// refused means an AWS-style document (top-level Version and Statement, or a
// statement's own Action and Resource) is refused before any request,
// instead of silently decoding to zero statements.
type policyDocument struct {
	Statements []iam.Statement `json:"statements"`
}

// readPolicyDocumentFile reads and decodes path for --document-file: opened
// without blocking on a FIFO, refused unless it is a regular file, capped at
// maxPolicyDocumentFileSize, and decoded with unknown fields refused. Go's
// json decoder matches field names without regard to case, so both the
// console's own lower-case keys ("statements", "effect") and get-policy's Go
// field names ("Statements", "Effect") decode the same way. Every error is a
// usageError, so a bad --document-file always exits 2 before any request.
func readPolicyDocumentFile(path string) ([]iam.Statement, error) {
	f, err := openRegularFileNonBlocking(path)
	if err != nil {
		return nil, newUsageError("--%s %q: %s", policyDocumentFlagName, path, err)
	}
	defer func() { _ = f.Close() }()

	st, err := f.Stat()
	if err != nil {
		return nil, newUsageError("--%s %q: %s", policyDocumentFlagName, path, err)
	}
	if !st.Mode().IsRegular() {
		return nil, newUsageError("--%s %q is not a regular file", policyDocumentFlagName, path)
	}

	data, err := readAllLimited(f, maxPolicyDocumentFileSize)
	if err != nil {
		return nil, newUsageError("--%s %q: %s", policyDocumentFlagName, path, err)
	}

	var doc policyDocument
	if err := decodeExactlyOneJSONObject(data, &doc); err != nil {
		return nil, newUsageError("--%s %q: %s", policyDocumentFlagName, path, err)
	}
	return doc.Statements, nil
}

// decodeExactlyOneJSONObject decodes data into target with unknown fields
// refused at every level, then reports an error for any non-whitespace data
// left after that one JSON value: a second object, or plain garbage, both of
// which mean data was not exactly one JSON document.
func decodeExactlyOneJSONObject(data []byte, target any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(target); err != nil {
		return err
	}
	var extra json.RawMessage
	switch err := dec.Decode(&extra); {
	case errors.Is(err, io.EOF):
		return nil
	case err == nil:
		return errors.New("unexpected data after the JSON value")
	default:
		return fmt.Errorf("unexpected data after the JSON value: %w", err)
	}
}
