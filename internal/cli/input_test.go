package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type jsonTestInput struct {
	Name  string
	Count int
}

func TestApplyCLIInputJSONEmptyIsNoOp(t *testing.T) {
	in := &jsonTestInput{Name: "unchanged"}
	if err := applyCLIInputJSON("", in); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if in.Name != "unchanged" {
		t.Fatalf("Name = %q, want unchanged", in.Name)
	}
}

func TestApplyCLIInputJSONLiteral(t *testing.T) {
	in := &jsonTestInput{}
	if err := applyCLIInputJSON(`{"Name":"n","Count":3}`, in); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if in.Name != "n" || in.Count != 3 {
		t.Fatalf("got %+v", in)
	}
}

func TestApplyCLIInputJSONUnknownKeyIsAUsageError(t *testing.T) {
	in := &jsonTestInput{}
	err := applyCLIInputJSON(`{"NotAField":"typo"}`, in)
	if err == nil {
		t.Fatalf("expected an error for an unknown key")
	}
	if exitCode(err) != 2 {
		t.Fatalf("exitCode = %d, want 2", exitCode(err))
	}
}

type caseTestInput struct {
	ID string
}

func TestApplyCLIInputJSONKeysAreCaseSensitive(t *testing.T) {
	in := &caseTestInput{}
	// encoding/json's own struct decoding would match "id" to ID when no
	// exact-case field exists; --cli-input-json must not fall back to that.
	err := applyCLIInputJSON(`{"id":"lowercase"}`, in)
	if err == nil {
		t.Fatalf("expected an error for a key that only matches case-insensitively")
	}
	if exitCode(err) != 2 {
		t.Fatalf("exitCode = %d, want 2", exitCode(err))
	}
	if in.ID != "" {
		t.Fatalf("ID = %q, want unchanged", in.ID)
	}
}

func TestApplyCLIInputJSONExactCaseMatchWorks(t *testing.T) {
	in := &caseTestInput{}
	if err := applyCLIInputJSON(`{"ID":"exact"}`, in); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if in.ID != "exact" {
		t.Fatalf("ID = %q, want exact", in.ID)
	}
}

func TestApplyCLIInputJSONTrailingGarbageIsAUsageError(t *testing.T) {
	in := &jsonTestInput{}
	err := applyCLIInputJSON(`{"Name":"n"}garbage`, in)
	if err == nil {
		t.Fatalf("expected an error for trailing data after the JSON value")
	}
	if exitCode(err) != 2 {
		t.Fatalf("exitCode = %d, want 2", exitCode(err))
	}
}

func TestApplyCLIInputJSONTrailingSecondValueIsAUsageError(t *testing.T) {
	in := &jsonTestInput{}
	err := applyCLIInputJSON(`{"Name":"n"}{"Count":1}`, in)
	if err == nil {
		t.Fatalf("expected an error for a second JSON value")
	}
	if exitCode(err) != 2 {
		t.Fatalf("exitCode = %d, want 2", exitCode(err))
	}
}

func TestApplyCLIInputJSONTrailingWhitespaceIsFine(t *testing.T) {
	in := &jsonTestInput{}
	if err := applyCLIInputJSON("{\"Name\":\"n\"}\n  \t", in); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if in.Name != "n" {
		t.Fatalf("Name = %q, want n", in.Name)
	}
}

func TestApplyCLIInputJSONFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "input.json")
	if err := os.WriteFile(path, []byte(`{"Name":"from-file"}`), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	in := &jsonTestInput{}
	if err := applyCLIInputJSON("file://"+path, in); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if in.Name != "from-file" {
		t.Fatalf("Name = %q, want from-file", in.Name)
	}
}

func TestApplyCLIInputJSONFileMissing(t *testing.T) {
	in := &jsonTestInput{}
	err := applyCLIInputJSON("file:///no/such/file.json", in)
	if err == nil {
		t.Fatalf("expected an error for a missing file")
	}
	if exitCode(err) != 2 {
		t.Fatalf("exitCode = %d, want 2", exitCode(err))
	}
}

func TestApplyCLIInputJSONFileSizeCap(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "big.json")
	// One byte over the cap, wrapped in a JSON string so it would otherwise
	// decode cleanly if the cap did not apply.
	big := `{"Name":"` + strings.Repeat("a", maxCLIInputJSONSize) + `"}`
	if err := os.WriteFile(path, []byte(big), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	in := &jsonTestInput{}
	err := applyCLIInputJSON("file://"+path, in)
	if err == nil {
		t.Fatalf("expected an error for a file over the %d byte cap", maxCLIInputJSONSize)
	}
	if exitCode(err) != 2 {
		t.Fatalf("exitCode = %d, want 2", exitCode(err))
	}
}

func TestApplyCLIInputJSONFileUnderCapSucceeds(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ok.json")
	content := `{"Name":"` + strings.Repeat("a", maxCLIInputJSONSize-100) + `"}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	in := &jsonTestInput{}
	if err := applyCLIInputJSON("file://"+path, in); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// nestedTestInput has a nested struct field whose own field carries a wire
// tag that differs from its Go name, the same shape as
// monitor.CheckNotifications.InAlarm ("In-alarm"), so a test here can check
// applyCLIInputJSON's nested-key handling without depending on monitor.
type nestedTestInput struct {
	Group nestedGroup
}

type nestedGroup struct {
	Members []string `json:"member-list"`
}

func TestApplyCLIInputJSONNestedUnknownKeyIsAUsageError(t *testing.T) {
	in := &nestedTestInput{}
	// "Members" is the Go field name, not its wire tag "member-list": before
	// the fix, the final decode was a plain json.Unmarshal, which ignores an
	// unknown key inside a nested struct instead of refusing it, so this key
	// silently set nothing.
	err := applyCLIInputJSON(`{"Group":{"Members":["a"]}}`, in)
	if err == nil {
		t.Fatalf("expected an error for an unknown key inside a nested struct")
	}
	if exitCode(err) != 2 {
		t.Fatalf("exitCode = %d, want 2", exitCode(err))
	}
	if len(in.Group.Members) != 0 {
		t.Fatalf("Group.Members = %v, want unchanged", in.Group.Members)
	}
}

func TestApplyCLIInputJSONNestedWireTagKeyWorks(t *testing.T) {
	in := &nestedTestInput{}
	if err := applyCLIInputJSON(`{"Group":{"member-list":["a","b"]}}`, in); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(in.Group.Members) != 2 || in.Group.Members[0] != "a" || in.Group.Members[1] != "b" {
		t.Fatalf("Group.Members = %v, want [a b]", in.Group.Members)
	}
}
