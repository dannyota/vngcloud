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
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/transport"
)

// ListObjectsInput selects one bounded page of object names in one bucket.
type ListObjectsInput struct {
	Bucket            string
	Prefix            string
	Delimiter         string
	ContinuationToken string
	MaxKeys           int
}

// ObjectSummary describes one listed object without downloading it.
type ObjectSummary struct {
	Key          string
	Size         int64
	ETag         string
	LastModified *time.Time
	StorageClass string
}

// ListObjectsOutput contains one page; continuation requires another call.
type ListObjectsOutput struct {
	Items                 []ObjectSummary
	CommonPrefixes        []string
	MaxKeys               int
	IsTruncated           bool
	NextContinuationToken string
}

// ListObjects returns one complete page without automatic pagination.
func (c *ObjectClient) ListObjects(ctx context.Context, in *ListObjectsInput) (*ListObjectsOutput, error) {
	if in == nil || in.MaxKeys < 0 || in.MaxKeys > 1000 || len(in.Prefix) > 1024 || !utf8.ValidString(in.Prefix) ||
		(in.Delimiter != "" && in.Delimiter != "/") || !listTokenValid(in.ContinuationToken) {
		return nil, fmt.Errorf("%w: valid list input and bounded filters are required", vngcloud.ErrInvalidInput)
	}
	u, err := c.objectBucketURL(in.Bucket)
	if err != nil {
		return nil, err
	}
	maxKeys := in.MaxKeys
	if maxKeys == 0 {
		maxKeys = 1000
	}
	query := url.Values{"list-type": {"2"}, "encoding-type": {"url"}, "max-keys": {strconv.Itoa(maxKeys)}}
	for name, value := range map[string]string{"prefix": in.Prefix, "delimiter": in.Delimiter, "continuation-token": in.ContinuationToken} {
		if value != "" {
			query.Set(name, value)
		}
	}
	u.RawQuery = objectEncodeQuery(query)
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, objectAPIError("storage.ListObjects", 0, "RequestFailed", err)
	}
	sum := sha256.Sum256(nil)
	c.signObjectRequest(req, hex.EncodeToString(sum[:]), c.now())
	resp, err := c.httpClient.Do(req)
	if err != nil {
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		return nil, objectAPIError("storage.ListObjects", 0, "RequestFailed", err)
	}
	var closeOnce sync.Once
	closeBody := func() { closeOnce.Do(func() { _ = resp.Body.Close() }) }
	defer closeBody()
	// Closing on cancellation unblocks body reads after response headers.
	stop := context.AfterFunc(ctx, closeBody)
	defer stop()
	if resp.StatusCode != http.StatusOK {
		err := c.objectResponseError("storage.ListObjects", resp, false)
		if ctx.Err() != nil {
			var api *vngcloud.APIError
			if errors.As(err, &api) {
				err = objectAPIError("storage.ListObjects", resp.StatusCode, api.Code, ctx.Err())
			}
		}
		return nil, err
	}
	out, err := c.decodeListObjects(resp.Body, in, maxKeys)
	if ctx.Err() != nil {
		return nil, objectAPIError("storage.ListObjects", 200, "RequestFailed", ctx.Err())
	}
	return out, err
}

func listTokenValid(value string) bool {
	return len(value) <= 8192 && utf8.ValidString(value) && !objectHasControls(value)
}

// Keys permit controls, but required fields never permit partial redaction.
func (c *ObjectClient) listSafe(value string) bool {
	for _, credential := range []string{c.credentials.AccessKeyID.Reveal(), c.credentials.SecretAccessKey.Reveal()} {
		if transport.RedactErrorText(value, credential, url.PathEscape(credential), url.QueryEscape(credential)) != value {
			return false
		}
	}
	return true
}

type listXMLParser struct {
	decoder   *objectXMLDecoder
	namespace string
}

func (p *listXMLParser) known(name xml.Name) bool { return name.Space == p.namespace }

