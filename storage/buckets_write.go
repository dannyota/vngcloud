package storage

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"danny.vn/vngcloud/internal/core"
)

// createBucketBody is the console body for a bucket without object lock.
type createBucketBody struct {
	Status string `json:"status"`
}

type CreateBucketInput struct {
	// Region is the vStorage region name; see ListBucketsInput.
	Region    string
	ProjectID string `vngcloud:"required"`
	Bucket    string `vngcloud:"required"`
	// Encryption enables default encryption before returning. False sends no
	// encryption call, including when the bucket already exists.
	Encryption bool
}

type CreateBucketOutput struct {
	Bucket
}

// CreateBucket creates a bucket and returns it as GetBucket reads it. It
// sends the console body for a bucket without object lock. If the read after
// the create fails, the error says the bucket was created.
//
// The create is a POST, so it is retried only after a 429 or a failed dial.
// After a 5xx or a network error the bucket may exist; the error names
// GetBucket as the check.
//
// Encryption true enables encryption after creation and before the final read.
// The sequence is not atomic: delay uploads until successful return. A repeat
// create also enables an existing bucket. Failures never delete the bucket and
// preserve their causes; setup failures wrap ErrBucketEncryptionIncomplete.
// Existing objects are not rewritten by this call.
func (c *Client) CreateBucket(ctx context.Context, in *CreateBucketInput) (*CreateBucketOutput, error) {
	const op = "storage.CreateBucket"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	id, err := c.checkBucketPaths(ctx, op, in.Region, in.ProjectID, in.Bucket)
	if err != nil {
		return nil, err
	}
	_, err = c.exchange(ctx, call{
		op:       op,
		method:   http.MethodPost,
		url:      c.route([]string{"ceph", "projects", in.ProjectID, "buckets", in.Bucket}, nil),
		regionID: id,
		body:     createBucketBody{Status: "Disabled"},
		ok:       []int{http.StatusOK, http.StatusCreated},
		write:    true,
	})
	if err != nil {
		return nil, mayHaveCreated(err)
	}
	if in.Encryption {
		if err := ctx.Err(); err != nil {
			return nil, incompleteBucketEncryption(in.Bucket, err)
		}
		if _, err := c.PutBucketEncryption(ctx, &PutBucketEncryptionInput{Region: in.Region, ProjectID: in.ProjectID, BucketName: in.Bucket, Enabled: true}); err != nil {
			return nil, incompleteBucketEncryption(in.Bucket, err)
		}
	}
	got, err := c.GetBucket(ctx, &GetBucketInput{Region: in.Region, ProjectID: in.ProjectID, Bucket: in.Bucket})
	if err != nil {
		if in.Encryption {
			return nil, fmt.Errorf("%s: bucket %q: encryption was confirmed but reading the bucket failed: %w", op, in.Bucket, err)
		}
		return nil, fmt.Errorf("%s: the bucket was created, but reading it back failed: %w", op, err)
	}
	return &CreateBucketOutput{Bucket: got.Bucket}, nil
}

// mayHaveCreated adds the GetBucket check to an error that leaves the create
// unknown: a 5xx or a network failure that is not safe to retry.
func mayHaveCreated(err error) error {
	var apiErr *core.APIError
	if !errors.As(err, &apiErr) || apiErr.Retryable {
		return err
	}
	if apiErr.StatusCode != 0 && apiErr.StatusCode < http.StatusInternalServerError {
		return err
	}
	if apiErr.Message == "" {
		apiErr.Message = "request failed"
	}
	apiErr.Message += "; the bucket may exist, check with GetBucket"
	return err
}

// checkBucketPaths validates the project and bucket before any request and
// resolves the region UUID. The region lookup is the only request it can
// send.
func (c *Client) checkBucketPaths(ctx context.Context, op, region, project, bucket string) (string, error) {
	if err := core.CheckPathID(op, "ProjectID", project); err != nil {
		return "", err
	}
	if !bucketNamePattern.MatchString(bucket) {
		return "", fmt.Errorf("%w: %s requires Bucket to match %s", core.ErrInvalidInput, op, bucketNamePattern.String())
	}
	return c.regionID(ctx, op, region)
}
