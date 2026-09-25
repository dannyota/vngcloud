package sdk

import (
	"context"

	"danny.vn/vngcloud/internal/core"
)

type Client struct {
	*core.Client
}

func NewClient(ctx context.Context, cfg core.Config) (*Client, error) {
	base := core.ClientOf(cfg)
	c := &Client{Client: base}
	if err := c.Authenticate(ctx); err != nil {
		return nil, err
	}
	return c, nil
}
