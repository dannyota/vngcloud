package storage

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"danny.vn/vngcloud"
)

func TestObjectsSecurity(t *testing.T) {
	for _, metadata := range []string{"synthetic-access-id", "synthetic-secret-key", `prefix synthetic-secret-key suffix`, `client_secret=secret`} {
		c, count := objectLocalClient(t, func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Region") != "" || r.Header.Get("Cookie") != "" || r.Header.Get("Idempotency-Key") != "" {
				t.Error("unexpected header")
			}
			w.Header().Set("ETag", metadata)
			w.Header().Set("Content-Type", metadata)
			if r.Method == "GET" {
				_, _ = w.Write([]byte{0, 255})
			}
		})
		put, err := c.PutObject(t.Context(), &PutObjectInput{Bucket: "bucket", Key: "key", Body: bytes.NewReader(nil)})
		if err != nil || put.ETag != "" {
			t.Fatalf("put=%v err=%v", put, err)
		}
		get, err := c.GetObject(t.Context(), &GetObjectInput{Bucket: "bucket", Key: "key"})
		if err != nil || get.ETag != "" || get.ContentType != "" {
			t.Fatalf("get=%v err=%v", get, err)
		}
		encoded, err := json.Marshal(get)
		if err != nil {
			t.Fatal(err)
		}
		for _, value := range []string{fmt.Sprintf("%+v", get), fmt.Sprintf("%#v", get), string(encoded), fmt.Sprintf("%#v", get.Body)} {
			if strings.Contains(value, metadata) || strings.Contains(value, "http.Client") {
				t.Fatal("unsafe output")
			}
		}
		_, err = io.Copy(io.Discard, get.Body)
		if err != nil {
			t.Fatal(err)
		}
		_ = get.Body.Close()
		if count.Load() != 2 {
			t.Fatalf("connections=%d", count.Load())
		}
	}
}
func TestObjectsSyntheticFixture(t *testing.T) {
	fixture, err := os.ReadFile("../testdata/storage/objects/error.xml")
	if err != nil {
		t.Fatal(err)
	}
	c, _ := objectLocalClient(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(403); _, _ = w.Write(fixture) })
	_, err = c.GetObject(t.Context(), &GetObjectInput{Bucket: "bucket", Key: "key"})
	var api *vngcloud.APIError
	if !errors.As(err, &api) || api.Code != "AccessDenied" || !errors.Is(err, vngcloud.ErrPermission) {
		t.Fatal(err)
	}
	assertObjectSafe(t, err, "synthetic-secret-key", "synthetic-object-bytes", "synthetic-host")
	_, err = c.PutObject(t.Context(), &PutObjectInput{Bucket: "bucket", Key: "key", Body: bytes.NewReader(nil)})
	if !errors.As(err, &api) || api.Code != "AccessDenied" || errors.Is(err, ErrObjectWriteUnconfirmed) {
		t.Fatal(err)
	}
}
func TestObjectsConcurrent(t *testing.T) {
	c, _ := objectLocalClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			_, _ = w.Write([]byte("bytes"))
		} else {
			_, _ = io.Copy(io.Discard, r.Body)
		}
	})
	var workers sync.WaitGroup
	for range 8 {
		workers.Go(func() {
			_, err := c.PutObject(t.Context(), &PutObjectInput{Bucket: "bucket", Key: "key", Body: bytes.NewReader([]byte("bytes")), ContentLength: 5})
			if err != nil {
				t.Error(err)
			}
			out, err := c.GetObject(t.Context(), &GetObjectInput{Bucket: "bucket", Key: "key"})
			if err != nil {
				t.Error(err)
				return
			}
			_, err = io.Copy(io.Discard, out.Body)
			if err != nil {
				t.Error(err)
			}
			_ = out.Body.Close()
		})
	}
	workers.Wait()
}

func TestObjectsEscapedMetadata(t *testing.T) {
	for _, credential := range []string{"short", "a\"b\\c"} {
		cfg := objectTestConfig()
		cfg.Credentials.AccessKeyID = vngcloud.Secret(credential)
		cfg.Credentials.SecretAccessKey = vngcloud.Secret(credential)
		c, err := NewObjectClient(cfg)
		if err != nil {
			t.Fatal(err)
		}
		encoded, _ := json.Marshal(credential)
		raw := &objectResponseTransport{response: &http.Response{StatusCode: 200, Header: http.Header{"Etag": {string(encoded)}, "Content-Type": {string(encoded)}}, Body: io.NopCloser(strings.NewReader("")), ContentLength: 0}}
		c.httpClient.Transport = raw
		put, err := c.PutObject(t.Context(), &PutObjectInput{Bucket: "bucket", Key: "key", Body: bytes.NewReader(nil)})
		if err != nil || put.ETag != "" {
			t.Fatalf("put=%v err=%v", put, err)
		}
		raw.response.Body = io.NopCloser(strings.NewReader(""))
		get, err := c.GetObject(t.Context(), &GetObjectInput{Bucket: "bucket", Key: "key"})
		if err != nil || get.ETag != "" || get.ContentType != "" {
			t.Fatalf("get=%v err=%v", get, err)
		}
		_ = get.Body.Close()
	}
}
func TestObjectsRedirect(t *testing.T) {
	var forwarded bool
	target := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) { forwarded = true }))
	defer target.Close()
	for _, status := range []int{301, 302, 303, 307, 308} {
		c, count := objectLocalClient(t, func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Location", target.URL)
			w.WriteHeader(status)
		})
		_, err := c.PutObject(t.Context(), &PutObjectInput{Bucket: "bucket", Key: "key", Body: bytes.NewReader(nil)})
		if !errors.Is(err, ErrObjectWriteUnconfirmed) {
			t.Fatal(err)
		}
		out, err := c.GetObject(t.Context(), &GetObjectInput{Bucket: "bucket", Key: "key"})
		if out != nil || err == nil || forwarded || count.Load() != 2 {
			t.Fatalf("out=%v err=%v", out, err)
		}
	}
}
