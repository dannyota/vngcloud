package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"danny.vn/vngcloud"
)

// HeadObjectInput selects one exact object key.
type HeadObjectInput struct {
	Bucket string
	Key    string
}

// HeadObjectOutput contains filtered metadata observations, not object content.
// ContentLength is -1 when absent. Metadata is always non-nil on success.
// VersionID and ServerSideEncryption do not guard later object operations.
type HeadObjectOutput struct {
	ContentLength        int64
	ETag                 string
	ContentType          string
	LastModified         *time.Time
	Metadata             map[string]string
	VersionID            string
	ServerSideEncryption string
}

// HeadObject inspects an object's metadata without downloading its content.
func (c *ObjectClient) HeadObject(ctx context.Context, in *HeadObjectInput) (*HeadObjectOutput, error) {
	const op = "storage.HeadObject"
	if in == nil {
		return nil, fmt.Errorf("%w: object input is required", vngcloud.ErrInvalidInput)
	}
	u, err := c.objectURL(in.Bucket, in.Key)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, u.String(), nil)
	if err != nil {
		return nil, objectAPIError(op, 0, "RequestFailed", err)
	}
	sum := sha256.Sum256(nil)
	c.signObjectRequest(req, hex.EncodeToString(sum[:]), c.now())
	resp, err := c.httpClient.Do(req)
	// HEAD never reads or drains a response, including an error response.
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		return nil, objectAPIError(op, 0, "RequestFailed", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, objectAPIError(op, resp.StatusCode, "S3Error", ctx.Err())
	}
	if ctx.Err() != nil {
		return nil, objectAPIError(op, resp.StatusCode, "RequestFailed", ctx.Err())
	}
	length, valid := c.headObjectLength(resp.Header)
	if !valid {
		return nil, objectAPIError(op, resp.StatusCode, "InvalidResponse", ctx.Err())
	}
	out := &HeadObjectOutput{
		ContentLength:        length,
		ETag:                 c.objectScalarHeader(resp.Header, "ETag", 1024),
		ContentType:          c.objectScalarHeader(resp.Header, "Content-Type", 256),
		LastModified:         c.objectTime(c.objectScalarHeader(resp.Header, "Last-Modified", 128), 128, http.TimeFormat),
		Metadata:             c.objectUserMetadata(resp.Header),
		VersionID:            c.objectScalarHeader(resp.Header, "x-amz-version-id", 1024),
		ServerSideEncryption: c.objectScalarHeader(resp.Header, "x-amz-server-side-encryption", 128),
	}
	if ctx.Err() != nil {
		return nil, objectAPIError(op, resp.StatusCode, "RequestFailed", ctx.Err())
	}
	return out, nil
}

func (c *ObjectClient) headObjectLength(h http.Header) (int64, bool) {
	value, seen := "", false
	for name, values := range h {
		if !strings.EqualFold(name, "Content-Length") {
			continue
		}
		if seen || len(values) != 1 {
			return 0, false
		}
		seen, value = true, values[0]
	}
	if !seen {
		return -1, true
	}
	if value == "" || c.filterObjectMetadata(value, len(value)) != value {
		return 0, false
	}
	for i := 0; i < len(value); i++ {
		if value[i] < '0' || value[i] > '9' {
			return 0, false
		}
	}
	length, err := strconv.ParseInt(value, 10, 64)
	return length, err == nil
}
