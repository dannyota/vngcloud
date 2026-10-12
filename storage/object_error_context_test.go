package storage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"testing/synctest"
	"time"

	"danny.vn/vngcloud"
)

func TestObjectErrorResponseContext(t *testing.T) {
	for _, tc := range []struct {
		status int
		want   error
	}{
		{301, nil}, {400, nil}, {401, vngcloud.ErrAuth},
		{403, vngcloud.ErrPermission}, {404, vngcloud.ErrNotFound},
		{409, nil}, {412, nil}, {429, vngcloud.ErrRateLimited},
		{500, nil}, {503, nil},
	} {
		for _, mode := range []string{"get", "put create", "put replace"} {
			for _, contextErr := range []error{context.Canceled, context.DeadlineExceeded} {
				t.Run(fmt.Sprintf("%d/%s/%s", tc.status, mode, contextErr), func(t *testing.T) {
					synctest.Test(t, func(t *testing.T) {
						ctx, cancel := context.WithCancel(t.Context())
						defer cancel()
						tr := &objectErrorContextTransport{status: tc.status}
						if errors.Is(contextErr, context.Canceled) {
							tr.cancel = cancel
						}
						c := objectTestClient(t)
						c.timeout = time.Second
						c.httpClient.Transport = tr
						operation := "storage.GetObject"
						var err error
						if mode == "get" {
							out, getErr := c.GetObject(ctx, &GetObjectInput{Bucket: "bucket", Key: "key"})
							if out != nil {
								_ = out.Body.Close()
								t.Fatal("failed GET returned output")
							}
							err = getErr
						} else {
							operation = "storage.PutObject"
							out, putErr := c.PutObject(ctx, &PutObjectInput{
								Bucket: "bucket", Key: "key", Body: bytes.NewReader(nil), Replace: mode == "put replace",
							})
							if out != nil {
								t.Fatal("failed PUT returned output")
							}
							err = putErr
						}
						var api *vngcloud.APIError
						if !errors.As(err, &api) || api.Operation != operation || api.StatusCode != tc.status {
							t.Fatalf("wrong API error: %v", err)
						}
						wantStatus := tc.want
						wantCode := "S3Error"
						if tc.status == 412 && mode == "put create" {
							wantStatus, wantCode = ErrObjectExists, "ObjectExists"
						}
						if !errors.Is(err, contextErr) {
							t.Errorf("lost context identity: %v", err)
						}
						for _, sentinel := range []error{vngcloud.ErrAuth, vngcloud.ErrPermission, vngcloud.ErrNotFound, vngcloud.ErrRateLimited, ErrObjectExists} {
							if errors.Is(err, sentinel) != errors.Is(wantStatus, sentinel) {
								t.Errorf("wrong status identity for %v: %v", sentinel, err)
							}
						}
						unconfirmed := mode != "get" && (tc.status < 400 || tc.status >= 500)
						if errors.Is(err, ErrObjectWriteUnconfirmed) != unconfirmed || api.Retryable != (tc.status == 429) || api.Code != wantCode {
							t.Errorf("wrong outcome: error=%v retryable=%t code=%s", err, api.Retryable, api.Code)
						}
						if tr.calls != 1 || tr.body.reads != 1 || tr.body.closes != 1 {
							t.Errorf("calls=%d reads=%d closes=%d", tr.calls, tr.body.reads, tr.body.closes)
						}
						assertObjectSafe(t, err, "synthetic-secret-key", "synthetic-access-id", "synthetic-object-bytes", "https://peer.invalid/private")
						assertObjectSafe(t, api.Err, "synthetic-secret-key", "synthetic-object-bytes", "https://peer.invalid/private")
					})
				})
			}
		}
	}
}

type objectErrorContextTransport struct {
	status int
	cancel context.CancelFunc
	calls  int
	body   *objectErrorContextBody
}

func (tr *objectErrorContextTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	tr.calls++
	tr.body = &objectErrorContextBody{ctx: req.Context(), cancel: tr.cancel}
	return &http.Response{StatusCode: tr.status, Header: make(http.Header), ContentLength: 1, Body: tr.body}, nil
}

type objectErrorContextBody struct {
	ctx    context.Context
	cancel context.CancelFunc
	reads  int
	closes int
}

func (b *objectErrorContextBody) Read(_ []byte) (int, error) {
	b.reads++
	if b.cancel != nil {
		b.cancel()
	}
	<-b.ctx.Done()
	return 0, fmt.Errorf("synthetic-secret-key https://peer.invalid/private synthetic-object-bytes: %w", b.ctx.Err())
}

func (b *objectErrorContextBody) Close() error { b.closes++; return nil }
