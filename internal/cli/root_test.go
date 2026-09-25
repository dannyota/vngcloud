package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestMainUnknownCommandIsAUsageError(t *testing.T) {
	withCleanEnv(t)
	var stdout, stderr bytes.Buffer
	code := Main(context.Background(), []string{"frobnicate"}, strings.NewReader(""), &stdout, &stderr)
	if code != 2 {
		t.Fatalf("exit code = %d, want 2; stderr=%q", code, stderr.String())
	}
	assertOneJSONErrorLine(t, stderr.Bytes(), "InvalidUsage")
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty (no usage text)", stdout.String())
	}
}

func TestMainFlagErrorIsAUsageError(t *testing.T) {
	withCleanEnv(t)
	var stdout, stderr bytes.Buffer
	code := Main(context.Background(), []string{"version", "--nope"}, strings.NewReader(""), &stdout, &stderr)
	if code != 2 {
		t.Fatalf("exit code = %d, want 2; stderr=%q", code, stderr.String())
	}
	assertOneJSONErrorLine(t, stderr.Bytes(), "InvalidUsage")
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty (no usage text)", stdout.String())
	}
}

func TestMainNoArgsPrintsHelp(t *testing.T) {
	withCleanEnv(t)
	var stdout, stderr bytes.Buffer
	code := Main(context.Background(), []string{}, strings.NewReader(""), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "vngcloud") {
		t.Fatalf("stdout = %q, want help text naming vngcloud", stdout.String())
	}
}

func TestRootShortMentionsGreenNode(t *testing.T) {
	root := newRootCmd(strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	if want := "Command-line access to GreenNode"; root.Short != want {
		t.Fatalf("Short = %q, want %q", root.Short, want)
	}
}

func TestNoArgsWrapsCobraErrorAsUsageError(t *testing.T) {
	err := noArgs(newRootCmd(strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}), []string{"extra"})
	if err == nil {
		t.Fatalf("expected an error")
	}
	var usageErr usageError
	if !errors.As(err, &usageErr) {
		t.Fatalf("error is not a usageError: %v (%T)", err, err)
	}
}

func assertOneJSONErrorLine(t *testing.T, data []byte, wantCode string) {
	t.Helper()
	if n := bytes.Count(data, []byte("\n")); n != 1 {
		t.Fatalf("expected exactly one line, got %d newlines in %q", n, data)
	}
	var decoded struct {
		Error errorEnvelope `json:"error"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("output is not valid JSON: %v (%q)", err, data)
	}
	if decoded.Error.Code != wantCode {
		t.Fatalf("Code = %q, want %q", decoded.Error.Code, wantCode)
	}
}
