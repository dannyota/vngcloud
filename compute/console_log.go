package compute

import (
	"context"
	"encoding/json"
	"errors"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/transport"
)

type GetServerConsoleLogInput struct {
	ServerID string `vngcloud:"required"`
}

// GetServerConsoleLogOutput holds console text that can contain secrets.
// Log.Reveal returns the text; ordinary formatting and encoding redact it.
type GetServerConsoleLogOutput struct {
	Log vngcloud.Secret
}

func (c *Client) GetServerConsoleLog(ctx context.Context, in *GetServerConsoleLogInput) (*GetServerConsoleLogOutput, error) {
	const op = "compute.GetServerConsoleLog"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "ServerID", in.ServerID); err != nil {
		return nil, err
	}
	projectID, err := c.c.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}
	var resp map[string]json.RawMessage
	status, err := c.c.DoJSONStatus(ctx, transport.Request{
		Operation:       op,
		Method:          "GET",
		URL:             c.computeURL("v2", []string{projectID, "servers", in.ServerID, "console-log"}, nil),
		OK:              []int{200},
		Sensitive:       true,
		NoRedirect:      true,
		MaxBody:         8 << 20,
		WithholdMessage: "console log request failed; body withheld",
		ClassifyError: func(status int, _ string) string {
			if code := core.ResolvedCode(status, ""); code != "" {
				return code
			}
			return "RequestFailed"
		},
	}, &resp)
	if errors.Is(err, transport.ErrBodyTooLarge) {
		return nil, &core.APIError{Operation: op, Code: "ResponseTooLarge", Message: "console log response exceeds 8 MiB", Err: transport.ErrBodyTooLarge}
	}
	var log *string
	if err == nil {
		if data, exists := resp["data"]; exists {
			err = json.Unmarshal(data, &log)
		}
	}
	if status == 200 && (err != nil || log == nil) {
		return nil, &core.APIError{Operation: op, Code: "RequestFailed", Message: "console log response invalid; body withheld", StatusCode: 200}
	}
	if err != nil {
		return nil, err
	}
	return &GetServerConsoleLogOutput{Log: vngcloud.Secret(*log)}, nil
}
