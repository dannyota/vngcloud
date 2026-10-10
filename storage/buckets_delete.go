package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"danny.vn/vngcloud/internal/core"
)

var (
	// ErrBucketNotEmpty means DeleteBucket refused a bucket that holds
	// objects, or whose object count the read did not report. Nothing was
	// deleted. Emptying a bucket is object work for an S3 client.
	ErrBucketNotEmpty = errors.New("vngcloud: bucket is not empty")

	// ErrNotSettled means a storage write has an unconfirmed outcome.
	// Read resource and payment state before attempting another write.
	ErrNotSettled = errors.New("storage: delete accepted but not settled")
)

// deletePollInterval and deletePollBound set the delete wait: a read at
// once, then one every interval until the bound has elapsed.
const (
	deletePollInterval = time.Second
	deletePollBound    = 30 * time.Second
)

type DeleteBucketInput struct {
	// Region is the vStorage region name; see ListBucketsInput.
	Region    string
	ProjectID string `vngcloud:"required"`
	Bucket    string `vngcloud:"required"`

	// NoWait returns as soon as the server accepts the delete, without
	// waiting for the bucket to disappear.
	NoWait bool
}

type DeleteBucketOutput struct{}

// bucketUsage holds the details fields the delete guard reads. Pointers tell
// a null or absent field from zero.
type bucketUsage struct {
	Count        *int64   `json:"count"`
	Size         *float64 `json:"size"`
	UsedCapacity *float64 `json:"usedCapacity"`
}

// DeleteBucket deletes an empty bucket. It reads the bucket first and
// returns ErrBucketNotEmpty, sending no DELETE, when the count is above 0 or
// not reported, or when the size or used capacity is above 0. There is no
// force option.
//
// The server deletes asynchronously. Unless NoWait is set, DeleteBucket then
// reads the bucket every second for up to 30 seconds and returns once it is
// gone. A bucket still readable, envelope code -1, and an empty response
// count as still deleting. Any other read error is returned at once with a
// note that the delete was accepted. If the bucket outlasts the bound, the
// error wraps ErrNotSettled.
func (c *Client) DeleteBucket(ctx context.Context, in *DeleteBucketInput) (*DeleteBucketOutput, error) {
	const op = "storage.DeleteBucket"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	id, err := c.checkBucketPaths(ctx, op, in.Region, in.ProjectID, in.Bucket)
	if err != nil {
		return nil, err
	}
	if err := c.checkBucketEmpty(ctx, op, id, in); err != nil {
		return nil, err
	}
	if _, err := c.exchange(ctx, call{
		op:       op,
		method:   http.MethodDelete,
		url:      c.route([]string{"ceph", "projects", in.ProjectID, "buckets", in.Bucket}, nil),
		regionID: id,
		ok:       []int{http.StatusOK, http.StatusNoContent},
		write:    true,
	}); err != nil {
		return nil, err
	}
	if in.NoWait {
		return &DeleteBucketOutput{}, nil
	}
	if err := c.waitBucketGone(ctx, op, in); err != nil {
		return nil, err
	}
	return &DeleteBucketOutput{}, nil
}

// checkBucketEmpty reads the bucket and refuses anything that is not
// provably empty.
func (c *Client) checkBucketEmpty(ctx context.Context, op, regionID string, in *DeleteBucketInput) error {
	env, err := c.do(ctx, "storage.GetBucket", c.route([]string{"ceph", "projects", in.ProjectID, in.Bucket, "details"}, nil), regionID)
	if err != nil {
		return fmt.Errorf("%s: reading the bucket before the delete: %w", op, err)
	}
	var u bucketUsage
	if len(env.Data) == 0 || string(env.Data) == "null" {
		return &core.APIError{Operation: op, StatusCode: http.StatusOK, Message: "response had no bucket"}
	}
	if err := json.Unmarshal(env.Data, &u); err != nil {
		return &core.APIError{Operation: op, StatusCode: http.StatusOK, Err: fmt.Errorf("decode bucket: %w", err)}
	}
	switch {
	case u.Count == nil:
		return fmt.Errorf("%w: %s: the bucket read did not report an object count; nothing was deleted", ErrBucketNotEmpty, op)
	case *u.Count > 0:
		return fmt.Errorf("%w: %s: bucket holds %d objects; empty it with an S3 client first", ErrBucketNotEmpty, op, *u.Count)
	case u.Size != nil && *u.Size > 0, u.UsedCapacity != nil && *u.UsedCapacity > 0:
		return fmt.Errorf("%w: %s: bucket reports stored data though the object count is 0; empty it with an S3 client first", ErrBucketNotEmpty, op)
	}
	return nil
}

// waitBucketGone polls GetBucket until the bucket answers not found. Code -1
// and an empty response are absorbed here only: they are what a read during
// the delete looks like, and a pre-delete read must still report them.
func (c *Client) waitBucketGone(ctx context.Context, op string, in *DeleteBucketInput) error {
	get := &GetBucketInput{Region: in.Region, ProjectID: in.ProjectID, Bucket: in.Bucket}
	return poll(ctx, c.now, func(ctx context.Context, d time.Duration) error {
		if err := c.sleep(ctx, d); err != nil {
			return fmt.Errorf("%s: the delete was accepted, but the wait ended: %w", op, err)
		}
		return nil
	}, deletePollInterval, deletePollBound,
		func(ctx context.Context) (bool, error) {
			_, err := c.GetBucket(ctx, get)
			switch {
			case err == nil:
				return false, nil
			case errors.Is(err, core.ErrNotFound):
				return true, nil
			case isStillDeleting(err):
				return false, nil
			}
			return true, fmt.Errorf("%s: the delete was accepted, but reading the bucket afterward failed: %w", op, err)
		},
		func() error {
			return fmt.Errorf("%w: %s: the bucket was still readable after %s; the delete was accepted, do not send it again",
				ErrNotSettled, op, deletePollBound)
		})
}

// isStillDeleting reports whether err is what a read answers while the
// server deletes the bucket: envelope code -1 or an empty response.
func isStillDeleting(err error) bool {
	var apiErr *core.APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	return apiErr.Code == "-1" || apiErr.Code == "EmptyResponse"
}
