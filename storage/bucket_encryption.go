package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"danny.vn/vngcloud/internal/core"
)

// ErrBucketEncryptionIncomplete means the bucket exists but its requested
// encryption setup did not finish. Read its encryption state before uploading.
var ErrBucketEncryptionIncomplete = errors.New("storage: bucket encryption setup incomplete")

type GetBucketEncryptionInput struct {
	// Region is the vStorage region name; see ListBucketsInput.
	Region     string
	ProjectID  string `vngcloud:"required"`
	BucketName string `vngcloud:"required"`
}

type GetBucketEncryptionOutput struct{ Enabled bool }

// GetBucketEncryption reads the server-managed default encryption state.
// Missing, null, or non-boolean state returns InvalidResponse.
func (c *Client) GetBucketEncryption(ctx context.Context, in *GetBucketEncryptionInput) (*GetBucketEncryptionOutput, error) {
	const op = "storage.GetBucketEncryption"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	id, err := c.checkBucketPaths(ctx, op, in.Region, in.ProjectID, in.BucketName)
	if err != nil {
		return nil, err
	}
	env, err := c.exchangeBucket(ctx, call{
		op: op, method: http.MethodGet,
		url:      c.route(bucketSettingParts(in.ProjectID, in.BucketName, "encryption"), nil),
		regionID: id, ok: []int{http.StatusOK},
	}, in.Region, in.ProjectID, in.BucketName)
	if err != nil {
		return nil, err
	}
	var data map[string]json.RawMessage
	var enabled *bool
	if json.Unmarshal(env.Data, &data) != nil || json.Unmarshal(data["encryption"], &enabled) != nil || enabled == nil {
		return nil, invalidResponse(op, "the response has no boolean encryption state")
	}
	return &GetBucketEncryptionOutput{Enabled: *enabled}, nil
}

type PutBucketEncryptionInput struct {
	// Region is the vStorage region name; see ListBucketsInput.
	Region     string
	ProjectID  string `vngcloud:"required"`
	BucketName string `vngcloud:"required"`
	// Enabled is the complete target state. Its zero value disables encryption.
	Enabled bool
}

type PutBucketEncryptionOutput struct{}

type putBucketEncryptionBody struct {
	Enable bool `json:"enable"`
}

// PutBucketEncryption sets default encryption for future uploads and confirms
// the state with one GetBucketEncryption under ctx. Effects on existing objects
// remain unverified. The PUT is sent once until live checks confirm safe resends.
// An accepted but unconfirmed write wraps ErrNotSettled and any read error.
func (c *Client) PutBucketEncryption(ctx context.Context, in *PutBucketEncryptionInput) (*PutBucketEncryptionOutput, error) {
	const op = "storage.PutBucketEncryption"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	id, err := c.checkBucketPaths(ctx, op, in.Region, in.ProjectID, in.BucketName)
	if err != nil {
		return nil, err
	}
	_, err = c.exchangeBucket(ctx, call{
		op: op, method: http.MethodPut,
		url:      c.route(bucketSettingParts(in.ProjectID, in.BucketName, "encryption"), nil),
		regionID: id, body: putBucketEncryptionBody{Enable: in.Enabled},
		ok: []int{http.StatusOK, http.StatusNoContent}, write: true, once: true,
	}, in.Region, in.ProjectID, in.BucketName)
	if err != nil {
		if encryptionOutcomeUnknown(err) {
			return nil, fmt.Errorf("%s: bucket %q: the change may have happened; check with GetBucketEncryption: %w", op, in.BucketName, err)
		}
		return nil, err
	}
	got, err := c.GetBucketEncryption(ctx, &GetBucketEncryptionInput{Region: in.Region, ProjectID: in.ProjectID, BucketName: in.BucketName})
	if err != nil {
		return nil, fmt.Errorf("%w: %s: bucket %q: check with GetBucketEncryption: %w", ErrNotSettled, op, in.BucketName, err)
	}
	if got.Enabled != in.Enabled {
		return nil, &encryptionMismatch{bucket: in.BucketName}
	}
	return &PutBucketEncryptionOutput{}, nil
}

// encryptionMismatch distinguishes a confirmed false state from a failed read
// when reporting partial creation. Both still match ErrNotSettled.
type encryptionMismatch struct{ bucket string }

func (e *encryptionMismatch) Error() string {
	return fmt.Sprintf("%s: storage.PutBucketEncryption: bucket %q did not match the requested state; check with GetBucketEncryption", ErrNotSettled, e.bucket)
}
func (e *encryptionMismatch) Unwrap() error { return ErrNotSettled }

func encryptionOutcomeUnknown(err error) bool {
	var mismatch *encryptionMismatch
	if errors.As(err, &mismatch) {
		return false
	}
	if errors.Is(err, ErrNotSettled) {
		return true
	}
	var api *core.APIError
	if !errors.As(err, &api) || api.Retryable {
		return false
	}
	return api.StatusCode == 0 || api.StatusCode >= 500 || api.StatusCode/100 == 3 || api.Code == "EmptyResponse" ||
		(api.StatusCode/100 == 2 && api.StatusCode != http.StatusOK && api.StatusCode != http.StatusNoContent)
}

func incompleteBucketEncryption(bucket string, err error) error {
	state := "the bucket exists without encryption enabled by this call"
	if encryptionOutcomeUnknown(err) {
		state = "the bucket exists but encryption is unconfirmed"
	}
	return fmt.Errorf("%w: bucket %q: %s; run get-bucket-encryption, then put-bucket-encryption --enabled=true if needed after fixing the cause; do not upload backups until a read confirms true: %w", ErrBucketEncryptionIncomplete, bucket, state, err)
}
