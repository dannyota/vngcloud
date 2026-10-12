package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"danny.vn/vngcloud"
)

type objectLocalTransport struct {
	base    http.RoundTripper
	address string
}

func (tr objectLocalTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	clone := r.Clone(r.Context())
	u := *r.URL
	clone.URL = &u
	clone.URL.Scheme = "http"
	clone.URL.Host = tr.address
	return tr.base.RoundTrip(clone)
}
func objectLocalClient(t *testing.T, handler http.HandlerFunc) (*ObjectClient, *atomic.Int32) {
	t.Helper()
	count := new(atomic.Int32)
	s := httptest.NewUnstartedServer(handler)
	s.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			count.Add(1)
		}
	}
	s.Start()
	t.Cleanup(s.Close)
	c := objectTestClient(t)
	c.httpClient.Transport = objectLocalTransport{c.httpClient.Transport, strings.TrimPrefix(s.URL, "http://")}
	return c, count
}
func TestPutObjectRequest(t *testing.T) {
	for _, data := range [][]byte{nil, {0, 255, 10, 128}} {
		for _, replace := range []bool{false, true} {
			var calls atomic.Int32
			c, connections := objectLocalClient(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				got, err := io.ReadAll(r.Body)
				if err != nil || r.Method != "PUT" || !bytes.Equal(got, data) || r.ContentLength != int64(len(data)) || len(r.TransferEncoding) != 0 {
					t.Errorf("request method=%s length=%d body=%x err=%v", r.Method, r.ContentLength, got, err)
				}
				if r.Header.Get("Content-Type") != "application/octet-stream" {
					t.Error("content type")
				}
				cond := r.Header.Get("If-None-Match")
				if (!replace && cond != "*") || (replace && cond != "") {
					t.Error("condition")
				}
				if !replace && !strings.Contains(r.Header.Get("Authorization"), "SignedHeaders=content-type;host;if-none-match;x-amz-content-sha256;x-amz-date") {
					t.Error("unsigned condition")
				}
				w.Header().Set("ETag", `"safe"`)
			})
			out, err := c.PutObject(t.Context(), &PutObjectInput{Bucket: "bucket", Key: "a//../%2F", Body: bytes.NewReader(data), ContentLength: int64(len(data)), Replace: replace})
			sum := sha256.Sum256(data)
			if err != nil || out == nil || out.SHA256 != hex.EncodeToString(sum[:]) || out.ContentLength != int64(len(data)) || out.ETag != `"safe"` || calls.Load() != 1 || connections.Load() != 1 {
				t.Fatalf("out=%v err=%v calls=%d connections=%d", out, err, calls.Load(), connections.Load())
			}
		}
	}
}

type objectSeekTrap struct{ touched bool }

func (b *objectSeekTrap) Read([]byte) (int, error) {
	b.touched = true
	return 0, errors.New("synthetic-reader-secret")
}
func (b *objectSeekTrap) Seek(int64, int) (int64, error) {
	b.touched = true
	return 0, errors.New("synthetic-reader-secret")
}
func TestPutObjectValidation(t *testing.T) {
	c, calls := objectLocalClient(t, func(_ http.ResponseWriter, _ *http.Request) { t.Error("unexpected dispatch") })
	for _, in := range []*PutObjectInput{nil, {Bucket: "bucket", Key: "key"}, {Bucket: "bad/bucket", Key: "key", Body: bytes.NewReader(nil)}, {Bucket: "bucket", Key: "", Body: bytes.NewReader(nil)}, {Bucket: "bucket", Key: "key", Body: bytes.NewReader(nil), ContentLength: -1}, {Bucket: "bucket", Key: "key", Body: bytes.NewReader(nil), ContentLength: MaxPutObjectSize + 1}, {Bucket: "bucket", Key: "key", Body: bytes.NewReader([]byte{1}), ContentLength: 2}, {Bucket: "bucket", Key: "key", Body: bytes.NewReader([]byte{1}), ContentLength: 0}, {Bucket: "bucket", Key: "key", Body: new(objectSeekTrap)}} {
		out, err := c.PutObject(t.Context(), in)
		if out != nil || !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Fatalf("out=%v err=%v", out, err)
		}
	}
	trap := new(objectSeekTrap)
	c.region = "HAN02"
	out, err := c.PutObject(t.Context(), &PutObjectInput{Bucket: "bucket", Key: "key", Body: trap})
	if out != nil || !errors.Is(err, vngcloud.ErrInvalidInput) || trap.touched || calls.Load() != 0 {
		t.Fatal("HAN02 accessed body")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	c.region = "HCM04"
	out, err = c.PutObject(ctx, &PutObjectInput{Bucket: "bucket", Key: "key", Body: bytes.NewReader(nil)})
	if out != nil || !errors.Is(err, context.Canceled) || calls.Load() != 0 {
		t.Fatal("canceled request dispatched")
	}
}
func TestPutObjectOutcomes(t *testing.T) {
	for _, status := range []int{200, 206, 301, 400, 401, 403, 404, 409, 412, 429, 500, 503} {
		c, count := objectLocalClient(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(status)
			if status == 200 {
				_, _ = w.Write([]byte("unexpected"))
			}
		})
		out, err := c.PutObject(t.Context(), &PutObjectInput{Bucket: "bucket", Key: "key", Body: bytes.NewReader(nil)})
		var api *vngcloud.APIError
		if out != nil || !errors.As(err, &api) || count.Load() != 1 {
			t.Fatalf("status=%d out=%v err=%v", status, out, err)
		}
		unconfirmed := status < 300 || status >= 500 || status >= 300 && status < 400
		if errors.Is(err, ErrObjectWriteUnconfirmed) != unconfirmed || unconfirmed && api.Retryable {
			t.Fatalf("status=%d err=%v", status, err)
		}
		if status == 412 && !errors.Is(err, ErrObjectExists) {
			t.Fatal(err)
		}
	}
}
func TestPutObjectNoReplay(t *testing.T) {
	for _, data := range [][]byte{nil, []byte("binary")} {
		var requests atomic.Int32
		c, count := objectLocalClient(t, func(w http.ResponseWriter, r *http.Request) {
			requests.Add(1)
			_, _ = io.Copy(io.Discard, r.Body)
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			_ = conn.Close()
		})
		for range 2 {
			out, err := c.PutObject(t.Context(), &PutObjectInput{Bucket: "bucket", Key: "key", Body: bytes.NewReader(data), ContentLength: int64(len(data))})
			if out != nil || !errors.Is(err, ErrObjectWriteUnconfirmed) {
				t.Fatalf("out=%v err=%v", out, err)
			}
		}
		if requests.Load() != 2 || count.Load() != 2 {
			t.Fatalf("requests=%d connections=%d", requests.Load(), count.Load())
		}
	}
}

