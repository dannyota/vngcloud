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
