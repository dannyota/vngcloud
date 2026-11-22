package billing

import (
	"context"
	"errors"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/transport"
)

func (c *Client) doBudgetWrite(ctx context.Context, req transport.Request, out any) (int, error) {
	var notSent bool
	req.Once = true
	req.NoRedirect = true
	req.NotSent = &notSent
	req.MaxBody = 8 << 20
	status, err := c.do(ctx, req, out)
	if err == nil || notSent || (status >= 400 && status < 500) {
		return status, err
	}
	safe := core.APIError{Operation: req.Operation, StatusCode: status}
	var api *core.APIError
	if errors.As(err, &api) {
		safe = *api
	}
	if status >= 300 && status < 400 {
		safe.Code = "InvalidResponse"
	}
	safe.Message = "budget write outcome is unconfirmed; read current state before retrying"
	safe.Retryable = false
	safe.Err = historySafeCause(err)
	return status, &safe
}
