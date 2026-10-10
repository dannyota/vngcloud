package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
)

func consoleLogFixture(t *testing.T, log string, status int) *svcFixture {
	t.Helper()
	body, err := json.Marshal(map[string]string{"data": log})
	if err != nil {
		t.Fatal(err)
	}
	return newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/servers/s-1/console-log": func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet || r.URL.RawQuery != "" {
				t.Errorf("request = %s %s", r.Method, r.URL)
			}
			w.WriteHeader(status)
			_, _ = w.Write(body)
		},
	})
}

func TestComputeConsoleLogOutput(t *testing.T) {
	tests := []struct {
		name, log, format, query, want string
		terminal                       bool
	}{
		{name: "raw", log: "synthetic-marker", format: "text", want: "synthetic-marker"},
		{name: "newline", log: "line\n", format: "text", want: "line\n"},
		{name: "controls", log: "\x00\x1b[31m\r\x7f\n\t", format: "text", want: "\x00\x1b[31m\r\x7f\n\t"},
		{name: "terminal", log: "\x00\x1b[31m\r\x7f\n\t", format: "text", want: `\x00\x1b[31m\r\x7f` + "\n\t", terminal: true},
		{name: "unicode-terminal", log: "Việt Nam\u009b2J\u0085\u202e\u2066\x7f\n\t", format: "text", want: `Việt Nam\x9b2J\x85\u202e\u2066\x7f` + "\n\t", terminal: true},
		{name: "unicode-terminal-query", log: "Việt Nam\u009b2J\u0085\u202e\u2066\x7f\n\t", format: "text", query: "Log", want: `Việt Nam\x9b2J\x85\u202e\u2066\x7f` + "\n\t", terminal: true},
		{name: "empty", format: "text"},
		{name: "empty-terminal", format: "text", terminal: true},
		{name: "json", log: "\x1b\n\t", format: "json", want: "{\n  \"Log\": \"\\u001b\\n\\t\"\n}\n"},
		{name: "default-json", log: "synthetic-marker", want: "{\n  \"Log\": \"synthetic-marker\"\n}\n"},
		{name: "empty-json", format: "json", want: "{\n  \"Log\": \"\"\n}\n"},
		{name: "table", log: "\x1b\n\t", format: "table", want: "+----------+\n| Log      |\n+----------+\n| \\x1b\\n\\t |\n+----------+\n"},
		{name: "empty-table", format: "table", want: "+-----+\n| Log |\n+-----+\n|     |\n+-----+\n"},
		{name: "string-query", log: "line\x1b\n", format: "text", query: "Log", want: "line\x1b\n"},
		{name: "terminal-query", log: "line\x1b\n", format: "text", query: "Log", want: "line\\x1b\n", terminal: true},
		{name: "number-query", log: "line", format: "text", query: "length(Log)", want: "4\n"},
		{name: "array-query", log: "line\n", format: "text", query: "[Log]", want: "line\\n\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixture := consoleLogFixture(t, tt.log, http.StatusOK)
			root, stdout, stderr := newSvcRoot(t, fixture)
			prev := isTerminalOverride
			isTerminalOverride = func(stream any) bool {
				if stream != stdout {
					t.Error("terminal detection did not use stdout")
				}
				return tt.terminal
			}
			t.Cleanup(func() { isTerminalOverride = prev })
			args := []string{"--region", "hcm-3", "--project-id", "proj-1", "--debug", "--read-only", "compute", "get-server-console-log", "--server-id", "s-1"}
			if tt.format != "" {
				args = append(args, "--output", tt.format)
			}
			if tt.query != "" {
				args = append(args, "--query", tt.query)
			}
			root.SetArgs(args)
			if err := root.ExecuteContext(context.Background()); err != nil {
				t.Fatalf("execute: %v", err)
			}
			if stdout.String() != tt.want {
				t.Fatalf("stdout = %q, want %q", stdout.String(), tt.want)
			}
			if strings.Contains(stderr.String(), "synthetic-marker") || strings.Contains(stderr.String(), "line") || strings.Contains(stderr.String(), "[31m") {
				t.Fatalf("log reached stderr: %q", stderr.String())
			}
			if !strings.Contains(stderr.String(), "console-log") {
				t.Fatalf("debug request missing: %s", stderr.String())
			}
		})
	}
}

