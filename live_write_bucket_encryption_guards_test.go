//go:build livewrite

package vngcloud_test

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/endpoints"
	"danny.vn/vngcloud/internal/transport"
)

type encryptionFakeHTTP func(*http.Request) (*http.Response, error)

func (f encryptionFakeHTTP) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func encryptionResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}

func offlineEncryptionReport(t *testing.T) (*encryptionReport, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "report.jsonl")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := file.Close(); err != nil {
			t.Error(err)
		}
	})
	return &encryptionReport{file: file}, path
}

func offlineEncryptionConfig(t *testing.T, fn encryptionFakeHTTP) vngcloud.Config {
	t.Helper()
	tc := transport.New(transport.Config{HTTPClient: &http.Client{Transport: fn}})
	return core.NewTestConfig("hcm-3", "project-1", endpoints.Set{Storage: "https://console.invalid/", Region: "hcm-3"}, tc)
}

func TestBucketEncryptionInventoryGuards(t *testing.T) {
	validBuckets := `{"success":true,"isNext":false,"datas":[]}`
	validKeys := `{"success":true,"datas":[{"userKeyId":"old-key"}]}`
	cases := map[string]string{
		"missing":            `{"success":true}`,
		"null":               `{"success":true,"datas":null}`,
		"null with fallback": `{"success":true,"datas":null,"data":[]}`,
		"wrong shape":        `{"success":true,"datas":{}}`,
		"malformed":          `{"success":true,"datas":[`,
		"truncated":          `{"success":true,"isNext":true,"datas":[]}`,
		"null row":           `{"success":true,"datas":[null]}`,
		"missing identifier": `{"success":true,"datas":[{}]}`,
		"wrong pagination":   `{"success":true,"isNext":"false","datas":[]}`,
		"null pagination":    `{"success":true,"isNext":null,"datas":[]}`,
	}
	for _, kind := range []string{"bucket", "key"} {
		for name, bad := range cases {
			t.Run(kind+"/"+name, func(t *testing.T) {
				writes := 0
				cfg := offlineEncryptionConfig(t, func(r *http.Request) (*http.Response, error) {
					if r.Method != "GET" {
						writes++
						return encryptionResponse(200, `{"success":true}`), nil
					}
					if r.URL.Path == "/internal/v1/regions" {
						return encryptionResponse(200, `{"success":true,"datas":[{"regionId":"region-1","regionName":"HCM04"}]}`), nil
					}
					if r.URL.Path == "/internal/v1/ceph/projects/project-1" {
						body := validBuckets
						if kind == "bucket" {
							body = bad
						}
						return encryptionResponse(200, body), nil
					}
					if r.URL.Path == "/internal/v1/users/s3_keys" {
						body := validKeys
						if kind == "key" {
							body = bad
						}
						return encryptionResponse(200, body), nil
					}
					t.Errorf("unexpected request %s", r.URL.Path)
					return encryptionResponse(500, ""), nil
				})
				report, _ := offlineEncryptionReport(t)
				run := newEncryptionResources(cfg, report, "project-1")
				if err := run.prepare(t.Context()); err == nil {
					t.Error("incomplete inventory accepted")
				}
				if err := run.createBucket(t.Context(), "vngcloud-live-unit", false); err == nil {
					t.Error("write allowed after rejected inventory")
				}
				if _, err := run.createKey(t.Context()); err == nil {
					t.Error("key write allowed after rejected inventory")
				}
				if writes != 0 {
					t.Fatalf("writes=%d, want zero", writes)
				}
			})
		}
	}
	t.Run("foreign bucket", func(t *testing.T) {
		writes := 0
		cfg := offlineEncryptionConfig(t, func(r *http.Request) (*http.Response, error) {
			if r.Method != "GET" {
				writes++
				return encryptionResponse(200, `{"success":true}`), nil
			}
			if r.URL.Path == "/internal/v1/regions" {
				return encryptionResponse(200, `{"success":true,"datas":[{"regionId":"region-1","regionName":"HCM04"}]}`), nil
			}
			if r.URL.Path == "/internal/v1/users/s3_keys" {
				return encryptionResponse(200, validKeys), nil
			}
			return encryptionResponse(200, `{"success":true,"datas":[{"name":"production"}]}`), nil
		})
		report, _ := offlineEncryptionReport(t)
		run := newEncryptionResources(cfg, report, "project-1")
		if err := run.prepare(t.Context()); err == nil {
			t.Error("foreign bucket accepted")
		}
		_ = run.createBucket(t.Context(), "vngcloud-live-unit", false)
		_, _ = run.createKey(t.Context())
		if writes != 0 {
			t.Fatalf("writes=%d, want zero", writes)
		}
	})
}

