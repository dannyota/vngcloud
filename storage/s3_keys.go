package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/core"
)

// ErrNoSecret is returned by CreateS3Key, with the created key, when the
// create response reports success but carries no secret. The key exists but
// is unusable: delete it with DeleteS3Key.
var ErrNoSecret = errors.New("storage: no secret returned")

// S3Key is a vStorage S3 key without its secret. A key has the rights of the
// IAM user that made it, on every bucket of its project. Fields the API
// leaves null, such as SubUserID, decode as zero.
type S3Key struct {
	UserKeyID   string `json:"userKeyId"`
	AccessKey   string `json:"accessKey"`
	ProjectID   string `json:"projectId"`
	RegionID    string `json:"regionId"`
	UserID      string `json:"userId"`
	SubUserID   string `json:"subUserId"`
	CreatedDate string `json:"createdDate"`
	Status      int    `json:"status"`
}

type ListS3KeysInput struct {
	// Region is the vStorage region name; see ListBucketsInput.
	Region    string
	ProjectID string `vngcloud:"required"`
}

type ListS3KeysOutput = core.List[S3Key]

// ListS3Keys lists the S3 keys of a project. The list carries a secretKey
// field, null on every key seen; S3Key leaves it out and the request is
// sensitive, so the response never reaches a capture hook.
func (c *Client) ListS3Keys(ctx context.Context, in *ListS3KeysInput) (*ListS3KeysOutput, error) {
	const op = "storage.ListS3Keys"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	id, err := c.checkKeyPaths(ctx, op, in.Region, in.ProjectID)
	if err != nil {
		return nil, err
	}
	env, err := c.exchange(ctx, call{
		op:        op,
		method:    http.MethodGet,
		url:       c.route([]string{"users", "s3_keys"}, url.Values{"projectId": {in.ProjectID}}),
		regionID:  id,
		ok:        []int{http.StatusOK},
		sensitive: true,
	})
	if err != nil {
		return nil, err
	}
	items, err := decodeList[S3Key](op, http.StatusOK, env)
	if err != nil {
		return nil, err
	}
	return &ListS3KeysOutput{Items: items}, nil
}

type CreateS3KeyInput struct {
	// Region is the vStorage region name; see ListBucketsInput.
	Region    string
	ProjectID string `vngcloud:"required"`
}

// CreateS3KeyOutput's SecretKey is a vngcloud.Secret: printing, logging, or
// JSON-encoding the Output gives "[redacted]" for it, and Reveal is the only
// way to read the value. It is empty only when CreateS3Key also returns
// ErrNoSecret.
type CreateS3KeyOutput struct {
	S3Key
	SecretKey vngcloud.Secret
}

type s3KeyBody struct {
	ProjectID string `json:"projectId"`
}

type createdS3Key struct {
	S3Key
	SecretKey string `json:"secretKey"`
}

// CreateS3Key creates an S3 key in a project. The server takes no name, and
// the secret is in the create response only.
//
// The Output comes from the create response, with ProjectID from the Input.
// A response without a userKeyId or accessKey is an error that says a key
// may exist. A response without a secret returns the key with an error
// wrapping ErrNoSecret.
//
// The request is sensitive: no capture hook sees the response, and a decode
// failure never quotes it. It is sent once, with no retry, no resend after a
// 401, and no redirect, so a key is never made twice by the SDK. After a
// 5xx, a network error, or a response that fails to decode, a key may exist
// whose secret is lost: list the keys and delete any UserKeyID you do not
// know. The SDK lists nothing itself, since a key another client made at the
// same time would look the same. A 4xx, an envelope refusal, a 429, or a
// failed dial made no key and passes through unchanged.
func (c *Client) CreateS3Key(ctx context.Context, in *CreateS3KeyInput) (*CreateS3KeyOutput, error) {
	const op = "storage.CreateS3Key"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	id, err := c.checkKeyPaths(ctx, op, in.Region, in.ProjectID)
	if err != nil {
		return nil, err
	}
	env, err := c.exchange(ctx, call{
		op:        op,
		method:    http.MethodPost,
		url:       c.route([]string{"users", "s3_keys"}, nil),
		regionID:  id,
		body:      s3KeyBody{ProjectID: in.ProjectID},
		ok:        []int{http.StatusOK, http.StatusCreated},
		write:     true,
		sensitive: true,
		once:      true,
	})
	if err != nil {
		return nil, keyMayExist(err)
	}
	var made createdS3Key
	if err := json.Unmarshal(env.Data, &made); err != nil || made.UserKeyID == "" || made.AccessKey == "" {
		return nil, &core.APIError{
			Operation: op,
			Code:      "InvalidResponse",
			Message:   "the create response had no key; body withheld" + keyMayExistHint,
		}
	}
	made.ProjectID = in.ProjectID
	out := &CreateS3KeyOutput{S3Key: made.S3Key, SecretKey: vngcloud.Secret(made.SecretKey)}
	if made.SecretKey == "" {
		return out, fmt.Errorf("%s: %w: the key exists but is unusable; delete it with DeleteS3Key", op, ErrNoSecret)
	}
	return out, nil
}

type DeleteS3KeyInput struct {
	// Region is the vStorage region name; see ListBucketsInput.
	Region    string
	ProjectID string `vngcloud:"required"`
	UserKeyID string `vngcloud:"required"`
}

type DeleteS3KeyOutput struct{}

// DeleteS3Key deletes an S3 key. It stops working at once and cannot be
// restored. The server answers success for a UserKeyID it does not know,
// and a repeat delete of a deleted key as envelope code 114.
func (c *Client) DeleteS3Key(ctx context.Context, in *DeleteS3KeyInput) (*DeleteS3KeyOutput, error) {
	const op = "storage.DeleteS3Key"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "UserKeyID", in.UserKeyID); err != nil {
		return nil, err
	}
	id, err := c.checkKeyPaths(ctx, op, in.Region, in.ProjectID)
	if err != nil {
		return nil, err
	}
	if _, err := c.exchange(ctx, call{
		op:       op,
		method:   http.MethodDelete,
		url:      c.route([]string{"users", "s3_keys", in.UserKeyID}, nil),
		regionID: id,
		body:     s3KeyBody{ProjectID: in.ProjectID},
		ok:       []int{http.StatusOK, http.StatusNoContent},
		write:    true,
	}); err != nil {
		return nil, err
	}
	return &DeleteS3KeyOutput{}, nil
}

// checkKeyPaths validates the project before any request and resolves the
// region UUID. The region lookup is the only request it can send.
func (c *Client) checkKeyPaths(ctx context.Context, op, region, project string) (string, error) {
	if err := core.CheckPathID(op, "ProjectID", project); err != nil {
		return "", err
	}
	return c.regionID(ctx, op, region)
}

const keyMayExistHint = "; a key may exist: list the keys and delete any UserKeyID you do not know, since its secret is lost"

// keyMayExist adds the list-and-delete advice to an error that leaves the
// create unknown: a 5xx, a network failure, or a response with no usable
// body. A refusal that proves no key was made passes through unchanged: a
// 4xx, an envelope refusal, a 429, or a failed dial.
func keyMayExist(err error) error {
	var apiErr *core.APIError
	if !errors.As(err, &apiErr) || apiErr.Retryable {
		return err
	}
	if apiErr.StatusCode != 0 && apiErr.StatusCode < http.StatusInternalServerError && apiErr.Code != "EmptyResponse" {
		return err
	}
	apiErr.Message = apiErr.Error() + keyMayExistHint
	return err
}
