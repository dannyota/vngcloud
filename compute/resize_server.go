package compute

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/transport"
	"danny.vn/vngcloud/pricing"
)

// ResizeServerInput changes a server's flavor. FlavorID must differ from
// the server's current flavor.
type ResizeServerInput struct {
	ServerID string `vngcloud:"required"`
	FlavorID string `vngcloud:"required"`

	// MaxPrice is VND a month; a paid ResizeServer refuses to send above
	// it. QuoteResizeServer ignores it.
	MaxPrice float64
	// NoWait skips ResizeServer's post-resize wait. QuoteResizeServer
	// ignores it.
	NoWait bool
}

// resizeServerBody is ResizeServer and QuoteResizeServer's shared request
// body: the same two fields, flavorId and serverId, that both the resize
// PUT and its quote's resourceInfo carry (see the design's quote request
// table). The SDK never sends the reference's optional hostGroupId.
type resizeServerBody struct {
	FlavorID string `json:"flavorId"`
	ServerID string `json:"serverId"`
}

// buildResizeServerBody validates in and builds the body ResizeServer and
// QuoteResizeServer both send.
func buildResizeServerBody(op string, in *ResizeServerInput) (resizeServerBody, error) {
	if err := core.CheckRequired(op, in); err != nil {
		return resizeServerBody{}, err
	}
	if err := core.CheckPathID(op, "ServerID", in.ServerID); err != nil {
		return resizeServerBody{}, err
	}
	if err := core.CheckPathID(op, "FlavorID", in.FlavorID); err != nil {
		return resizeServerBody{}, err
	}
	return resizeServerBody{FlavorID: in.FlavorID, ServerID: in.ServerID}, nil
}

// QuoteResizeServer prices the flavor change Input describes, without
// sending it. It builds the same body ResizeServer sends and quotes it
// with ActionResize. It ignores Input.MaxPrice and Input.NoWait, which
// govern only an actual resize.
func (c *Client) QuoteResizeServer(ctx context.Context, in *ResizeServerInput) (*pricing.GetQuoteOutput, error) {
	const op = "compute.QuoteResizeServer"
	body, err := buildResizeServerBody(op, in)
	if err != nil {
		return nil, err
	}
	info, err := core.QuoteResourceInfo(body)
	if err != nil {
		return nil, err
	}
	return c.pricing.GetQuote(ctx, &pricing.GetQuoteInput{
		ResourceType: pricing.ResourceServer,
		Action:       pricing.ActionResize,
		ResourceInfo: info,
	})
}

// ResizeServerOutput is ResizeServer's result. MonthlyPrice is the quote's
// own OptimumPrice, the price actually checked against Input.MaxPrice.
type ResizeServerOutput struct {
	Server       Server
	MonthlyPrice float64
}