func TestBucketEncryptionKeyCleanupOwnership(t *testing.T) {
	for _, inventory := range []string{"complete", "missing", "null", "truncated"} {
		t.Run(inventory, func(t *testing.T) {
			keys := map[string]bool{"old-key": true, "our-key": true, "other-key": true}
			var deleted []string
			cfg := offlineEncryptionConfig(t, func(r *http.Request) (*http.Response, error) {
				if r.Method == "DELETE" {
					id := strings.TrimPrefix(r.URL.Path, "/internal/v1/users/s3_keys/")
					deleted = append(deleted, id)
					delete(keys, id)
					return encryptionResponse(200, `{"success":true}`), nil
				}
				if r.URL.Path == "/internal/v1/regions" {
					return encryptionResponse(200, `{"success":true,"datas":[{"regionId":"region-1","regionName":"HCM04"}]}`), nil
				}
				if r.URL.Path != "/internal/v1/users/s3_keys" {
					t.Errorf("unexpected request %s", r.URL.Path)
				}
				switch inventory {
				case "missing":
					return encryptionResponse(200, `{"success":true}`), nil
				case "null":
					return encryptionResponse(200, `{"success":true,"datas":null}`), nil
				case "truncated":
					return encryptionResponse(200, `{"success":true,"datas":[],"isNext":true}`), nil
				}
				rows := []map[string]string{}
				for id := range keys {
					rows = append(rows, map[string]string{"userKeyId": id})
				}
				body, _ := json.Marshal(map[string]any{"success": true, "datas": rows})
				return encryptionResponse(200, string(body)), nil
			})
			report, _ := offlineEncryptionReport(t)
			run := newEncryptionResources(cfg, report, "project-1")
			run.baseline = map[string]bool{"old-key": true}
			run.createdKey = "our-key"
			run.keyAttempted = true
			_ = run.cleanupKeys(t.Context())
			want := 0
			if inventory == "complete" {
				want = 1
			}
			if len(deleted) != want || (want == 1 && deleted[0] != "our-key") || !keys["old-key"] || !keys["other-key"] {
				t.Fatalf("deleted=%v keys=%v", deleted, keys)
			}
		})
	}
}

func TestBucketEncryptionUnconfirmedKeyIsNotDeleted(t *testing.T) {
	deletes := 0
	cfg := offlineEncryptionConfig(t, func(r *http.Request) (*http.Response, error) {
		if r.Method == "DELETE" {
			deletes++
		}
		if r.URL.Path == "/internal/v1/regions" {
			return encryptionResponse(200, `{"success":true,"datas":[{"regionId":"region-1","regionName":"HCM04"}]}`), nil
		}
		return encryptionResponse(200, `{"success":true,"datas":[{"userKeyId":"old-key"},{"userKeyId":"unknown-key"}]}`), nil
	})
	report, _ := offlineEncryptionReport(t)
	run := newEncryptionResources(cfg, report, "project-1")
	run.baseline = map[string]bool{"old-key": true}
	run.keyAttempted = true
	left := run.cleanupKeys(t.Context())
	if deletes != 0 || len(left) == 0 {
		t.Fatalf("deletes=%d leftovers=%v", deletes, left)
	}
}

