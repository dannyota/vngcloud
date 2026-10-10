package cdn

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"danny.vn/vngcloud/internal/core"
)

// ErrPurgeCooldown means the CDN was purged less than 30 seconds ago. The
// caller can try again after the cooldown; PurgePaths does not wait or retry.
var ErrPurgeCooldown = errors.New("cdn: purge cooldown")

// PurgePathsInput identifies a CDN and the cached paths to purge.
type PurgePathsInput struct {
	CDNDomain string   `vngcloud:"required"`
	Paths     []string `vngcloud:"required"`
}

// PurgePathsOutput is empty when the purge is accepted.
type PurgePathsOutput struct{}

type purgePathsBody struct {
	CDNDomain string   `json:"cdnDomain"`
	Type      string   `json:"type"`
	Patterns  []string `json:"patterns"`
}

// PurgePaths removes cached objects by path. It requires at least one
// non-empty path, and no path can contain an asterisk. A purge within 30
// seconds of the previous purge returns ErrPurgeCooldown.
//
// The POST uses the standard non-idempotent retry rule: the transport retries
// a 429 or a failed dial, but not a server error or an ambiguous network
// failure.
func (c *Client) PurgePaths(ctx context.Context, in *PurgePathsInput) (*PurgePathsOutput, error) {
	const op = "cdn.PurgePaths"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if len(in.Paths) == 0 {
		return nil, fmt.Errorf("%w: %s requires Paths", core.ErrInvalidInput, op)
	}
	for _, path := range in.Paths {
		switch {
		case path == "":
			return nil, fmt.Errorf("%w: %s requires every path to be non-empty", core.ErrInvalidInput, op)
		case strings.Contains(path, "*"):
			return nil, fmt.Errorf("%w: %s paths must not contain *", core.ErrInvalidInput, op)
		}
	}
	body := purgePathsBody{CDNDomain: in.CDNDomain, Type: "URI", Patterns: in.Paths}
	if _, err := c.do(ctx, call{op: op, method: http.MethodPost, parts: []string{"cdn", "flush-cache"}, body: body}); err != nil {
		return nil, err
	}
	return &PurgePathsOutput{}, nil
}