// ResizeServer changes a server's flavor. It reads the server first: the
// same flavor as Input.FlavorID is core.ErrInvalidInput, and a status other
// than ACTIVE or STOPPED is ErrUnexpectedStatus; both send nothing.
//
// Before any request, ResizeServer also rejects a NaN, +Inf, -Inf, or
// negative Input.MaxPrice with core.ErrInvalidInput. It then builds one
// request body from Input with buildResizeServerBody (ADR 0002 rule 8),
// the same builder QuoteResizeServer uses, and sends that body to the
// quote endpoint first. It refuses with vngcloud.ErrPriceAboveMax, sending
// nothing, when the quote's OptimumPrice exceeds Input.MaxPrice (default
// 0).
//
// The resize PUT is sent at most once (transport.Request.Once), per ADR
// 0003: a resend would act on the flavor read this call already took,
// which only grows staler. A 4xx response proves the server never acted
// and is returned as is; any other failure returns an error wrapping
// ErrNotSettled, and the recovery is to run ResizeServer again, since it
// always reads first.
//
// Without NoWait, ResizeServer then waits up to 15 minutes, polling
// GetServer every 5 seconds, for a read showing the new flavor and Status
// ACTIVE or STOPPED. ERROR wraps ErrFailed; the bound running out, or a
// read or a sleep failing, wraps ErrNotSettled. NoWait returns at once
// instead, with Output.Server holding the pre-resize read.
func (c *Client) ResizeServer(ctx context.Context, in *ResizeServerInput) (*ResizeServerOutput, error) {
	const op = "compute.ResizeServer"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckMaxPrice(op, in.MaxPrice); err != nil {
		return nil, err
	}
	body, err := buildResizeServerBody(op, in)
	if err != nil {
		return nil, err
	}

	current, err := c.GetServer(ctx, &GetServerInput{ServerID: in.ServerID})
	if err != nil {
		return nil, err
	}
	if current.Server.Flavor.FlavorID == in.FlavorID {
		return nil, fmt.Errorf("%w: %s: server %s already has flavor %s", core.ErrInvalidInput, op, in.ServerID, in.FlavorID)
	}
	if !strings.EqualFold(current.Server.Status, serverStatusActive) && !strings.EqualFold(current.Server.Status, serverStatusStopped) {
		return nil, fmt.Errorf("%w: %s: server %s is %q", ErrUnexpectedStatus, op, in.ServerID, current.Server.Status)
	}

	info, err := core.QuoteResourceInfo(body)
	if err != nil {
		return nil, err
	}
	quote, err := c.pricing.GetQuote(ctx, &pricing.GetQuoteInput{
		ResourceType: pricing.ResourceServer,
		Action:       pricing.ActionResize,
		ResourceInfo: info,
	})
	if err != nil {
		return nil, err
	}
	if err := core.CheckPriceAboveMax(op, quote.OptimumPrice, in.MaxPrice); err != nil {
		return nil, err
	}

	projectID, err := c.c.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}
	req := transport.Request{
		Operation: op,
		Method:    http.MethodPut,
		URL:       c.computeURL("v2", []string{projectID, "servers", in.ServerID, "resize"}, nil),
		Body:      body,
		OK:        []int{202},
		Once:      true,
	}
	if err := c.c.DoJSON(ctx, req, nil); err != nil {
		if is4xxAPIError(err) {
			return nil, err
		}
		return &ResizeServerOutput{Server: current.Server}, fmt.Errorf("%w: %s: server %s: %w", ErrNotSettled, op, in.ServerID, err)
	}

	if in.NoWait {
		return &ResizeServerOutput{Server: current.Server, MonthlyPrice: quote.OptimumPrice}, nil
	}
	settled, waitErr := c.waitServerResized(ctx, op, in.ServerID, in.FlavorID)
	if settled == nil {
		settled = &current.Server
	}
	return &ResizeServerOutput{Server: *settled, MonthlyPrice: quote.OptimumPrice}, waitErr
}

// waitServerResized is ResizeServer's post-resize wait unless NoWait is
// set: it reads serverID with GetServer until its Flavor.FlavorID reaches
// wantFlavorID with Status ACTIVE or STOPPED (settled), or ERROR (failed);
// any other outcome keeps it polling.
func (c *Client) waitServerResized(ctx context.Context, op, serverID, wantFlavorID string) (*Server, error) {
	var server *Server
	err := poll(ctx, c.now, c.sleep, serverResizeBound,
		func(ctx context.Context) (bool, error) {
			out, err := c.GetServer(ctx, &GetServerInput{ServerID: serverID})
			if err != nil {
				return true, err
			}
			server = &out.Server
			switch {
			case isServerError(server.Status):
				return true, fmt.Errorf("%w: %s: server %s is ERROR", ErrFailed, op, serverID)
			case server.Flavor.FlavorID == wantFlavorID &&
				(strings.EqualFold(server.Status, serverStatusActive) || strings.EqualFold(server.Status, serverStatusStopped)):
				return true, nil
			default:
				return false, nil
			}
		},
		func() error {
			return fmt.Errorf("%w: %s: server %s did not confirm resize to flavor %s within %s; run this operation again to check",
				ErrNotSettled, op, serverID, wantFlavorID, serverResizeBound)
		},
	)
	if err != nil && !errors.Is(err, ErrFailed) && !errors.Is(err, ErrNotSettled) {
		err = fmt.Errorf("%w: %s: server %s: %w", ErrNotSettled, op, serverID, err)
	}
	return server, err
}
