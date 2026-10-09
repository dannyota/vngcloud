package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"

	"danny.vn/vngcloud/internal/core"
)

// listBucketsLimit is the per-project bucket cap, so one call returns every
// bucket.
const listBucketsLimit = "1000"

// bucketNamePattern is a path-safety check only. It rejects "/", "?", "%",
// ".", and "..". The S3 naming rules stay on the server.
var bucketNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,254}$`)

// Bucket is a vStorage bucket. Dates stay strings, as the API sends them.
type Bucket struct {
	Name            string `json:"name"`
	ObjectCount     int64  `json:"count"`
	SizeBytes       int64  `json:"size"`
	IsPublic        bool   `json:"isPublic"`
	IsVersioned     bool   `json:"isVersioned"`
	CreatedDate     string `json:"createdDate"`
	LastModified    string `json:"lastModified"`
	Type            string `json:"type"`
	VersionLocation string `json:"versionLocation"`
}

type ListBucketsInput struct {
	// Region is the vStorage region name, such as HCM04. Empty maps the
	// config region: hcm-3 to HCM04 and han-1 to HAN02.
	Region    string
	ProjectID string `vngcloud:"required"`
}

type ListBucketsOutput = core.List[Bucket]

// ListBuckets lists every bucket in a project.
func (c *Client) ListBuckets(ctx context.Context, in *ListBucketsInput) (*ListBucketsOutput, error) {
	const op = "storage.ListBuckets"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "ProjectID", in.ProjectID); err != nil {
		return nil, err
	}
	id, err := c.regionID(ctx, op, in.Region)
	if err != nil {
		return nil, err
	}
	q := url.Values{}
	q.Set("limit", listBucketsLimit)
	env, err := c.do(ctx, op, c.route([]string{"ceph", "projects", in.ProjectID}, q), id)
	if err != nil {
		return nil, err
	}
	if env.IsNext {
		return nil, &core.APIError{Operation: op, StatusCode: 200, Message: "response has more buckets than one call returns (isNext)"}
	}
	items, err := decodeList[Bucket](op, 200, env)
	if err != nil {
		return nil, err
	}
	return &ListBucketsOutput{Items: items}, nil
}

type GetBucketInput struct {
	// Region is the vStorage region name; see ListBucketsInput.
	Region    string
	ProjectID string `vngcloud:"required"`
	Bucket    string `vngcloud:"required"`
}

type GetBucketOutput struct {
	Bucket
}

// GetBucket reads one bucket's details.
func (c *Client) GetBucket(ctx context.Context, in *GetBucketInput) (*GetBucketOutput, error) {
	const op = "storage.GetBucket"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "ProjectID", in.ProjectID); err != nil {
		return nil, err
	}
	if !bucketNamePattern.MatchString(in.Bucket) {
		return nil, fmt.Errorf("%w: %s requires Bucket to match %s", core.ErrInvalidInput, op, bucketNamePattern.String())
	}
	id, err := c.regionID(ctx, op, in.Region)
	if err != nil {
		return nil, err
	}
	env, err := c.do(ctx, op, c.route([]string{"ceph", "projects", in.ProjectID, in.Bucket, "details"}, nil), id)
	if err != nil {
		return nil, err
	}
	var out GetBucketOutput
	if len(env.Data) == 0 || string(env.Data) == "null" {
		return nil, &core.APIError{Operation: op, StatusCode: 200, Message: "response had no bucket"}
	}
	if err := json.Unmarshal(env.Data, &out.Bucket); err != nil {
		return nil, &core.APIError{Operation: op, StatusCode: 200, Err: fmt.Errorf("decode bucket: %w", err)}
	}
	return &out, nil
}
