package storage

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"danny.vn/vngcloud"
)

func TestObjectBodyRead(t *testing.T) {
	for _, tc := range []struct {
		data          string
		length, limit int64
		code          string
	}{{"", 0, 2, ""}, {"ab", 2, 2, ""}, {"ab", -1, 2, ""}, {"abc", -1, 2, "ObjectTooLarge"}, {"a", 2, 2, "IncompleteObject"}} {
		ctx, cancel := context.WithCancel(t.Context())
		b := newObjectBody(ctx, io.NopCloser(strings.NewReader(tc.data)), cancel, tc.length, tc.limit)
		got, err := io.ReadAll(b)
		if tc.code == "" {
			if err != nil || string(got) != tc.data {
				t.Fatalf("data=%q err=%v", got, err)
			}
		} else {
			var api *vngcloud.APIError
			if !errors.As(err, &api) || api.Code != tc.code || api.Operation != "storage.GetObject" || api.StatusCode != 200 || api.Retryable {
				t.Fatalf("err=%v", err)
			}
			if tc.code == "IncompleteObject" && !errors.Is(err, io.ErrUnexpectedEOF) {
				t.Fatal(err)
			}
		}
		if err := b.Close(); err != nil {
			t.Fatal(err)
		}
		_, err = b.Read(make([]byte, 1))
		var api *vngcloud.APIError
		if !errors.As(err, &api) || api.Code != "ObjectBodyClosed" {
			t.Fatal(err)
		}
	}
}

type objectFaultBody struct {
	readErr, closeErr error
	closed            bool
}

func (b *objectFaultBody) Read([]byte) (int, error) { return 0, b.readErr }
func (b *objectFaultBody) Close() error             { b.closed = true; return b.closeErr }
func TestObjectBodySafeCauses(t *testing.T) {
	for _, cause := range []error{errors.New("synthetic-secret-key"), io.ErrUnexpectedEOF} {
		ctx, cancel := context.WithCancel(t.Context())
		raw := &objectFaultBody{readErr: cause}
		b := newObjectBody(ctx, raw, cancel, -1, 2)
		_, err := b.Read(make([]byte, 2))
		var api *vngcloud.APIError
		if !errors.As(err, &api) || api.Code != "RequestFailed" || api.Err != nil || !raw.closed {
			t.Fatalf("error=%v cause=%v closed=%v", err, api.Err, raw.closed)
		}
		assertObjectSafe(t, err, "synthetic-secret-key")
	}
}

func TestObjectBodyLifetime(t *testing.T) {
	headers := make(chan struct{})
	c, _ := objectLocalClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "2")
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		close(headers)
		<-r.Context().Done()
	})
	ctx, cancel := context.WithCancel(t.Context())
	out, err := c.GetObject(ctx, &GetObjectInput{Bucket: "bucket", Key: "key"})
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	<-headers
	cancel()
	_, err = io.ReadAll(out.Body)
	var api *vngcloud.APIError
	if !errors.As(err, &api) || !errors.Is(err, context.Canceled) || api.Retryable {
		t.Fatal(err)
	}
	_ = out.Body.Close()
	raw := &objectFaultBody{readErr: io.EOF, closeErr: errors.New("synthetic-secret-key")}
	ctx, cancel = context.WithCancel(t.Context())
	b := newObjectBody(ctx, raw, cancel, 0, 1)
	err = b.Close()
	if !errors.As(err, &api) || api.Code != "RequestFailed" || !raw.closed || ctx.Err() != context.Canceled {
		t.Fatal(err)
	}
	assertObjectSafe(t, err, "synthetic-secret-key")
}

func TestObjectBodyProbeBound(t *testing.T) {
	raw := &objectCountingBody{Reader: strings.NewReader("123456789")}
	ctx, cancel := context.WithCancel(t.Context())
	b := newObjectBody(ctx, raw, cancel, -1, 3)
	got, err := io.ReadAll(b)
	var api *vngcloud.APIError
	if string(got) != "123" || raw.count != 4 || !errors.As(err, &api) || api.Code != "ObjectTooLarge" || !raw.closed {
		t.Fatalf("got=%q count=%d err=%v", got, raw.count, err)
	}
}
func TestObjectBodyDeadline(t *testing.T) {
	c, _ := objectLocalClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1")
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	})
	c.timeout = time.Second
	out, err := c.GetObject(t.Context(), &GetObjectInput{Bucket: "bucket", Key: "key"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = io.ReadAll(out.Body)
	var api *vngcloud.APIError
	if !errors.As(err, &api) || !errors.Is(err, context.DeadlineExceeded) || api.StatusCode != 200 || api.Retryable {
		t.Fatal(err)
	}
	_ = out.Body.Close()
	c = objectTestClient(t)
	tr := &objectResponseTransport{response: &http.Response{StatusCode: 200, Header: make(http.Header), ContentLength: 0, Body: io.NopCloser(strings.NewReader(""))}}
	c.httpClient.Transport = tr
	deadline := time.Now().Add(time.Minute)
	ctx, cancel := context.WithDeadline(t.Context(), deadline)
	defer cancel()
	out, err = c.GetObject(ctx, &GetObjectInput{Bucket: "bucket", Key: "key"})
	if err != nil {
		t.Fatal(err)
	}
	got, ok := tr.request.Context().Deadline()
	if !ok || !got.Equal(deadline) {
		t.Fatalf("deadline=%v", got)
	}
	_ = out.Body.Close()
	if tr.request.Context().Err() != context.Canceled {
		t.Fatal("close did not release deadline")
	}
}