func (p *listXMLParser) skip() error {
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

// Store at most the field bound plus a marker byte. Oversize optional fields
// are omitted later; the XML wrapper still consumes and bounds the document.
func (p *listXMLParser) scalar(limit int) (string, error) {
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

func listRootLimit(name string) int {
	switch name {
	case "IsTruncated":
		return 5
	case "EncodingType":
		return 3
	case "Name":
		return 255
	case "Prefix":
		return 3072
	case "Delimiter":
		return 3
	case "ContinuationToken", "NextContinuationToken":
		return 8192
	case "MaxKeys", "KeyCount":
		return 20
	default:
		return -1
	}
}

func (p *listXMLParser) entry(prefix bool) (map[string]string, error) {
	fields := make(map[string]string)
	for {
		token, err := p.decoder.Token()
		if err != nil {
			return nil, err
		}
		switch token := token.(type) {
		case xml.EndElement:
			return fields, nil
		case xml.CharData:
			if !objectXMLWhitespace(token) {
				return nil, errObjectXMLInvalid
			}
		case xml.StartElement:
			limit := -1
			if p.known(token.Name) {
				if prefix {
					if token.Name.Local == "Prefix" {
						limit = 3072
					}
				} else {
					switch token.Name.Local {
					case "Key":
						limit = 3072
					case "Size":
						limit = 20
					case "ETag":
						limit = 1024
					case "StorageClass", "LastModified":
						limit = 128
					}
				}
			}
			if limit < 0 {
				if err := p.skip(); err != nil {
					return nil, err
				}
				continue
			}
			name := token.Name.Local
			if _, seen := fields[name]; seen {
				return nil, errObjectXMLInvalid
			}
			value, err := p.scalar(limit)
			if err != nil {
				return nil, err
			}
			fields[name] = value
		}
	}
}

func (c *ObjectClient) listIdentity(raw string) (string, error) {
	value, err := url.PathUnescape(raw)
	if err != nil || value == "" || len(value) > 1024 || !utf8.ValidString(value) || !c.listSafe(value) {
		return "", errObjectXMLInvalid
	}
	return value, nil
}

func (c *ObjectClient) listNumber(raw string, limit int64) (int64, error) {
	if raw == "" || len(raw) > 20 || !c.listSafe(raw) {
		return 0, errObjectXMLInvalid
	}
	for _, b := range raw {
		if b < '0' || b > '9' {
			return 0, errObjectXMLInvalid
		}
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value > limit || !c.listSafe(strconv.FormatInt(value, 10)) {
		return 0, errObjectXMLInvalid
	}
	return value, nil
}

func (c *ObjectClient) listOptional(value string, limit int) string {
	if !c.listSafe(value) {
		return ""
	}
	return c.filterObjectMetadata(value, limit)
}

func (c *ObjectClient) listSummary(fields map[string]string, in *ListObjectsInput) (ObjectSummary, error) {
	key, err := c.listIdentity(fields["Key"])
	if err != nil || !strings.HasPrefix(key, in.Prefix) {
		return ObjectSummary{}, errObjectXMLInvalid
	}
	size, err := c.listNumber(fields["Size"], 1<<63-1)
	if err != nil {
		return ObjectSummary{}, err
	}
	return ObjectSummary{Key: key, Size: size, ETag: c.listOptional(fields["ETag"], 1024),
		StorageClass: c.listOptional(fields["StorageClass"], 128), LastModified: c.objectTime(fields["LastModified"], 128, time.RFC3339Nano)}, nil
}

func (c *ObjectClient) listEchoes(fields map[string]string, in *ListObjectsInput, maxKeys int) error {
	for name, value := range fields {
		switch name {
		case "Name":
			if value != in.Bucket || !c.listSafe(value) {
				return errObjectXMLInvalid
			}
		case "Prefix", "Delimiter":
			decoded, err := url.PathUnescape(value)
			want := in.Prefix
			if name == "Delimiter" {
				want = in.Delimiter
			}
			if err != nil || !utf8.ValidString(decoded) || decoded != want || !c.listSafe(decoded) {
				return errObjectXMLInvalid
			}
		case "ContinuationToken":
			if !listTokenValid(value) || value != in.ContinuationToken || !c.listSafe(value) {
				return errObjectXMLInvalid
			}
		case "MaxKeys", "KeyCount":
			number, err := c.listNumber(value, int64(maxKeys))
			if err != nil || (name == "MaxKeys" && number != int64(maxKeys)) {
				return errObjectXMLInvalid
			}
		}
	}
	return nil
}

func (c *ObjectClient) decodeListObjects(r io.Reader, in *ListObjectsInput, maxKeys int) (*ListObjectsOutput, error) {
	out, err := c.parseListObjects(r, in, maxKeys)
	if err != nil {
		code := "InvalidResponse"
		if errors.Is(err, errObjectXMLRead) {
			code = "RequestFailed"
		}
		return nil, objectAPIError("storage.ListObjects", 200, code, err)
	}
	return out, nil
}

func (c *ObjectClient) parseListObjects(r io.Reader, in *ListObjectsInput, maxKeys int) (*ListObjectsOutput, error) {
	p := &listXMLParser{decoder: newObjectXMLDecoder(r, 8*1024*1024, "ListBucketResult")}
	out := &ListObjectsOutput{Items: []ObjectSummary{}, CommonPrefixes: []string{}, MaxKeys: maxKeys}
	fields := make(map[string]string)
	rootSeen, rootEnded := false, false
	for {
		token, err := p.decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		switch token := token.(type) {
		case xml.StartElement:
			if !rootSeen {
				rootSeen = true
				p.namespace = token.Name.Space
				continue
			}
			if !p.known(token.Name) {
				if err := p.skip(); err != nil {
					return nil, err
				}
				continue
			}
			name := token.Name.Local
			if name == "Contents" || name == "CommonPrefixes" {
				if len(out.Items)+len(out.CommonPrefixes) == maxKeys {
					return nil, errObjectXMLInvalid
				}
				entry, err := p.entry(name == "CommonPrefixes")
				if err != nil {
					return nil, err
				}
				if name == "Contents" {
					item, err := c.listSummary(entry, in)
					if err != nil {
						return nil, err
					}
					out.Items = append(out.Items, item)
				} else {
					prefix, err := c.listIdentity(entry["Prefix"])
					if err != nil || in.Delimiter != "/" || !strings.HasPrefix(prefix, in.Prefix) || !strings.HasSuffix(prefix, "/") {
						return nil, errObjectXMLInvalid
					}
					out.CommonPrefixes = append(out.CommonPrefixes, prefix)
				}
				continue
			}
			limit := listRootLimit(name)
			if limit < 0 {
				if err := p.skip(); err != nil {
					return nil, err
				}
				continue
			}
			if _, seen := fields[name]; seen {
				return nil, errObjectXMLInvalid
			}
			value, err := p.scalar(limit)
			if err != nil {
				return nil, err
			}
			fields[name] = value
		case xml.EndElement:
			rootEnded = true
		case xml.CharData:
			if !objectXMLWhitespace(token) {
				return nil, errObjectXMLInvalid
			}
		}
	}
	if !rootEnded || fields["EncodingType"] != "url" || (fields["IsTruncated"] != "true" && fields["IsTruncated"] != "false") {
		return nil, errObjectXMLInvalid
	}
	if err := c.listEchoes(fields, in, maxKeys); err != nil {
		return nil, err
	}
	out.IsTruncated = fields["IsTruncated"] == "true"
	token := fields["NextContinuationToken"]
	if !listTokenValid(token) || !c.listSafe(token) || (out.IsTruncated && (token == "" || token == in.ContinuationToken)) || (!out.IsTruncated && token != "") {
		return nil, errObjectXMLInvalid
	}
	out.NextContinuationToken = token
	return out, nil
}
