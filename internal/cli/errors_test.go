package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"danny.vn/vngcloud"
)

func TestExitCode(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{"nil", nil, 0},
		{"context canceled bare", context.Canceled, 1},
		{"context deadline exceeded bare", context.DeadlineExceeded, 1},
		{
			"context canceled wrapped in APIError",
			&vngcloud.APIError{Operation: "compute.ListServers", Err: context.Canceled},
			1,
		},
		{
			"context canceled wrapped in LoginError",
			&vngcloud.LoginError{Reason: "sign-in request failed", Err: context.Canceled},
			1,
		},
		{"no credentials", vngcloud.ErrNoCredentials, 3},
		{"credentials file", vngcloud.ErrCredentialsFile, 3},
		{"login error", &vngcloud.LoginError{Reason: "sign-in failed", Status: 401, Err: vngcloud.ErrAuth}, 3},
		{
			"401 after retry",
			&vngcloud.APIError{Operation: "compute.ListServers", StatusCode: 401, Code: "Unauthorized", Err: vngcloud.ErrAuth},
			3,
		},
		{
			"not found",
			&vngcloud.APIError{Operation: "compute.GetServer", StatusCode: 404, Code: "NotFound", Err: vngcloud.ErrNotFound},
			4,
		},
		{"usage error", usageError{msg: "bad flag"}, 2},
		{"read only error", readOnlyError{source: "--read-only"}, 2},
		{"invalid input", fmt.Errorf("%w: Name is required", vngcloud.ErrInvalidInput), 2},
		{"invalid config", fmt.Errorf("%w: region is required", vngcloud.ErrInvalidConfig), 2},
		{"project ambiguous", fmt.Errorf("%w: hcm-3", vngcloud.ErrProjectAmbiguous), 2},
		{
			"query failed after write succeeded falls through to 1",
			&queryFailedError{err: errors.New("bad projection"), writeSucceeded: true},
			1,
		},
		{
			"server error",
			&vngcloud.APIError{Operation: "billing.ListBudgets", StatusCode: 500, Code: "ServerError"},
			1,
		},
		{"plain error", errors.New("boom"), 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := exitCode(tt.err); got != tt.want {
				t.Fatalf("exitCode(%v) = %d, want %d", tt.err, got, tt.want)
			}
		})
	}
}

func TestClassify(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantCode   string
		wantStatus int
		wantOp     string
	}{
		{"usage error", usageError{msg: "bad flag"}, "InvalidUsage", 0, ""},
		{"read only error", readOnlyError{source: "--read-only"}, "ReadOnly", 0, ""},
		{"no credentials", vngcloud.ErrNoCredentials, "NoCredentials", 0, ""},
		{"credentials file", vngcloud.ErrCredentialsFile, "NoCredentials", 0, ""},
		{"login error", &vngcloud.LoginError{Reason: "sign-in failed", Err: vngcloud.ErrAuth}, "LoginFailed", 0, ""},
		{"invalid input", fmt.Errorf("%w: Name is required", vngcloud.ErrInvalidInput), "InvalidUsage", 0, ""},
		{"invalid config", fmt.Errorf("%w: region is required", vngcloud.ErrInvalidConfig), "InvalidConfig", 0, ""},
		{"plain error falls back", errors.New("boom"), "RequestFailed", 0, ""},
		{
			"query failed",
			&queryFailedError{err: errors.New("bad projection")},
			"QueryFailed", 0, "",
		},
		{
			"api error with resolved code and status",
			&vngcloud.APIError{Operation: "compute.GetServer", StatusCode: 404, Code: "NotFound", Message: "server not found"},
			"NotFound", 404, "compute.GetServer",
		},
		{
			"api error with no code and no status falls back to RequestFailed",
			&vngcloud.APIError{Operation: "compute.GetServer", Err: errors.New("decode failed")},
			"RequestFailed", 0, "compute.GetServer",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := classify(tt.err)
			if env.Code != tt.wantCode {
				t.Errorf("Code = %q, want %q", env.Code, tt.wantCode)
			}
			if env.Status != tt.wantStatus {
				t.Errorf("Status = %d, want %d", env.Status, tt.wantStatus)
			}
			if env.Operation != tt.wantOp {
				t.Errorf("Operation = %q, want %q", env.Operation, tt.wantOp)
			}
			if env.Message == "" {
				t.Errorf("Message is empty")
			}
		})
	}
}

func TestPrintErrorShape(t *testing.T) {
	var buf bytes.Buffer
	printError(&buf, &vngcloud.APIError{Operation: "compute.GetServer", StatusCode: 404, Code: "NotFound", Message: "server not found"})

	out := buf.String()
	if n := bytes.Count(buf.Bytes(), []byte("\n")); n != 1 {
		t.Fatalf("expected exactly one line, got %d newlines in %q", n, out)
	}

	var decoded struct {
		Error errorEnvelope `json:"error"`
	}
	if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
		t.Fatalf("output is not valid JSON: %v (%q)", err, out)
	}
	if decoded.Error.Code != "NotFound" || decoded.Error.Status != 404 || decoded.Error.Operation != "compute.GetServer" {
		t.Fatalf("unexpected envelope: %+v", decoded.Error)
	}
	if decoded.Error.Message != "server not found" {
		t.Fatalf("Message = %q", decoded.Error.Message)
	}
}

func TestPrintErrorOmitsStatusAndOperationForNonAPIError(t *testing.T) {
	var buf bytes.Buffer
	printError(&buf, usageError{msg: "unknown flag --nope"})

	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	var inner map[string]json.RawMessage
	if err := json.Unmarshal(decoded["error"], &inner); err != nil {
		t.Fatalf("error field is not an object: %v", err)
	}
	if _, ok := inner["status"]; ok {
		t.Fatalf("status should be omitted for a non-API error, got %s", decoded["error"])
	}
	if _, ok := inner["operation"]; ok {
		t.Fatalf("operation should be omitted for a non-API error, got %s", decoded["error"])
	}
}
