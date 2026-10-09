package compute

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/transport"
)

// is4xxAPIError reports whether err is a *core.APIError whose StatusCode is
// 4xx, meaning the server rejected the request outright and never acted on
// it.
func is4xxAPIError(err error) bool {
	var apiErr *core.APIError
	return errors.As(err, &apiErr) && apiErr.StatusCode >= 400 && apiErr.StatusCode < 500
}

// StartServerInput identifies the server to start.
type StartServerInput struct {
	ServerID string `vngcloud:"required"`

	NoWait bool
}

// StartServerOutput is the server after the call, and whether the call
// itself changed anything. Changed is false only when the server was
// already ACTIVE.
type StartServerOutput struct {
	Server  Server
	Changed bool
}

// StartServer drives ServerID to ACTIVE. It reads the server first. Already
// ACTIVE returns at once with Changed false, sending nothing. STOPPED sends
// the start PUT at most once (transport.Request.Once): a resend would act
// on a status read that only grows staler, per ADR 0003. Any other status
// fails closed with ErrUnexpectedStatus, sending nothing, so a start is
// never sent to a server mid-create.
//
// A 4xx response proves the server never acted and is returned as is. Any
// other failure, including a 5xx or a network error after the dial
// succeeded, may have reached the server, so the returned error wraps
// ErrNotSettled instead; the recovery is to run StartServer again, since it
// always reads first.
//
// Without NoWait, StartServer then waits up to 5 minutes, polling GetServer
// every 5 seconds, for Status ACTIVE. ERROR wraps ErrFailed; the bound
// running out, or a read or a sleep failing, wraps ErrNotSettled. NoWait
// returns at once instead, with Output.Server holding the pre-toggle read.
func (c *Client) StartServer(ctx context.Context, in *StartServerInput) (*StartServerOutput, error) {
	const op = "compute.StartServer"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	return c.toggleServerStatus(ctx, op, in.ServerID, in.NoWait, serverStatusActive, serverStatusStopped, "start")
}

// StopServerInput identifies the server to stop.
type StopServerInput struct {
	ServerID string `vngcloud:"required"`

	NoWait bool
}

// StopServerOutput is StartServerOutput's counterpart for StopServer.
type StopServerOutput struct {
	Server  Server
	Changed bool
}

// StopServer drives ServerID to STOPPED. See StartServer; the two share one
// implementation with the roles of the current and target status reversed.
func (c *Client) StopServer(ctx context.Context, in *StopServerInput) (*StopServerOutput, error) {
	const op = "compute.StopServer"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	out, err := c.toggleServerStatus(ctx, op, in.ServerID, in.NoWait, serverStatusStopped, serverStatusActive, "stop")
	if out == nil {
		return nil, err
	}
	return &StopServerOutput{Server: out.Server, Changed: out.Changed}, err
}

// toggleServerStatus is StartServer and StopServer's shared implementation.
// target is the status the call wants (ACTIVE for start, STOPPED for
// stop); from is the only status the toggle is sent from (STOPPED for
// start, ACTIVE for stop); action names the PUT's path segment ("start" or
// "stop").
func (c *Client) toggleServerStatus(ctx context.Context, op, serverID string, noWait bool, target, from, action string) (*StartServerOutput, error) {
	if err := core.CheckPathID(op, "ServerID", serverID); err != nil {
		return nil, err
	}

	current, err := c.GetServer(ctx, &GetServerInput{ServerID: serverID})
	if err != nil {
		return nil, err
	}
	if strings.EqualFold(current.Server.Status, target) {
		return &StartServerOutput{Server: current.Server, Changed: false}, nil
	}
	if !strings.EqualFold(current.Server.Status, from) {
		return nil, fmt.Errorf("%w: %s: server %s is %q", ErrUnexpectedStatus, op, serverID, current.Server.Status)
	}

	projectID, err := c.c.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}
	req := transport.Request{
		Operation: op,
		Method:    http.MethodPut,
		URL:       c.computeURL("v2", []string{projectID, "servers", serverID, action}, nil),
		OK:        []int{202},
		Once:      true,
	}
	if err := c.c.DoJSON(ctx, req, nil); err != nil {
		if is4xxAPIError(err) {
			return nil, err
		}
		return &StartServerOutput{Server: current.Server}, fmt.Errorf("%w: %s: server %s: %w", ErrNotSettled, op, serverID, err)
	}

	if noWait {
		return &StartServerOutput{Server: current.Server, Changed: true}, nil
	}
	settled, waitErr := c.waitServerStatus(ctx, op, serverID, target)
	if settled == nil {
		settled = &current.Server
	}
	return &StartServerOutput{Server: *settled, Changed: true}, waitErr
}

