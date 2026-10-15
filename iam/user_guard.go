package iam

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"danny.vn/vngcloud/internal/core"
)

// guardUserWrite checks caller classification, self, then target origin.
// Privilege and attachment decisions remain with the server for delete.
func (c *Client) guardUserWrite(ctx context.Context, op, id string, options ...userReadOptions) error {
	caller, err := c.requireClassifiedCaller(ctx, op, options...)
	if err != nil {
		return fmt.Errorf("%s: read caller identity: %w", op, err)
	}
	if caller.UserType == callerTypeIAMUser {
		if core.CheckPathID(op, "UserID", caller.UserID) != nil {
			return fmt.Errorf("%w: %s: caller identity is unusable", ErrPrivilegedChange, op)
		}
		if strings.EqualFold(caller.UserID, id) {
			return fmt.Errorf("%w: %s", ErrSelfChange, op)
		}
	}
	var credentials []string
	defer func() { clear(credentials) }()
	if len(options) != 0 {
		opts := guardUserReadOptions(options...)
		if opts.SentCredentials == nil {
			opts.SentCredentials = &credentials
		}
		options = []userReadOptions{opts}
	}
	user, err := c.getUser(ctx, op, id, guardUserReadOptions(options...))
	if err != nil {
		var api *core.APIError
		if !errors.As(err, &api) || api.StatusCode == 0 {
			return fmt.Errorf("%s: read target user: %w: %w", op, ErrOriginNotIAM, err)
		}
		return fmt.Errorf("%s: read target user: %w", op, err)
	}
	if err := guardCachedResponse(op, user, options...); err != nil {
		return fmt.Errorf("%s: read target user: %w: %w", op, ErrOriginNotIAM, err)
	}
	if core.CheckPathID(op, "UserID", user.ID) != nil || !strings.EqualFold(user.ID, id) || user.From != "iam" {
		return fmt.Errorf("%w: %s: target identity or origin is unproven", ErrOriginNotIAM, op)
	}
	return nil
}
