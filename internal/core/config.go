package core

import (
	"context"
	"fmt"
)

// Config is a resolved configuration built by NewConfig. Every service client
// built from one Config shares its token and project discovery, so a program
// logs in once; two logins inside one TOTP window would fail.
type Config struct {
	client *Client
}

// NewConfig builds a Config from options only.
func NewConfig(opts ...Option) (Config, error) {
	c, err := newClient(opts...)
	if err != nil {
		return Config{}, err
	}
	return Config{client: c}, nil
}

func (c Config) Region() string { return ClientOf(c).Region() }

// Authenticate logs in now, so credential errors surface before the first call.
func (c Config) Authenticate(ctx context.Context) error { return ClientOf(c).Authenticate(ctx) }

// ClientOf returns the shared client. A zero Config yields a client whose
// calls fail with ErrInvalidConfig.
func ClientOf(c Config) *Client {
	if c.client == nil {
		return &Client{err: fmt.Errorf("%w: Config was not built by NewConfig", ErrInvalidConfig)}
	}
	return c.client
}

type EndpointOverrides struct {
	VServer            string
	VLB                string
	VNetwork           string
	GlobalLoadBalancer string
	GLB                string
	DNS                string
	ContainerRegistry  string
	VCR                string
	Portal             string
	Signin             string
	Dashboard          string
	Token              string
	Billing            string
}