// RebootServerInput identifies the server to reboot.
type RebootServerInput struct {
	ServerID string `vngcloud:"required"`

	NoWait bool
}

type RebootServerOutput struct {
	Server Server
}

// RebootServer reboots ServerID. It reads the server first; a status other
// than ACTIVE fails closed with ErrUnexpectedStatus, sending nothing. When
// ACTIVE, it sends the reboot PUT at most once (transport.Request.Once).
//
// A 4xx response proves the server never acted and is returned as is. Any
// other failure returns an error wrapping ErrNotSettled; run RebootServer
// again to check, since it always reads first.
//
// Without NoWait, RebootServer then waits up to 5 minutes, polling
// GetServer every 5 seconds, for a read showing ACTIVE at least 10 seconds
// after the PUT was sent, since an immediate read can still show the
// pre-reboot ACTIVE state before REBOOTING appears. ERROR wraps ErrFailed;
// the bound running out, or a read or a sleep failing, wraps ErrNotSettled.
// NoWait returns at once instead, with Output.Server holding the
// pre-reboot read.
func (c *Client) RebootServer(ctx context.Context, in *RebootServerInput) (*RebootServerOutput, error) {
	const op = "compute.RebootServer"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "ServerID", in.ServerID); err != nil {
		return nil, err
	}

	current, err := c.GetServer(ctx, &GetServerInput{ServerID: in.ServerID})
	if err != nil {
		return nil, err
	}
	if !strings.EqualFold(current.Server.Status, serverStatusActive) {
		return nil, fmt.Errorf("%w: %s: server %s is %q", ErrUnexpectedStatus, op, in.ServerID, current.Server.Status)
	}

	projectID, err := c.c.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}
	sentAt := c.now()
	req := transport.Request{
		Operation: op,
		Method:    http.MethodPut,
		URL:       c.computeURL("v2", []string{projectID, "servers", in.ServerID, "reboot"}, nil),
		OK:        []int{202},
		Once:      true,
	}
	if err := c.c.DoJSON(ctx, req, nil); err != nil {
		if is4xxAPIError(err) {
			return nil, err
		}
		return &RebootServerOutput{Server: current.Server}, fmt.Errorf("%w: %s: server %s: %w", ErrNotSettled, op, in.ServerID, err)
	}

	if in.NoWait {
		return &RebootServerOutput{Server: current.Server}, nil
	}
	settled, waitErr := c.waitServerRebooted(ctx, op, in.ServerID, sentAt)
	if settled == nil {
		settled = &current.Server
	}
	return &RebootServerOutput{Server: *settled}, waitErr
}

// renameServerBody is RenameServer's request body.
type renameServerBody struct {
	NewName string `json:"newName"`
}

// RenameServerInput renames a server.
type RenameServerInput struct {
	ServerID string `vngcloud:"required"`
	Name     string `vngcloud:"required"`
}

type RenameServerOutput struct {
	Server Server
}

// RenameServer renames a server. Rename is free and keeps the transport's
// normal PUT retries. The response carries the server directly, so there is
// no wait.
func (c *Client) RenameServer(ctx context.Context, in *RenameServerInput) (*RenameServerOutput, error) {
	const op = "compute.RenameServer"
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
	var resp struct {
		Data Server `json:"data"`
	}
	req := transport.Request{
		Operation: op,
		Method:    http.MethodPut,
		URL:       c.computeURL("v2", []string{projectID, "servers", in.ServerID, "rename"}, nil),
		Body:      renameServerBody{NewName: in.Name},
		OK:        []int{200},
	}
	if err := c.c.DoJSON(ctx, req, &resp); err != nil {
		return nil, err
	}
	return &RenameServerOutput{Server: resp.Data}, nil
}
