package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptrace"
	"sync/atomic"

	"danny.vn/vngcloud"
)

// PutObjectInput describes one immutable, seekable object starting at offset zero.
// The caller owns Body. Replace permits unconditional whole-object replacement.
type PutObjectInput struct {
	Bucket        string
	Key           string
	Body          io.ReadSeeker `json:"-"`
	ContentLength int64
	Replace       bool
}

// PutObjectOutput describes accepted bytes. ETag is optional and opaque.
type PutObjectOutput struct {
	ContentLength int64
	SHA256        string
	ETag          string
}

// PutObject hashes and sends one bounded object without retrying or closing Body.
func (c *ObjectClient) PutObject(ctx context.Context, in *PutObjectInput) (*PutObjectOutput, error) {
	if in == nil || in.Body == nil || in.ContentLength < 0 || in.ContentLength > MaxPutObjectSize {
		return nil, fmt.Errorf("%w: object body and bounded length are required", vngcloud.ErrInvalidInput)
	}
	u, err := c.objectURL(in.Bucket, in.Key)
	if err != nil {
		return nil, err
	}
	if c.region == "HAN02" && !in.Replace {
		return nil, fmt.Errorf("%w: HAN02 create-only object writes are unavailable", vngcloud.ErrInvalidInput)
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	hash, err := hashObjectBody(ctx, in.Body, in.ContentLength, MaxPutObjectSize)
	if err != nil {
		return nil, err
	}
	var connected atomic.Bool
	trace := &httptrace.ClientTrace{GotConn: func(httptrace.GotConnInfo) { connected.Store(true) }}
	requestCtx := httptrace.WithClientTrace(ctx, trace)
	// A limited reader has no GetBody and does not transfer caller ownership.
	req, err := http.NewRequestWithContext(requestCtx, http.MethodPut, u.String(), io.LimitReader(in.Body, in.ContentLength))
	if err != nil {
		return nil, objectAPIError("storage.PutObject", 0, "RequestFailed", err)
	}
	req.ContentLength = in.ContentLength
	if in.ContentLength == 0 {
		req.Body = http.NoBody
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	if !in.Replace {
		req.Header.Set("If-None-Match", "*")
	}
	c.signObjectRequest(req, hash, c.now())
	if err := ctx.Err(); err != nil {
		return nil, objectAPIError("storage.PutObject", 0, "RequestFailed", err)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		api := objectAPIError("storage.PutObject", 0, "RequestFailed", err)
		if connected.Load() {
			return nil, unconfirmedObjectWrite(api)
		}
		return nil, api
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		responseErr := c.objectResponseError("storage.PutObject", resp, !in.Replace)
		if resp.StatusCode >= 400 && resp.StatusCode < 500 {
			return nil, responseErr
		}
		return nil, unconfirmedObjectWrite(responseErr)
	}
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, 1025))
	if readErr != nil || len(body) != 0 || ctx.Err() != nil {
		if ctx.Err() != nil {
			readErr = ctx.Err()
		}
		return nil, unconfirmedObjectWrite(objectAPIError("storage.PutObject", resp.StatusCode, "RequestFailed", readErr))
	}
	return &PutObjectOutput{ContentLength: in.ContentLength, SHA256: hash, ETag: c.filterObjectMetadata(resp.Header.Get("ETag"), 1024)}, nil
}

func unconfirmedObjectWrite(err error) error {
	var api *vngcloud.APIError
	if errors.As(err, &api) {
		api.Retryable = false
	}
	return fmt.Errorf("%w: %w", ErrObjectWriteUnconfirmed, err)
}

func hashObjectBody(ctx context.Context, body io.ReadSeeker, length, limit int64) (string, error) {
	invalid := func() error { return fmt.Errorf("%w: object body length or seek failed", vngcloud.ErrInvalidInput) }
	canceled := func() error { return objectAPIError("storage.PutObject", 0, "RequestFailed", ctx.Err()) }
	if length < 0 || length > limit {
		return "", invalid()
	}
	if ctx.Err() != nil {
		return "", canceled()
	}
	offset, err := body.Seek(0, io.SeekStart)
	if ctx.Err() != nil {
		return "", canceled()
	}
	if err != nil || offset != 0 {
		return "", invalid()
	}
	hash := sha256.New()
	var buffer [32 * 1024]byte
	remaining := length
	for remaining > 0 {
		if ctx.Err() != nil {
			return "", canceled()
		}
		size := min(int64(len(buffer)), remaining)
		n, readErr := body.Read(buffer[:size])
		if ctx.Err() != nil {
			return "", canceled()
		}
		if n < 0 || int64(n) > size {
			return "", invalid()
		}
		if n > 0 {
			_, _ = hash.Write(buffer[:n])
			remaining -= int64(n)
		}
		if readErr != nil && (!errors.Is(readErr, io.EOF) || remaining != 0) || n == 0 && readErr == nil {
			return "", invalid()
		}
	}
	if ctx.Err() != nil {
		return "", canceled()
	}
	var probe [1]byte
	n, err := body.Read(probe[:])
	if ctx.Err() != nil {
		return "", canceled()
	}
	if n != 0 || !errors.Is(err, io.EOF) {
		return "", invalid()
	}
	offset, err = body.Seek(0, io.SeekStart)
	if ctx.Err() != nil {
		return "", canceled()
	}
	if err != nil || offset != 0 {
		return "", invalid()
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