func TestBucketEncryptionBucketCleanupGuards(t *testing.T) {
	for _, kind := range []string{"objects", "versions", "markers", "uploads", "malformed", "truncated", "missing completeness", "wrong root", "failed list", "unknown 404"} {
		t.Run(kind, func(t *testing.T) {
			bucketDeletes := 0
			cfg := offlineEncryptionConfig(t, func(r *http.Request) (*http.Response, error) {
				if r.URL.Path == "/internal/v1/regions" {
					return encryptionResponse(200, `{"success":true,"datas":[{"regionId":"region-1","regionName":"HCM04"}]}`), nil
				}
				if r.Method == "DELETE" {
					bucketDeletes++
					return encryptionResponse(200, `{"success":true}`), nil
				}
				if bucketDeletes > 0 {
					return encryptionResponse(200, `{"success":false,"code":404}`), nil
				}
				return encryptionResponse(200, `{"success":true,"data":{"name":"vngcloud-live-unit","count":0,"size":0}}`), nil
			})
			report, _ := offlineEncryptionReport(t)
			run := newEncryptionResources(cfg, report, "project-1")
			run.buckets = []string{"vngcloud-live-unit"}
			objects, _ := newLiveObjects("https://s3.invalid", "HCM04", "fake-access", "fake-secret")
			objects.http = &http.Client{Transport: encryptionFakeHTTP(func(r *http.Request) (*http.Response, error) {
				if r.Method == "DELETE" {
					return encryptionResponse(204, ""), nil
				}
				root := "ListBucketResult"
				child := ""
				switch {
				case r.URL.Query().Has("uploads"):
					root = "ListMultipartUploadsResult"
					if kind == "uploads" {
						child = `<Upload><Key>object</Key><UploadId>upload-1</UploadId></Upload>`
					}
				case r.URL.Query().Has("versions"):
					root = "ListVersionsResult"
					if kind == "versions" {
						child = `<Version><Key>object</Key><VersionId>version-1</VersionId></Version>`
					}
					if kind == "markers" {
						child = `<DeleteMarker><Key>object</Key><VersionId>marker-1</VersionId></DeleteMarker>`
					}
				case kind == "objects":
					child = `<Contents><Key>object</Key></Contents>`
				}
				switch kind {
				case "malformed":
					return encryptionResponse(200, "<"), nil
				case "wrong root":
					return encryptionResponse(200, `<Error><Code>AccessDenied</Code></Error>`), nil
				case "failed list":
					return encryptionResponse(403, ""), nil
				case "unknown 404":
					return encryptionResponse(404, `<Error><Code>NoSuchUpload</Code></Error>`), nil
				case "missing completeness":
					return encryptionResponse(200, "<"+root+"/>"), nil
				}
				truncated := "false"
				if kind == "truncated" {
					truncated = "true"
				}
				return encryptionResponse(200, "<"+root+"><IsTruncated>"+truncated+"</IsTruncated>"+child+"</"+root+">"), nil
			})}
			run.s3 = &encryptionS3{objects: objects, report: report, t: t}
			left := run.cleanupBuckets(t.Context())
			if bucketDeletes != 0 || len(left) == 0 {
				t.Fatalf("bucket deletes=%d leftovers=%v", bucketDeletes, left)
			}
		})
	}
}

