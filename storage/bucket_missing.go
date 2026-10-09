package storage

import (
	"context"
	"errors"
	"net/http"

	"danny.vn/vngcloud/internal/core"
)

// exchangeBucket sends k, a versioning, CORS, or policy call on a bucket. On
// a bucket that is gone the server answers HTTP 200 with an empty body, which
// exchange reports as EmptyResponse, and a bucket whose policy has a
// statement without a principal answers the same while it exists. So that
// error leads to one GetBucket with the same fields: its ErrNotFound is
// returned, and any other result returns the EmptyResponse error, since the
// read's own failure says nothing about the call. The read runs under ctx and
// is not retried by this function.
func (c *Client) exchangeBucket(ctx context.Context, k call, region, project, bucket string) (*envelope, error) {
	env, err := c.exchange(ctx, k)
	if err == nil {
		return env, nil
	}
	var apiErr *core.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != "EmptyResponse" || apiErr.StatusCode/100 != http.StatusOK/100 {
		return nil, err
	}
	if _, gerr := c.GetBucket(ctx, &GetBucketInput{Region: region, ProjectID: project, Bucket: bucket}); errors.Is(gerr, core.ErrNotFound) {
		return nil, gerr
	}
	return nil, err
}
