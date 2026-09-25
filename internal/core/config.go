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

// ProfileSetting returns key's value in the resolved profile's config
// section, or "" when there is none. LoadConfig sets this from the profile
// it resolved; a Config built by NewConfig has no profile and always
// returns "". The SDK reads nothing from this key itself; it exists for a
// caller, such as the CLI, that wants a profile setting LoadConfig does not
// otherwise act on (for example "output" or "read_only").
func (c Config) ProfileSetting(key string) string { return ClientOf(c).ProfileSetting(key) }

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
