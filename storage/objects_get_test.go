package storage

import (
	"bytes"
	"errors"
	"io"
	"math"
	"net/http"
	"strings"
	"testing"

	"danny.vn/vngcloud"
)

func TestGetObjectHeaders(t *testing.T) {
	for _, status := range []int{200, 206, 301, 400, 403, 404, 409, 412, 429, 500} {
		c, _ := objectLocalClient(t, func(w http.ResponseWriter, r *http.Request) {
			if r.Method != "GET" {
				t.Error(r.Method)
			}
			w.Header().Set("Content-Type", "application/octet-stream")
			w.WriteHeader(status)
			_, _ = w.Write([]byte{0, 255, 128})
		})
		out, err := c.GetObject(t.Context(), &GetObjectInput{Bucket: "bucket", Key: "key"})
		if status != 200 {
			var api *vngcloud.APIError
			if out != nil || !errors.As(err, &api) {
				t.Fatalf("status=%d out=%v err=%v", status, out, err)
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(out.Body)
		closeErr := out.Body.Close()
		if err != nil || closeErr != nil || !bytes.Equal(data, []byte{0, 255, 128}) {
			t.Fatalf("data=%x err=%v close=%v", data, err, closeErr)
		}
	}
	c := objectTestClient(t)
	for _, in := range []*GetObjectInput{nil, {Bucket: "bucket", Key: "key", MaxBytes: -1}, {Bucket: "bucket", Key: "key", MaxBytes: math.MaxInt64}} {
		out, err := c.GetObject(t.Context(), in)
		if out != nil || !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Fatalf("out=%v err=%v", out, err)
		}
	}
}

type objectResponseTransport struct {
	response *http.Response
	err      error
	request  *http.Request
}

func (tr *objectResponseTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	tr.request = r
	return tr.response, tr.err
}
func TestGetObjectHeaderFailures(t *testing.T) {
	for _, header := range []string{"-1", "bogus", "3"} {
		raw := &objectFaultBody{readErr: io.EOF}
		c := objectTestClient(t)
		c.httpClient.Transport = &objectResponseTransport{response: &http.Response{StatusCode: 200, Header: http.Header{"Content-Length": {header}}, ContentLength: -1, Body: raw}}
		out, err := c.GetObject(t.Context(), &GetObjectInput{Bucket: "bucket", Key: "key", MaxBytes: 2})
		if out != nil || err == nil || !raw.closed {
			t.Fatalf("header=%q out=%v err=%v closed=%v", header, out, err, raw.closed)
		}
	}
	c, _ := objectLocalClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Set("Content-Disposition", "synthetic-secret-key")
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		_, _ = w.Write([]byte{31, 139, 0, 255})
	})
	out, err := c.GetObject(t.Context(), &GetObjectInput{Bucket: "bucket", Key: "key"})
	if err != nil {
		t.Fatal(err)
	}
	if out.ContentLength != -1 {
		t.Fatalf("length=%d", out.ContentLength)
	}
	data, err := io.ReadAll(out.Body)
	_ = out.Body.Close()
	if err != nil || !bytes.Equal(data, []byte{31, 139, 0, 255}) {
		t.Fatalf("data=%x err=%v", data, err)
	}
}

func TestGetObjectContentTypeBoundary(t *testing.T) {
	for _, tc := range []struct {
		name string
		size int
	}{
		{"256 bytes", 256},
		{"257 bytes", 257},
	} {
		t.Run(tc.name, func(t *testing.T) {
			contentType := "application/" + strings.Repeat("a", tc.size-len("application/"))
			payload := []byte{0, 255, 128}
			c, connections := objectLocalClient(t, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", contentType)
				w.Header().Set("ETag", `"safe"`)
				_, _ = w.Write(payload)
			})
			out, err := c.GetObject(t.Context(), &GetObjectInput{Bucket: "bucket", Key: "key"})
			if err != nil {
				t.Fatal(err)
			}
			data, readErr := io.ReadAll(out.Body)
			closeErr := out.Body.Close()
			if readErr != nil || closeErr != nil || !bytes.Equal(data, payload) {
				t.Fatalf("data=%x read=%v close=%v", data, readErr, closeErr)
			}
			want := ""
			if tc.size == 256 {
				want = contentType
			}
			if out.ContentType != want {
				t.Fatalf("ContentType length=%d, want %d", len(out.ContentType), len(want))
			}
			if out.ETag != `"safe"` || connections.Load() != 1 {
				t.Fatalf("ETag=%q connections=%d", out.ETag, connections.Load())
			}
		})
	}
}
