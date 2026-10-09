//go:build livewrite

package vngcloud_test

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// liveObjects makes signed, path-style requests to one region's S3 host. It
// never follows a redirect and refuses a non-HTTPS host.
type liveObjects struct {
	signer s3Signer
	base   *url.URL
	http   *http.Client
}

func newLiveObjects(host, region, accessKey, secretKey string) (*liveObjects, error) {
	base, err := url.Parse(host)
	if err != nil || base.Scheme != "https" || base.Host == "" {
		return nil, errors.New("the S3 host is not an https URL")
	}
	return &liveObjects{
		signer: s3Signer{accessKey: accessKey, secretKey: secretKey, region: region},
		base:   base,
		http: &http.Client{
			Timeout:       30 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}, nil
}

// do sends one signed request and returns the status and body. An empty key
// addresses the bucket itself.
func (o *liveObjects) do(ctx context.Context, method, bucket, key string, query url.Values, body []byte) (int, []byte, error) {
	u := *o.base
	u.Path = "/" + bucket
	if key != "" {
		u.Path += "/" + key
	}
	u.RawPath = s3EncodePath(u.Path)
	u.RawQuery = canonicalQuery(query)
	req, err := http.NewRequestWithContext(ctx, method, u.String(), bytes.NewReader(body))
	if err != nil {
		return 0, nil, errors.New("build the S3 request")
	}
	o.signer.sign(req, body, time.Now())
	resp, err := o.http.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("S3 request failed (%T)", err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return resp.StatusCode, nil, fmt.Errorf("read the S3 response (%T)", err)
	}
	return resp.StatusCode, data, nil
}

func (o *liveObjects) put(ctx context.Context, bucket, key string, body []byte) (int, error) {
	status, _, err := o.do(ctx, http.MethodPut, bucket, key, nil, body)
	return status, err
}

// get returns the status and the object bytes.
func (o *liveObjects) get(ctx context.Context, bucket, key string) (int, []byte, error) {
	return o.do(ctx, http.MethodGet, bucket, key, nil, nil)
}

func (o *liveObjects) remove(ctx context.Context, bucket, key string) (int, error) {
	status, _, err := o.do(ctx, http.MethodDelete, bucket, key, nil, nil)
	return status, err
}

// list returns the status and the object keys of one listing page.
func (o *liveObjects) list(ctx context.Context, bucket string) (int, []string, error) {
	status, body, err := o.do(ctx, http.MethodGet, bucket, "", url.Values{"list-type": {"2"}}, nil)
	if err != nil || status != http.StatusOK {
		return status, nil, err
	}
	var result struct {
		Contents []struct {
			Key string `xml:"Key"`
		} `xml:"Contents"`
	}
	if err := xml.Unmarshal(body, &result); err != nil {
		return status, nil, errors.New("decode the S3 listing")
	}
	keys := make([]string, len(result.Contents))
	for i, c := range result.Contents {
		keys[i] = c.Key
	}
	return status, keys, nil
}
