package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptrace"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"danny.vn/vngcloud"
)

// CopyObjectInput selects distinct current names in the client's configured
// region and credential/project scope. Replace must explicitly permit replacing
// any current destination, including one changed by a concurrent writer.
type CopyObjectInput struct {
	SourceBucket string
	SourceKey    string
	Bucket       string
	Key          string
	Replace      bool
}

// CopyObjectOutput contains bounded metadata observations, not a lossless clone
// or integrity attestation. Version fields are diagnostic and never authorize
// source cleanup. Empty fields and nil LastModified mean absent or filtered.
type CopyObjectOutput struct {
	ETag                 string
	LastModified         *time.Time
	VersionID            string
	SourceVersionID      string
	ServerSideEncryption string
}

// CopyObject sends one native server-side replacement copy without retrying or
// deleting the source. Both buckets must belong to the same project and region.
// The service selects the current source; neither name is locked against races.
// Provider metadata defaults apply without overrides or a lossless clone promise.
// Recorded HCM04 behavior refuses encrypted sources with 501 and can leave a copy
// unencrypted despite destination bucket encryption. HAN02 encryption behavior
// is unverified. Inspect the destination before repeating an unconfirmed copy.
func (c *ObjectClient) CopyObject(ctx context.Context, in *CopyObjectInput) (*CopyObjectOutput, error) {
	const op = "storage.CopyObject"
	if in == nil {
		return nil, fmt.Errorf("%w: object input is required", vngcloud.ErrInvalidInput)
	}
	if !in.Replace {
		return nil, fmt.Errorf("%w: native create-only copy is unavailable; Replace must be true", vngcloud.ErrInvalidInput)
	}
	source, err := c.objectURL(in.SourceBucket, in.SourceKey)
	if err != nil {
		return nil, err
	}
	destination, err := c.objectURL(in.Bucket, in.Key)
	if err != nil {
		return nil, err
	}
	if in.SourceBucket == in.Bucket && in.SourceKey == in.Key {
		return nil, fmt.Errorf("%w: source and destination must be distinct", vngcloud.ErrInvalidInput)
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	var connected atomic.Bool
	trace := &httptrace.ClientTrace{GotConn: func(httptrace.GotConnInfo) { connected.Store(true) }}
	req, err := http.NewRequestWithContext(httptrace.WithClientTrace(ctx, trace), http.MethodPut, destination.String(), nil)
	if err != nil {
		return nil, objectAPIError(op, 0, "RequestFailed", err)
	}
	req.Header.Set("X-Amz-Copy-Source", source.EscapedPath())
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
			return nil, unconfirmedObjectCopy(api)
		}
		return nil, api
	}
	var closeOnce sync.Once
	closeBody := func() { closeOnce.Do(func() { _ = resp.Body.Close() }) }
	defer closeBody()
	// Cancellation must unblock decoding even after headers have arrived.
	stop := context.AfterFunc(ctx, closeBody)
	defer stop()
	if resp.StatusCode != http.StatusOK {
		responseErr := c.objectResponseError(op, resp, false)
		if ctx.Err() != nil {
			responseErr = objectAPIError(op, resp.StatusCode, vngcloud.ErrorCode(responseErr), errors.Join(responseErr, ctx.Err()))
		}
		if resp.StatusCode >= 400 && resp.StatusCode < 500 {
			return nil, responseErr
		}
		return nil, unconfirmedObjectCopy(responseErr)
	}
	out, err := c.decodeCopyObjectResult(resp.Body)
	if ctx.Err() != nil {
		return nil, unconfirmedObjectCopy(objectAPIError(op, 200, "RequestFailed", ctx.Err()))
	}
	if err != nil {
		return nil, unconfirmedObjectCopy(err)
	}
	out.VersionID = c.objectScalarHeader(resp.Header, "x-amz-version-id", 1024)
	out.SourceVersionID = c.objectScalarHeader(resp.Header, "x-amz-copy-source-version-id", 1024)
	out.ServerSideEncryption = c.objectScalarHeader(resp.Header, "x-amz-server-side-encryption", 128)
	if ctx.Err() != nil {
		return nil, unconfirmedObjectCopy(objectAPIError(op, 200, "RequestFailed", ctx.Err()))
	}
	return out, nil
}

func (c *ObjectClient) decodeCopyObjectResult(r io.Reader) (*CopyObjectOutput, error) {
	const op = "storage.CopyObject"
	p := &copyXMLParser{decoder: newObjectXMLDecoder(r, 64*1024, "CopyObjectResult", "Error")}
	fields := make(map[string]string)
	root := ""
	for {
		token, err := p.decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			code := "InvalidResponse"
			if errors.Is(err, errObjectXMLRead) {
				code = "RequestFailed"
			}
			return nil, objectAPIError(op, 200, code, err)
		}
		switch token := token.(type) {
		case xml.StartElement:
			if root == "" {
				root, p.namespace = token.Name.Local, token.Name.Space
				continue
			}
			limit := -1
			if root == "CopyObjectResult" {
				switch token.Name.Local {
				case "ETag":
					limit = 1024
				case "LastModified":
					limit = 128
				}
			} else if token.Name.Local == "Code" {
				limit = 64
			}
			if limit < 0 {
				err = p.skip()
			} else if token.Name.Space != p.namespace {
				err = errObjectXMLInvalid
			} else if _, seen := fields[token.Name.Local]; seen {
				err = errObjectXMLInvalid
			} else {
				fields[token.Name.Local], err = p.scalar(limit)
			}
			if err != nil {
				code := "InvalidResponse"
				if errors.Is(err, errObjectXMLRead) {
					code = "RequestFailed"
				}
				return nil, objectAPIError(op, 200, code, err)
			}
		case xml.CharData:
			if !objectXMLWhitespace(token) {
				return nil, objectAPIError(op, 200, "InvalidResponse", nil)
			}
		}
	}
	if root == "Error" {
		code := fields["Code"]
		if !objectKnownS3Code(code) || c.filterObjectMetadata(code, 64) != code {
			code = "S3Error"
		}
		return nil, objectAPIError(op, 200, code, nil)
	}
	if fields["ETag"] == "" {
		return nil, objectAPIError(op, 200, "InvalidResponse", nil)
	}
	return &CopyObjectOutput{ETag: c.filterObjectMetadata(fields["ETag"], 1024), LastModified: c.objectTime(fields["LastModified"], 128, time.RFC3339)}, nil
}

type copyXMLParser struct {
	decoder   *objectXMLDecoder
	namespace string
}

func (p *copyXMLParser) skip() error {
	depth := 1
	for depth > 0 {
		token, err := p.decoder.Token()
		if err != nil {
			return err
		}
		switch token.(type) {
		case xml.StartElement:
			depth++
		case xml.EndElement:
			depth--
		}
	}
	return nil
}

// Retain only the scalar cap and an overflow marker while consuming the whole
// field. Unsafe or oversized ETag display values cannot revoke valid shape.
func (p *copyXMLParser) scalar(limit int) (string, error) {
	var text strings.Builder
	for {
		token, err := p.decoder.Token()
		if err != nil {
			return "", err
		}
		switch token := token.(type) {
		case xml.StartElement:
			return "", errObjectXMLInvalid
		case xml.EndElement:
			return text.String(), nil
		case xml.CharData:
			remaining := limit + 1 - text.Len()
			if remaining > 0 {
				text.Write(token[:min(len(token), remaining)])
			}
		}
	}
}
