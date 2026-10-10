// Package backup reads Backup Center backends and policies in hcm-3.
package backup

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/routes"
	"danny.vn/vngcloud/internal/transport"
)

type Client struct{ c *core.Client }

// New shares cfg's credential provider with other service clients.
func New(cfg vngcloud.Config) *Client { return &Client{c: core.ClientOf(cfg)} }

type ListBackendsInput struct{}

type ListBackendsOutput struct {
	Items     []Backend
	Page      *int
	PageSize  *int
	TotalPage int
	TotalItem int
}

type ListPoliciesInput struct {
	Page int
	Size int
}
type ListPoliciesOutput = core.PagedList[Policy]

// ListBackends reads the unpaged backend list. ProjectID does not scope this read.
func (c *Client) ListBackends(ctx context.Context, _ *ListBackendsInput) (*ListBackendsOutput, error) {
	const op = "backup.ListBackends"
	if err := c.checkRegion(op); err != nil {
		return nil, err
	}
	var resp collection[Backend]
	if err := c.get(ctx, op, "backends", nil, &resp); err != nil {
		return nil, err
	}
	if !resp.valid(false) {
		return nil, invalidBody(op)
	}
	return &ListBackendsOutput{Items: resp.Items, Page: resp.Page, PageSize: resp.PageSize, TotalPage: *resp.TotalPages, TotalItem: *resp.TotalItems}, nil
}

// ListPolicies reads one page. Zero selects page 1 and size 200.
// ProjectID does not scope this read.
func (c *Client) ListPolicies(ctx context.Context, in *ListPoliciesInput) (*ListPoliciesOutput, error) {
	const op = "backup.ListPolicies"
	if err := c.checkRegion(op); err != nil {
		return nil, err
	}
	page, size := 0, 0
	if in != nil {
		page, size = in.Page, in.Size
	}
	if page < 0 || size < 0 {
		return nil, fmt.Errorf("%w: %s: Page and Size must not be negative", core.ErrInvalidInput, op)
	}
	if page == 0 {
		page = 1
	}
	if size == 0 {
		size = 200
	}
	q := url.Values{"page": {strconv.Itoa(page)}, "size": {strconv.Itoa(size)}}
	var resp collection[Policy]
	if err := c.get(ctx, op, "backup-policies", q, &resp); err != nil {
		return nil, err
	}
	if !resp.valid(true) {
		return nil, invalidBody(op)
	}
	return core.NewPagedList(resp.Items, *resp.Page, *resp.PageSize, *resp.TotalPages, *resp.TotalItems), nil
}

func (c *Client) checkRegion(op string) error {
	if c.c.Region() != "hcm-3" {
		return fmt.Errorf("%w: %s: Backup Center supports only hcm-3", core.ErrInvalidConfig, op)
	}
	return nil
}

func (c *Client) get(ctx context.Context, op, path string, q url.Values, out any) error {
	status, err := c.c.DoJSONStatus(ctx, transport.Request{Operation: op, Method: "GET", URL: c.c.RouteURL(routes.Route{Product: routes.ProductBackupCenter, Version: "v1", Parts: []string{path}, Query: q}), OK: []int{200}, Sensitive: true, WithholdMessage: "Backup Center response withheld"}, out)
	var api *core.APIError
	if errors.As(err, &api) {
		if api.StatusCode == 0 && status > 0 {
			api.StatusCode = status
		}
		// Server codes can contain credentials too. Preserve only status-derived text.
		return &core.APIError{Operation: api.Operation, StatusCode: api.StatusCode, Code: core.ResolvedCode(api.StatusCode, ""), Message: api.Message, Retryable: api.Retryable, Err: api.Err}
	}
	return err
}

func invalidBody(op string) error {
	return &core.APIError{Operation: op, StatusCode: 200, Message: "unrecognized Backup Center response; body withheld"}
}