func TestComputeConsoleLogErrors(t *testing.T) {
	tests := []struct {
		name, query   string
		missing       bool
		status, exit  int
		code, message string
	}{
		{name: "missing", missing: true, status: 200, exit: 2, code: "InvalidUsage"},
		{name: "not-found", status: 404, exit: 4, code: "NotFound"},
		{name: "runtime-query", query: "abs(Log)", status: 200, exit: 1, code: "QueryFailed", message: "console log query failed; result withheld"},
		{name: "syntax-query", query: "[", status: 200, exit: 2, code: "InvalidUsage"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixture := consoleLogFixture(t, "synthetic-marker", tt.status)
			root, stdout, stderr := newSvcRoot(t, fixture)
			args := []string{"--region", "hcm-3", "--project-id", "proj-1", "--debug", "compute", "get-server-console-log"}
			if !tt.missing {
				args = append(args, "--server-id", "s-1")
			}
			if tt.query != "" {
				args = append(args, "--query", tt.query)
			}
			root.SetArgs(args)
			err := root.ExecuteContext(context.Background())
			if err == nil {
				t.Fatal("expected error")
			}
			if exitCode(err) != tt.exit || classify(err).Code != tt.code {
				t.Fatalf("error = %v, exit = %d, code = %s", err, exitCode(err), classify(err).Code)
			}
			if tt.name == "runtime-query" {
				var queryErr *queryFailedError
				if !errors.As(err, &queryErr) {
					t.Fatalf("error type = %T", err)
				}
				if queryErr.err != nil || errors.Unwrap(queryErr) != nil {
					t.Fatal("query error retained an underlying error")
				}
				for current := fmt.Errorf("command: %w", err); current != nil; current = errors.Unwrap(current) {
					if strings.Contains(current.Error(), "synthetic-marker") || strings.Contains(current.Error(), "JMESPath") || strings.Contains(current.Error(), "Invalid type") {
						t.Fatal("wrapped error leaked query details")
					}
				}
			}
			if tt.message != "" && classify(err).Message != tt.message {
				t.Fatalf("message = %q", classify(err).Message)
			}
			if stdout.Len() != 0 || strings.Contains(err.Error()+stderr.String(), "synthetic-marker") {
				t.Fatalf("result leaked: stdout=%q stderr=%q err=%v", stdout.String(), stderr.String(), err)
			}
			if tt.exit == 2 && fixture.requestCount() != 0 {
				t.Fatal("usage error sent a request")
			}
		})
	}
}

func TestComputeConsoleLogInputJSONAndProfile(t *testing.T) {
	fixture := consoleLogFixture(t, "synthetic-marker", http.StatusOK)
	root, stdout, _ := newSvcRoot(t, fixture)
	home := withCleanEnv(t)
	writeConfigFile(t, home, "[profile agent]\nregion = hcm-3\nread_only = true\noutput = text\n")
	root.SetArgs([]string{"--profile", "agent", "--project-id", "proj-1", "compute", "get-server-console-log", "--cli-input-json", `{"ServerID":"s-1"}`})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	if stdout.String() != "synthetic-marker" {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestComputeConsoleLogHelp(t *testing.T) {
	fixture := consoleLogFixture(t, "", http.StatusOK)
	root, stdout, _ := newSvcRoot(t, fixture)
	root.SetArgs([]string{"compute", "get-server-console-log", "--help"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "passwords and keys") {
		t.Fatalf("help = %s", stdout.String())
	}
	if fixture.requestCount() != 0 {
		t.Fatal("help sent a request")
	}
}

func TestComputeConsoleLogPipe(t *testing.T) {
	for _, log := range []string{"", "synthetic-marker", "synthetic-marker\n", "\x00\x1b[31m\r\x7f\n\t", "Việt Nam\u009b2J\u0085\u202e\u2066\x7f\n\t"} {
		t.Run(log, func(t *testing.T) {
			fixture := consoleLogFixture(t, log, http.StatusOK)
			_, _, stderr := newSvcRoot(t, fixture)
			reader, writer, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = reader.Close(); _ = writer.Close() })
			code := Main(context.Background(), []string{"--region", "hcm-3", "--project-id", "proj-1", "compute", "get-server-console-log", "--server-id", "s-1", "--output", "text"}, strings.NewReader(""), writer, stderr)
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
			data, err := io.ReadAll(reader)
			if err != nil {
				t.Fatal(err)
			}
			if code != 0 || string(data) != log || stderr.Len() != 0 {
				t.Fatalf("exit=%d stdout=%q stderr=%q", code, data, stderr.String())
			}
		})
	}
}

func TestComputeConsoleLogQueryStderr(t *testing.T) {
	fixture := consoleLogFixture(t, "synthetic-marker", http.StatusOK)
	_, stdout, stderr := newSvcRoot(t, fixture)
	code := Main(context.Background(), []string{"--region", "hcm-3", "--project-id", "proj-1", "compute", "get-server-console-log", "--server-id", "s-1", "--query", "abs(Log)"}, strings.NewReader(""), stdout, stderr)
	const want = "{\"error\":{\"code\":\"QueryFailed\",\"message\":\"console log query failed; result withheld\"}}\n"
	if code != 1 || stdout.Len() != 0 || stderr.String() != want {
		t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestWithheldQueryErrorUnwrap(t *testing.T) {
	err := &queryFailedError{withheldMessage: "console log query failed; result withheld", err: errors.New("JMESPath: synthetic-marker")}
	if errors.Unwrap(err) != nil {
		t.Fatal("withheld query error exposed its cause")
	}
	wrapped := fmt.Errorf("command: %w", err)
	if errors.Unwrap(errors.Unwrap(wrapped)) != nil {
		t.Fatal("wrapped withheld query error exposed its cause")
	}
}
