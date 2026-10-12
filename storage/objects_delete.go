package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptrace"
	"sync/atomic"

	"danny.vn/vngcloud"
)

// DeleteObjectInput selects the current object name without a version or condition.
type DeleteObjectInput struct {
	Bucket string
	Key    string
}

// DeleteObjectOutput contains optional observations from an accepted deletion.
// A nil marker or empty version means absent or filtered. Neither proves prior
// existence, permanent erasure, or protection against concurrent writers.
type DeleteObjectOutput struct {
	DeleteMarker *bool
	VersionID    string
}

// DeleteObject sends one unconditional current-name DELETE without retrying.
// Versioning can retain old versions or add a marker. Repeating deletion can
// affect newly written data or add another marker; inspect an unconfirmed result
// before deciding whether to delete again.
func (c *ObjectClient) DeleteObject(ctx context.Context, in *DeleteObjectInput) (*DeleteObjectOutput, error) {
	const op = "storage.DeleteObject"
	if in == nil {
		return nil, fmt.Errorf("%w: object input is required", vngcloud.ErrInvalidInput)
	}
	u, err := c.objectURL(in.Bucket, in.Key)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	var connected atomic.Bool
	trace := &httptrace.ClientTrace{GotConn: func(httptrace.GotConnInfo) { connected.Store(true) }}
	req, err := http.NewRequestWithContext(httptrace.WithClientTrace(ctx, trace), http.MethodDelete, u.String(), nil)
	if err != nil {
		return nil, objectAPIError(op, 0, "RequestFailed", err)
	}
	sum := sha256.Sum256(nil)
	c.signObjectRequest(req, hex.EncodeToString(sum[:]), c.now())
	if err := ctx.Err(); err != nil {
		return nil, objectAPIError(op, 0, "RequestFailed", err)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		api := objectAPIError(op, 0, "RequestFailed", err)
		if connected.Load() {
			return nil, unconfirmedObjectDelete(api)
		}
		return nil, api
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNoContent {
		responseErr := c.objectResponseError(op, resp, false)
		if ctx.Err() != nil {
			responseErr = objectAPIError(op, resp.StatusCode, vngcloud.ErrorCode(responseErr), errors.Join(responseErr, ctx.Err()))
		}
		if resp.StatusCode >= 400 && resp.StatusCode < 500 {
			return nil, responseErr
		}
		return nil, unconfirmedObjectDelete(responseErr)
	}
	// HTTP 204 is bodyless. Closing without a read also handles injected bodies;
	// optional metadata cannot revoke the server's confirmed acceptance.
	out := &DeleteObjectOutput{VersionID: c.objectScalarHeader(resp.Header, "x-amz-version-id", 1024)}
	switch c.objectScalarHeader(resp.Header, "x-amz-delete-marker", 5) {
	case "true":
		out.DeleteMarker = vngcloud.Ptr(true)
	case "false":
		out.DeleteMarker = vngcloud.Ptr(false)
	}
	return out, nil
}
