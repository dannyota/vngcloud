package main

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/endpoints"
	"danny.vn/vngcloud/internal/transport"
	"danny.vn/vngcloud/storage"
)

type storageExampleHTTP func(*http.Request) (*http.Response, error)

func (f storageExampleHTTP) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestShowStorageCapturesEncryptionForEveryBucket(t *testing.T) {
	raw := newRawCaptureStore()
	reads := []string{}
	tc := transport.New(transport.Config{
		HTTPClient: &http.Client{Transport: storageExampleHTTP(func(r *http.Request) (*http.Response, error) {
			if r.Method != "GET" {
				t.Errorf("unexpected write %s", r.Method)
			}
			body := `{"success":true,"datas":[]}`
			switch r.URL.Path {
			case "/internal/v1/regions":
				body = `{"success":true,"datas":[{"regionId":"region-1","regionName":"HCM04"}]}`
			case "/internal/v1/projects":
				body = `{"success":true,"datas":[{"projectId":"project-1"}]}`
			case "/internal/v1/ceph/projects/project-1":
				body = `{"success":true,"datas":[{"name":"bucket-one"},{"name":"bucket-two"}]}`
			case "/internal/v1/ceph/projects/project-1/bucket-one/details":
				body = `{"success":true,"data":{"name":"bucket-one"}}`
			case "/internal/v1/ceph/projects/project-1/buckets/bucket-one/encryption":
				reads = append(reads, "bucket-one")
				body = `{"success":true,"data":{"encryption":true}}`
			case "/internal/v1/ceph/projects/project-1/buckets/bucket-two/encryption":
				reads = append(reads, "bucket-two")
				body = `{"success":true,"data":{"encryption":false}}`
			}
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
		})},
		Capture: func(c transport.Capture) {
			raw.add("unit", "hcm-3", vngcloud.ResponseCapture{Operation: c.Operation, Method: c.Method, URL: c.URL, StatusCode: c.StatusCode, Body: c.Body})
		},
	})
	cfg := core.NewTestConfig("hcm-3", "project-1", endpoints.Set{Region: "hcm-3", Storage: "https://console.invalid/"}, tc)
	outputs := newSDKOutputStore()
	showStorage(t.Context(), cfg, outputs)
	if strings.Join(reads, ",") != "bucket-one,bucket-two" {
		t.Fatalf("encryption reads=%v", reads)
	}
	captured := raw.resources["storage/bucket_encryption"]
	if captured == nil || len(captured.Regions) != 2 {
		t.Fatal("raw encryption responses missing")
	}
	decoded := outputs.resources["storage/bucket_encryption"]
	if decoded == nil || len(decoded.Regions) != 2 {
		t.Fatal("decoded encryption outputs missing")
	}
	for i, want := range []bool{true, false} {
		state, ok := decoded.Regions[i].Items.(*storage.GetBucketEncryptionOutput)
		if !ok || state == nil || state.Enabled != want || decoded.Regions[i].Error != "" {
			t.Fatalf("decoded output %d=%+v", i, decoded.Regions[i])
		}
		if !strings.Contains(string(captured.Regions[i].Body), `"encryption":`) {
			t.Fatalf("raw response %d missing encryption", i)
		}
	}
}
