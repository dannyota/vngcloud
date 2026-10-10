// Package vks reads Kubernetes cluster inventory and account quota.
package vks

import (
	"context"
	"errors"
	"fmt"
	"net/url"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/routes"
	"danny.vn/vngcloud/internal/transport"
)

// Client shares the Config's credentials and session with other services.
type Client struct{ c *core.Client }

func New(cfg vngcloud.Config) *Client { return &Client{c: core.ClientOf(cfg)} }

func (c *Client) validate() error {
	switch c.c.Region() {
	case "hcm-3", "han-1":
		return nil
	case "":
		return fmt.Errorf("%w: Config was not built by NewConfig", core.ErrInvalidConfig)
	default:
		return fmt.Errorf("%w: VKS requires hcm-3 or han-1", core.ErrInvalidConfig)
	}
}

func malformed(op string) error {
	return &core.APIError{Operation: op, StatusCode: 200, Code: core.ResolvedCode(200, ""), Message: "malformed response; body withheld"}
}

func (c *Client) read(ctx context.Context, op, path string, q url.Values, out any) error {
	if err := c.validate(); err != nil {
		return err
	}
	status, err := c.c.DoJSONStatus(ctx, transport.Request{
		Operation:       op,
		Method:          "GET",
		URL:             c.c.RouteURL(routes.Route{Product: routes.ProductVKS, Version: "v1", Parts: []string{path}, Query: q}),
		OK:              []int{200},
		Sensitive:       true,
		WithholdMessage: "VKS request failed; response withheld",
	}, out)
	if err != nil {
		if status == 200 {
			return malformed(op)
		}
		return safeError(op, err)
	}
	return nil
}

// No arbitrary cause crosses the VKS boundary, because causes can carry URLs
// or response bodies even when the transport withholds its message.
func safeError(op string, err error) error {
	e := &core.APIError{Operation: op, Message: "request failed; response withheld"}
	var ae *core.APIError
	if errors.As(err, &ae) {
		e.StatusCode = ae.StatusCode
		e.Retryable = ae.Retryable
	}
	e.Code = core.ResolvedCode(e.StatusCode, "")
	switch {
	case errors.Is(err, context.Canceled):
		e.Err = context.Canceled
	case errors.Is(err, context.DeadlineExceeded):
		e.Err = context.DeadlineExceeded
	default:
		switch e.StatusCode {
		case 401:
			e.Err = core.ErrAuth
		case 403:
			e.Err = core.ErrPermission
		case 404:
			e.Err = core.ErrNotFound
		case 429:
			e.Err = core.ErrRateLimited
		default:
			for _, sentinel := range []error{core.ErrAuth, core.ErrPermission, core.ErrNotFound, core.ErrRateLimited} {
				if errors.Is(err, sentinel) {
					e.Err = sentinel
					break
				}
			}
		}
	}
	var login *core.LoginError
	if errors.As(err, &login) {
		return &core.LoginError{Status: login.Status, CaptchaSuspected: login.CaptchaSuspected, Reason: op + ": authentication failed; details withheld", Err: e.Err}
	}
	return e
}