type objectGeneratedReader struct {
	size, pos, total int64
	maxRead, seeks   int
	closeCalls       int
	cancel           context.CancelFunc
}

func (b *objectGeneratedReader) Read(p []byte) (int, error) {
	b.maxRead = max(b.maxRead, len(p))
	if b.cancel != nil {
		b.cancel()
	}
	if b.pos == b.size {
		return 0, io.EOF
	}
	n := int(min(int64(len(p)), b.size-b.pos))
	clear(p[:n])
	b.pos += int64(n)
	b.total += int64(n)
	return n, nil
}
func (b *objectGeneratedReader) Seek(offset int64, whence int) (int64, error) {
	if whence != io.SeekStart || offset != 0 {
		return 0, errors.New("bad seek")
	}
	b.seeks++
	b.pos = 0
	return 0, nil
}
func (b *objectGeneratedReader) Close() error { b.closeCalls++; return nil }
func TestPutObjectBoundedWork(t *testing.T) {
	for _, size := range []int64{0, 1, 65536, 65537} {
		b := &objectGeneratedReader{size: size}
		_, err := hashObjectBody(t.Context(), b, min(size, int64(65536)), 65536)
		if (size > 65536) != errors.Is(err, vngcloud.ErrInvalidInput) || b.total != size || b.maxRead > 32768 {
			t.Fatalf("size=%d read=%d buffer=%d err=%v", size, b.total, b.maxRead, err)
		}
		if size <= 65536 && b.seeks != 2 {
			t.Fatalf("seeks=%d", b.seeks)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	b := &objectGeneratedReader{size: 65536, cancel: cancel}
	_, err := hashObjectBody(ctx, b, b.size, b.size)
	if !errors.Is(err, context.Canceled) || b.total > 32768 {
		t.Fatal(err)
	}
	c, _ := objectLocalClient(t, func(w http.ResponseWriter, r *http.Request) { _, _ = io.Copy(io.Discard, r.Body) })
	c.region = "HAN02"
	b = &objectGeneratedReader{size: 2}
	out, err := c.PutObject(t.Context(), &PutObjectInput{Bucket: "bucket", Key: "key", Body: b, ContentLength: 2, Replace: true})
	if err != nil || out == nil || b.total != 4 || b.closeCalls != 0 || b.seeks != 2 {
		t.Fatalf("out=%v err=%v reader=%+v", out, err, b)
	}
}

func TestPutObjectDispatchBoundary(t *testing.T) {
	c := objectTestClient(t)
	raw := &objectFaultBody{readErr: io.EOF}
	tr := &objectResponseTransport{response: &http.Response{StatusCode: 200, Header: make(http.Header), Body: raw}}
	c.httpClient.Transport = tr
	out, err := c.PutObject(t.Context(), &PutObjectInput{Bucket: "bucket", Key: "key", Body: bytes.NewReader(nil)})
	if err != nil || out == nil || tr.request.GetBody != nil || tr.request.Header.Get("Idempotency-Key") != "" || tr.request.Header.Get("X-Idempotency-Key") != "" || !raw.closed {
		t.Fatalf("out=%v err=%v", out, err)
	}
	tr.err = &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("synthetic-secret-key")}
	tr.response = nil
	out, err = c.PutObject(t.Context(), &PutObjectInput{Bucket: "bucket", Key: "key", Body: bytes.NewReader(nil)})
	var api *vngcloud.APIError
	if out != nil || !errors.As(err, &api) || errors.Is(err, ErrObjectWriteUnconfirmed) {
		t.Fatalf("out=%v err=%v", out, err)
	}
	assertObjectSafe(t, err, "synthetic-secret-key")
}
func TestPutObjectDisconnectBeforeBody(t *testing.T) {
	for _, size := range []int64{0, 65536} {
		c, count := objectLocalClient(t, func(w http.ResponseWriter, _ *http.Request) {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			_ = conn.Close()
		})
		out, err := c.PutObject(t.Context(), &PutObjectInput{Bucket: "bucket", Key: "key", Body: &objectGeneratedReader{size: size}, ContentLength: size})
		if out != nil || !errors.Is(err, ErrObjectWriteUnconfirmed) || count.Load() != 1 {
			t.Fatalf("out=%v err=%v connections=%d", out, err, count.Load())
		}
	}
}
func TestPutObjectCanceledAfterDispatch(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	c, count := objectLocalClient(t, func(_ http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		cancel()
		<-r.Context().Done()
	})
	out, err := c.PutObject(ctx, &PutObjectInput{Bucket: "bucket", Key: "key", Body: bytes.NewReader(nil)})
	var api *vngcloud.APIError
	if out != nil || !errors.Is(err, ErrObjectWriteUnconfirmed) || !errors.Is(err, context.Canceled) || !errors.As(err, &api) || api.Retryable || count.Load() != 1 {
		t.Fatalf("out=%v err=%v", out, err)
	}
	if !strings.Contains(err.Error(), "download and compare before retrying") {
		t.Fatal(err)
	}
}
func TestPutObjectBoundedSuccessBody(t *testing.T) {
	raw := &objectCountingBody{Reader: strings.NewReader(strings.Repeat("x", 2048))}
	c := objectTestClient(t)
	c.httpClient.Transport = &objectResponseTransport{response: &http.Response{StatusCode: 200, Header: make(http.Header), Body: raw}}
	out, err := c.PutObject(t.Context(), &PutObjectInput{Bucket: "bucket", Key: "key", Body: bytes.NewReader(nil)})
	if out != nil || !errors.Is(err, ErrObjectWriteUnconfirmed) || raw.count != 1025 || !raw.closed {
		t.Fatalf("out=%v err=%v count=%d closed=%v", out, err, raw.count, raw.closed)
	}
}

type objectCountingBody struct {
	io.Reader
	count  int
	closed bool
}

func (b *objectCountingBody) Read(p []byte) (int, error) {
	n, err := b.Reader.Read(p)
	b.count += n
	return n, err
}
func (b *objectCountingBody) Close() error { b.closed = true; return nil }

type objectRewindFailure struct {
	*bytes.Reader
	seeks int
}

func (b *objectRewindFailure) Seek(offset int64, whence int) (int64, error) {
	b.seeks++
	if b.seeks == 2 {
		return 0, errors.New("synthetic-secret-key")
	}
	return b.Reader.Seek(offset, whence)
}
func TestPutObjectRewindFailure(t *testing.T) {
	c, count := objectLocalClient(t, func(_ http.ResponseWriter, _ *http.Request) { t.Error("unexpected request") })
	b := &objectRewindFailure{Reader: bytes.NewReader([]byte("data"))}
	out, err := c.PutObject(t.Context(), &PutObjectInput{Bucket: "bucket", Key: "key", Body: b, ContentLength: 4})
	if out != nil || !errors.Is(err, vngcloud.ErrInvalidInput) || count.Load() != 0 {
		t.Fatalf("out=%v err=%v", out, err)
	}
	assertObjectSafe(t, err, "synthetic-secret-key")
}

type objectCancelReader struct {
	cancel     context.CancelFunc
	duringSeek bool
}

func (b *objectCancelReader) Seek(int64, int) (int64, error) {
	if b.duringSeek {
		b.cancel()
		return 0, errors.New("synthetic-secret-key")
	}
	return 0, nil
}
func (b *objectCancelReader) Read([]byte) (int, error) {
	b.cancel()
	return 0, errors.New("synthetic-secret-key")
}
func TestPutObjectHashCancellation(t *testing.T) {
	for _, duringSeek := range []bool{false, true} {
		ctx, cancel := context.WithCancel(t.Context())
		b := &objectCancelReader{cancel: cancel, duringSeek: duringSeek}
		_, err := hashObjectBody(ctx, b, 1, 2)
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		assertObjectSafe(t, err, "synthetic-secret-key")
	}
}
