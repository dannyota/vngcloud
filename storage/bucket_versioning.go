package storage

import (
	"context"
	"encoding/json"
	"net/http"

	"danny.vn/vngcloud/internal/core"
)

type GetBucketVersioningInput struct {
	// Region is the vStorage region name; see ListBucketsInput.
	Region    string
	ProjectID string `vngcloud:"required"`
	Bucket    string `vngcloud:"required"`
}

type GetBucketVersioningOutput struct {
	// Enabled is true while versioning is on.
	Enabled bool
	// Status is the server's state, unchanged: "Off", "Enabled", or
	// "Suspended". An unknown value passes through.
	Status string
}

type versioningData struct {
	Versioning       bool   `json:"versioning"`
	VersioningStatus string `json:"versioningStatus"`
}

// GetBucketVersioning returns a bucket's versioning state. A bucket reads
// Status "Off" only until the first PutBucketVersioning; nothing returns it
// to "Off" afterwards. A missing bucket is ErrNotFound: the server answers
// it with an empty body, and the call then reads the bucket to tell.
func (c *Client) GetBucketVersioning(ctx context.Context, in *GetBucketVersioningInput) (*GetBucketVersioningOutput, error) {
	const op = "storage.GetBucketVersioning"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	id, err := c.checkBucketPaths(ctx, op, in.Region, in.ProjectID, in.Bucket)
	if err != nil {
		return nil, err
	}
	env, err := c.exchangeBucket(ctx, call{
		op:       op,
		method:   http.MethodGet,
		url:      c.route(bucketSettingParts(in.ProjectID, in.Bucket, "versioning"), nil),
		regionID: id,
		ok:       []int{http.StatusOK},
	}, in.Region, in.ProjectID, in.Bucket)
	if err != nil {
		return nil, err
	}
	var data versioningData
	if len(env.Data) == 0 || env.Data[0] != '{' || json.Unmarshal(env.Data, &data) != nil {
		return nil, invalidResponse(op, "the response has no versioning state")
	}
	return &GetBucketVersioningOutput{Enabled: data.Versioning, Status: data.VersioningStatus}, nil
}

type PutBucketVersioningInput struct {
	// Region is the vStorage region name; see ListBucketsInput.
	Region    string
	ProjectID string `vngcloud:"required"`
	Bucket    string `vngcloud:"required"`
	// Enabled is the target state. It is a pointer so that a caller must
	// choose: the server reads a body without it as false, which suspends
	// versioning.
	Enabled *bool `vngcloud:"required"`
}

type PutBucketVersioningOutput struct{}

type putBucketVersioningBody struct {
	Enable bool `json:"enable"`
}

// PutBucketVersioning turns versioning on with Enabled true, or suspends it
// with false. A nil Enabled is ErrInvalidInput and sends nothing.
//
// Suspending keeps the versions already stored, and a bucket never versioned
// also reads "Suspended" afterwards, not "Off". While versioning is on,
// overwrites and deletes keep old versions, which use quota and make
// DeleteBucket refuse the bucket. A put names the target state, so a repeat
// gives the same result and the call keeps the transport's retries. The
// change shows on the next read.
func (c *Client) PutBucketVersioning(ctx context.Context, in *PutBucketVersioningInput) (*PutBucketVersioningOutput, error) {
	const op = "storage.PutBucketVersioning"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	id, err := c.checkBucketPaths(ctx, op, in.Region, in.ProjectID, in.Bucket)
	if err != nil {
		return nil, err
	}
	if _, err := c.exchangeBucket(ctx, call{
		op:       op,
		method:   http.MethodPut,
		url:      c.route(bucketSettingParts(in.ProjectID, in.Bucket, "versioning"), nil),
		regionID: id,
		body:     putBucketVersioningBody{Enable: *in.Enabled},
		ok:       []int{http.StatusOK, http.StatusNoContent},
		write:    true,
	}, in.Region, in.ProjectID, in.Bucket); err != nil {
		return nil, err
	}
	return &PutBucketVersioningOutput{}, nil
}

func bucketSettingParts(project, bucket, setting string) []string {
	return []string{"ceph", "projects", project, "buckets", bucket, setting}
}

// invalidResponse is the error for a 2xx envelope whose data is not the shape
// the call expects. It never quotes the response.
func invalidResponse(op, message string) error {
	return &core.APIError{Operation: op, StatusCode: http.StatusOK, Code: "InvalidResponse", Message: message}
}
