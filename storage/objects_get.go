package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"

	"danny.vn/vngcloud"
)

// GetObjectInput selects one object and a finite download bound.
// Zero MaxBytes selects DefaultMaxDownloadBytes.
type GetObjectInput struct {
	Bucket   string
	Key      string
	MaxBytes int64
}

// GetObjectOutput exposes filtered headers and an owned download body.
// Read Body to clean EOF and close it. Headers alone do not prove completion.
type GetObjectOutput struct {
	Body          io.ReadCloser `json:"-"`
	ContentLength int64
	ETag          string
	ContentType   string
}

// Format excludes the body and its private request state.
func (out GetObjectOutput) Format(f fmt.State, _ rune) {
	_, _ = fmt.Fprintf(f, "storage.GetObjectOutput{ContentLength:%d ETag:%q ContentType:%q}", out.ContentLength, out.ETag, out.ContentType)
}

// GetObject returns a bounded body whose deadline survives the header response.
func (c *ObjectClient) GetObject(ctx context.Context, in *GetObjectInput) (*GetObjectOutput, error) {
	if in == nil || in.MaxBytes < 0 || in.MaxBytes == math.MaxInt64 {
		return nil, invalidObjectDownload()
	}
	u, err := c.objectURL(in.Bucket, in.Key)
	if err != nil {
		return nil, err
	}
	limit := in.MaxBytes
	if limit == 0 {
		limit = DefaultMaxDownloadBytes
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		cancel()
		return nil, objectAPIError("storage.GetObject", 0, "RequestFailed", err)
	}
	sum := sha256.Sum256(nil)
	c.signObjectRequest(req, hex.EncodeToString(sum[:]), c.now())
	resp, err := c.httpClient.Do(req)
	if err != nil {
		cancel()
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		return nil, objectAPIError("storage.GetObject", 0, "RequestFailed", err)
	}
	fail := func(err error) (*GetObjectOutput, error) { _ = resp.Body.Close(); cancel(); return nil, err }
	if resp.StatusCode != http.StatusOK {
		return fail(c.objectResponseError("storage.GetObject", resp, false))
	}
	length := resp.ContentLength
	if values, exists := resp.Header["Content-Length"]; exists {
		if len(values) != 1 || values[0] == "" {
			return fail(objectAPIError("storage.GetObject", 200, "RequestFailed", nil))
		}
		for _, b := range values[0] {
			if b < '0' || b > '9' {
				return fail(objectAPIError("storage.GetObject", 200, "RequestFailed", nil))
			}
		}
		length, err = strconv.ParseInt(values[0], 10, 64)
		if err != nil {
			return fail(objectAPIError("storage.GetObject", 200, "RequestFailed", nil))
		}
	}
	if length < -1 {
		return fail(objectAPIError("storage.GetObject", 200, "RequestFailed", nil))
	}
	if length > limit {
		return fail(objectAPIError("storage.GetObject", 200, "ObjectTooLarge", nil))
	}
	if ctx.Err() != nil {
		return fail(objectAPIError("storage.GetObject", 200, "RequestFailed", ctx.Err()))
	}
	return &GetObjectOutput{Body: newObjectBody(ctx, resp.Body, cancel, length, limit), ContentLength: length,
		ETag: c.filterObjectMetadata(resp.Header.Get("ETag"), 1024), ContentType: c.filterObjectMetadata(resp.Header.Get("Content-Type"), 256)}, nil
}

func invalidObjectDownload() error {
	return fmt.Errorf("%w: object input and finite download bound are required", vngcloud.ErrInvalidInput)
}