func TestBucketEncryptionCopyRefusalsContinueComparisons(t *testing.T) {
	for _, probe := range []string{"copy 403", "copy embedded", "move 403", "move embedded", "both"} {
		t.Run(probe, func(t *testing.T) {
			payload := []byte("offline object")
			objects := map[string][]byte{}
			report, path := offlineEncryptionReport(t)
			live, _ := newLiveObjects("https://s3.invalid", "HCM04", "fake-access", "fake-secret")
			live.http = &http.Client{Transport: encryptionFakeHTTP(func(r *http.Request) (*http.Response, error) {
				key := strings.TrimPrefix(r.URL.Path, "/bucket/")
				if r.Method == "PUT" && r.Header.Get("X-Amz-Copy-Source") != "" {
					refused := (key == "copy" && strings.HasPrefix(probe, "copy")) || (key == "moved" && strings.HasPrefix(probe, "move")) || probe == "both"
					if refused {
						status := 403
						if strings.HasSuffix(probe, "embedded") {
							status = 200
						}
						return encryptionResponse(status, `<Error><Code>AccessDenied</Code></Error>`), nil
					}
					objects[key] = payload
					return encryptionResponse(200, `<CopyObjectResult><ETag>fake</ETag></CopyObjectResult>`), nil
				}
				switch r.Method {
				case "PUT":
					objects[key] = payload
					return encryptionResponse(200, ""), nil
				case "DELETE":
					delete(objects, key)
					return encryptionResponse(204, ""), nil
				case "HEAD":
					if _, ok := objects[key]; !ok {
						return encryptionResponse(404, ""), nil
					}
					return encryptionResponse(200, ""), nil
				case "GET":
					return encryptionResponse(200, string(objects[key])), nil
				}
				t.Errorf("unexpected method %s", r.Method)
				return encryptionResponse(500, ""), nil
			})}
			s3 := &encryptionS3{objects: live, report: report, t: t}
			var toggles []bool
			err := s3.compareStates(t.Context(), "bucket", payload, func(enabled bool) error { toggles = append(toggles, enabled); return nil })
			if err != nil || len(toggles) != 3 || !toggles[0] || toggles[1] || !toggles[2] {
				t.Fatalf("toggles=%v err=%v", toggles, err)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			refusals := 0
			wantStatus := 403
			if strings.HasSuffix(probe, "embedded") {
				wantStatus = 200
			}
			for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
				var response struct {
					Status int    `json:"status"`
					Code   string `json:"errorCode"`
				}
				if json.Unmarshal([]byte(line), &response) != nil {
					t.Fatal("invalid private report")
				}
				if response.Code == "AccessDenied" {
					refusals++
					if response.Status != wantStatus {
						t.Fatalf("refusal status=%d want=%d", response.Status, wantStatus)
					}
				}
			}
			wantRefusals := 1
			if probe == "both" {
				wantRefusals = 2
			}
			if refusals != wantRefusals {
				t.Fatalf("recorded refusals=%d want=%d", refusals, wantRefusals)
			}
			if _, ok := objects["after"]; !ok {
				t.Fatal("after-disable upload missing")
			}
		})
	}
}

func TestBucketEncryptionCompleteInventoryAllowsCreate(t *testing.T) {
	for _, field := range []string{"datas", "data"} {
		t.Run(field, func(t *testing.T) {
			writes := 0
			cfg := offlineEncryptionConfig(t, func(r *http.Request) (*http.Response, error) {
				if r.Method == "POST" {
					writes++
					return encryptionResponse(200, `{"success":true}`), nil
				}
				switch r.URL.Path {
				case "/internal/v1/regions":
					return encryptionResponse(200, `{"success":true,"datas":[{"regionId":"region-1","regionName":"HCM04"}]}`), nil
				case "/internal/v1/ceph/projects/project-1/vngcloud-live-unit/details":
					return encryptionResponse(200, `{"success":true,"data":{"name":"vngcloud-live-unit","count":0,"size":0}}`), nil
				}
				return encryptionResponse(200, `{"success":true,"`+field+`":[]}`), nil
			})
			report, _ := offlineEncryptionReport(t)
			run := newEncryptionResources(cfg, report, "project-1")
			if err := run.prepare(t.Context()); err != nil {
				t.Fatal(err)
			}
			if err := run.createBucket(t.Context(), "vngcloud-live-unit", false); err != nil {
				t.Fatal(err)
			}
			if writes != 1 {
				t.Fatalf("writes=%d", writes)
			}
		})
	}
}

func TestBucketEncryptionKeyCleanupRequiresBaseline(t *testing.T) {
	for _, baseline := range []map[string]bool{nil, {"old-key": true}} {
		deletes := 0
		cfg := offlineEncryptionConfig(t, func(r *http.Request) (*http.Response, error) {
			if r.Method == "DELETE" {
				deletes++
			}
			if r.URL.Path == "/internal/v1/regions" {
				return encryptionResponse(200, `{"success":true,"datas":[{"regionId":"region-1","regionName":"HCM04"}]}`), nil
			}
			return encryptionResponse(200, `{"success":true,"datas":[{"userKeyId":"old-key"}]}`), nil
		})
		report, _ := offlineEncryptionReport(t)
		run := newEncryptionResources(cfg, report, "project-1")
		run.baseline = baseline
		run.keyAttempted = true
		run.createdKey = "old-key"
		_ = run.cleanupKeys(t.Context())
		if deletes != 0 {
			t.Fatalf("deleted a key without a complete baseline proving absence: %d", deletes)
		}
	}
}

func TestBucketEncryptionKeyCleanupVerificationRequiresCompleteList(t *testing.T) {
	for _, body := range []string{`{"success":true}`, `{"success":true,"datas":null}`, `{"success":true,"datas":[],"isNext":true}`} {
		reads, deletes := 0, 0
		cfg := offlineEncryptionConfig(t, func(r *http.Request) (*http.Response, error) {
			if r.URL.Path == "/internal/v1/regions" {
				return encryptionResponse(200, `{"success":true,"datas":[{"regionId":"region-1","regionName":"HCM04"}]}`), nil
			}
			if r.Method == "DELETE" {
				deletes++
				return encryptionResponse(200, `{"success":true}`), nil
			}
			reads++
			if reads == 1 {
				return encryptionResponse(200, `{"success":true,"datas":[{"userKeyId":"our-key"}]}`), nil
			}
			return encryptionResponse(200, body), nil
		})
		report, _ := offlineEncryptionReport(t)
		run := newEncryptionResources(cfg, report, "project-1")
		run.baseline = map[string]bool{}
		run.keyAttempted = true
		run.createdKey = "our-key"
		left := run.cleanupKeys(t.Context())
		if reads != 2 || deletes != 1 || len(left) == 0 {
			t.Fatalf("reads=%d deletes=%d leftovers=%v", reads, deletes, left)
		}
	}
}

func TestBucketEncryptionKeyInventoryStaysSensitive(t *testing.T) {
	report, path := offlineEncryptionReport(t)
	captures := 0
	tc := transport.New(transport.Config{
		HTTPClient: &http.Client{Transport: encryptionFakeHTTP(func(r *http.Request) (*http.Response, error) {
			switch r.URL.Path {
			case "/internal/v1/regions":
				return encryptionResponse(200, `{"success":true,"datas":[{"regionId":"region-1","regionName":"HCM04"}]}`), nil
			case "/internal/v1/users/s3_keys":
				return encryptionResponse(200, `{"success":true,"datas":[{"userKeyId":"old-key","secretKey":"unit-secret"}]}`), nil
			}
			return encryptionResponse(200, `{"success":true,"datas":[]}`), nil
		})},
		Capture: func(c transport.Capture) {
			if c.Operation == "storage.ListS3Keys" {
				captures++
			}
			report.capture(vngcloud.ResponseCapture{Operation: c.Operation, Method: c.Method, URL: c.URL, StatusCode: c.StatusCode, Body: c.Body})
		},
	})
	cfg := core.NewTestConfig("hcm-3", "project-1", endpoints.Set{Region: "hcm-3", Storage: "https://console.invalid/"}, tc)
	run := newEncryptionResources(cfg, report, "project-1")
	if err := run.prepare(t.Context()); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if captures != 0 || strings.Contains(string(data), "unit-secret") {
		t.Fatal("sensitive key inventory reached capture")
	}
}
