//go:build livewrite

package livetest_test

import (
	"bytes"
	"context"
	"crypto/md5" //nolint:gosec // S3 DeleteObjects requires Content-MD5, not a security hash.
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/storage"
)

// encryptionReport contains no request headers or key responses. Account data
// and raw responses stay in the ignored private output directory.
type encryptionReport struct {
	mu     sync.Mutex
	file   *os.File
	failed bool
}

func (r *encryptionReport) record(value any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if json.NewEncoder(r.file).Encode(value) != nil {
		r.failed = true
	}
}

func (r *encryptionReport) check(t *testing.T) {
	t.Helper()
	r.mu.Lock()
	failed := r.failed
	r.mu.Unlock()
	if failed {
		t.Fatal("private report failed")
	}
}

func (r *encryptionReport) capture(c vngcloud.ResponseCapture) {
	switch c.Operation {
	case "storage.CreateBucket", "storage.GetBucket", "storage.GetBucketEncryption", "storage.PutBucketEncryption", "storage.DeleteBucket", "storage.ListBuckets":
		r.record(map[string]any{"operation": c.Operation, "method": c.Method, "url": c.URL, "status": c.StatusCode, "body": string(c.Body)})
	}
}

type encryptionS3 struct {
	objects *liveObjects
	report  *encryptionReport
	t       *testing.T
}

func (s *encryptionS3) request(ctx context.Context, method, bucket, key string, q url.Values, headers http.Header, body []byte) (int, http.Header, []byte, error) {
	u := *s.objects.base
	u.Path = "/" + bucket
	if key != "" {
		u.Path += "/" + key
	}
	u.RawPath = s3EncodePath(u.Path)
	u.RawQuery = canonicalQuery(q)
	req, err := http.NewRequestWithContext(ctx, method, u.String(), bytes.NewReader(body)) //nolint:gosec // The host is fixed; S3 keys only change the path.
	if err != nil {
		return 0, nil, nil, errors.New("build S3 request failed")
	}
	req.Header = headers.Clone()
	if req.Header == nil {
		req.Header = make(http.Header)
	}
	s.objects.signer.sign(req, body, time.Now())
	resp, err := s.objects.http.Do(req) //nolint:gosec // The host is fixed and redirects are refused.
	if err != nil {
		return 0, nil, nil, errors.New("S3 request failed")
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	var result struct {
		Code string `xml:"Code"`
	}
	_ = xml.Unmarshal(data, &result)
	metadata := map[string]string{}
	for _, name := range []string{"ETag", "Content-Length", "Content-Type", "Last-Modified", "X-Amz-Server-Side-Encryption", "X-Amz-Version-Id"} {
		metadata[name] = resp.Header.Get(name)
	}
	s.report.record(map[string]any{"headers": metadata, "operation": "S3", "method": method, "bucket": bucket, "key": key, "query": q, "status": resp.StatusCode, "encryption": resp.Header.Get("X-Amz-Server-Side-Encryption"), "etag": resp.Header.Get("ETag"), "version": resp.Header.Get("X-Amz-Version-Id"), "errorCode": result.Code, "body": string(data)})
	s.t.Logf("S3 status=%d", resp.StatusCode)
	if err != nil {
		return resp.StatusCode, resp.Header, nil, errors.New("read S3 response failed")
	}
	return resp.StatusCode, resp.Header, data, nil
}

func (s *encryptionS3) require(ctx context.Context, method, bucket, key string, q url.Values, headers http.Header, body []byte, want int) (http.Header, []byte) {
	s.t.Helper()
	status, h, data, err := s.request(ctx, method, bucket, key, q, headers, body)
	s.report.check(s.t)
	if err != nil || status != want {
		s.t.Fatalf("S3 failed: status=%d expected=%d", status, want)
	}
	return h, data
}

func TestLiveWriteBucketEncryption(t *testing.T) {
	if os.Getenv("VNGCLOUD_LIVE_WRITE") != "1" || os.Getenv("VNGCLOUD_LIVE_BUCKET_ENCRYPTION") != "1" {
		t.Skip("set VNGCLOUD_LIVE_WRITE=1 and VNGCLOUD_LIVE_BUCKET_ENCRYPTION=1")
	}
	project := os.Getenv("VNGCLOUD_LIVE_STORAGE_PROJECT_ID")
	if project == "" {
		t.Fatal("set VNGCLOUD_LIVE_STORAGE_PROJECT_ID to the approved throwaway HCM04 project")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	suffix, err := randomHex(4)
	if err != nil {
		t.Fatal("generate name failed")
	}
	dir := repoPath("examples/basic/output/raw/storage")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal("create report directory failed")
	}
	path := filepath.Join(dir, "bucket-encryption-"+suffix+".jsonl")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal("create private report failed")
	}
	report := &encryptionReport{file: file}
	// Registered first so resource cleanup also reaches the report.
	t.Cleanup(func() {
		report.mu.Lock()
		defer report.mu.Unlock()
		if report.failed {
			t.Error("private report failed")
		}
		if report.file.Close() != nil {
			t.Error("close report failed")
		}
	})
	cfg := liveWriteConfig(ctx, t, vngcloud.WithResponseCapture(report.capture))
	run := newEncryptionResources(cfg, report, project)
	if err := run.prepare(ctx); err != nil {
		t.Fatal("project or key inventory check failed; no writes sent")
	}
	client := run.client
	bucket := "vngcloud-live-" + suffix
	beforeBucket := bucket + "-before"
	for _, existing := range run.inventory {
		if existing.Name == bucket || existing.Name == beforeBucket {
			t.Fatal("bucket name collision; no writes sent")
		}
	}
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		leftovers := append(run.cleanupBuckets(cleanup), run.cleanupKeys(cleanup)...)
		report.record(map[string]any{"leftovers": leftovers, "projectDeleted": false})
		t.Logf("cleanup leftovers=%d", len(leftovers))
		if len(leftovers) > 0 {
			t.Errorf("cleanup failed; %d leftovers named in %s", len(leftovers), path)
		}
	})
	create := func(b string, encrypted bool) {
		t.Helper()
		if err := run.createBucket(ctx, b, encrypted); err != nil {
			t.Fatal("create failed")
		}
		report.check(t)
	}
	read := func(b string, want bool) {
		t.Helper()
		got, err := client.GetBucketEncryption(ctx, &storage.GetBucketEncryptionInput{Region: "HCM04", ProjectID: project, BucketName: b})
		if err != nil || got.Enabled != want {
			t.Fatal("encryption read failed")
		}
		report.check(t)
		t.Log("encryption read pass")
	}
	toggle := func(b string, enabled bool) {
		t.Helper()
		if _, err := client.PutBucketEncryption(ctx, &storage.PutBucketEncryptionInput{Region: "HCM04", ProjectID: project, BucketName: b, Enabled: enabled}); err != nil {
			t.Fatal("encryption write failed")
		}
		read(b, enabled)
	}
	create(bucket, true)
	read(bucket, true)
	toggle(bucket, false)
	toggle(bucket, true)
	toggle(bucket, true)
	missing := bucket + "-missing"
	_, getErr := client.GetBucketEncryption(ctx, &storage.GetBucketEncryptionInput{Region: "HCM04", ProjectID: project, BucketName: missing})
	report.check(t)
	if !errors.Is(getErr, vngcloud.ErrNotFound) {
		t.Fatal("missing bucket read check failed")
	}
	_, putErr := client.PutBucketEncryption(ctx, &storage.PutBucketEncryptionInput{Region: "HCM04", ProjectID: project, BucketName: missing, Enabled: true})
	report.check(t)
	if !errors.Is(putErr, vngcloud.ErrNotFound) {
		t.Fatal("missing bucket write check failed")
	}
	key, err := run.createKey(ctx)
	if err != nil || key == nil {
		t.Fatal("temporary S3 key failed")
	}
	objects, err := newLiveObjects("https://hcm04.vstorage.vngcloud.vn", "HCM04", key.AccessKey, key.SecretKey.Reveal())
	if err != nil {
		t.Fatal("S3 setup failed")
	}
	s3 := &encryptionS3{objects: objects, report: report, t: t}
	run.s3 = s3
	// An algorithm or a service error is evidence, not an assumed contract.
	status, _, _, err := s3.request(ctx, "GET", bucket, "", url.Values{"encryption": {""}}, nil, nil)
	report.check(t)
	if err != nil || status < 200 || status >= 500 {
		t.Fatalf("S3 encryption check failed: status=%d", status)
	}
	payload := []byte("vngcloud encryption verification\n")
	s3.require(ctx, "PUT", bucket, "enabled", nil, nil, payload, 200)
	conditional := http.Header{"If-None-Match": {"*"}}
	s3.require(ctx, "PUT", bucket, "conditional", nil, conditional, payload, 200)
	s3.require(ctx, "PUT", bucket, "conditional", nil, conditional, payload, 412)
	s3.multipart(ctx, bucket)
	s3.require(ctx, "PUT", bucket, "delete-one", nil, nil, payload, 200)
	s3.require(ctx, "PUT", bucket, "delete-two", nil, nil, payload, 200)
	s3.deleteObjects(ctx, bucket, []string{"delete-one", "delete-two"})
	for _, k := range []string{"delete-one", "delete-two"} {
		s3.require(ctx, "HEAD", bucket, k, nil, nil, nil, 404)
	}
	create(beforeBucket, false)
	read(beforeBucket, false)
	if err := s3.compareStates(ctx, beforeBucket, payload, func(enabled bool) error {
		toggle(beforeBucket, enabled)
		return nil
	}); err != nil {
		t.Fatal("object comparisons failed")
	}
	report.record(map[string]any{"result": "pass", "unresolved": []string{"effects invisible through S3", "encryption in other regions"}})
	t.Log("encryption checks pass")
}

func (s *encryptionS3) multipart(ctx context.Context, bucket string) {
	s.t.Helper()
	_, body := s.require(ctx, "POST", bucket, "multipart", url.Values{"uploads": {""}}, nil, nil, 200)
	var started struct {
		UploadID string `xml:"UploadId"`
	}
	if xml.Unmarshal(body, &started) != nil || started.UploadID == "" {
		s.t.Fatal("multipart initiation failed")
	}
	type part struct {
		Number int    `xml:"PartNumber"`
		ETag   string `xml:"ETag"`
	}
	complete := struct {
		XMLName xml.Name `xml:"CompleteMultipartUpload"`
		Parts   []part   `xml:"Part"`
	}{}
	for i := 1; i <= 2; i++ {
		h, _ := s.require(ctx, "PUT", bucket, "multipart", url.Values{"uploadId": {started.UploadID}, "partNumber": {strconv.Itoa(i)}}, nil, bytes.Repeat([]byte("x"), 6<<20), 200)
		if h.Get("ETag") == "" {
			s.t.Fatal("multipart ETag missing")
		}
		complete.Parts = append(complete.Parts, part{Number: i, ETag: h.Get("ETag")})
	}
	payload, err := xml.Marshal(complete)
	if err != nil {
		s.t.Fatal("multipart body failed")
	}
	_, body = s.require(ctx, "POST", bucket, "multipart", url.Values{"uploadId": {started.UploadID}}, http.Header{"Content-Type": {"application/xml"}}, payload, 200)
	var result struct {
		XMLName xml.Name
		ETag    string `xml:"ETag"`
	}
	if xml.Unmarshal(body, &result) != nil || result.XMLName.Local != "CompleteMultipartUploadResult" || result.ETag == "" {
		s.t.Fatal("multipart completion failed")
	}
	h, _ := s.require(ctx, "HEAD", bucket, "multipart", nil, nil, nil, 200)
	if h.Get("Content-Length") != strconv.Itoa(12<<20) {
		s.t.Fatal("multipart size failed")
	}
	_, body = s.require(ctx, "GET", bucket, "multipart", nil, nil, nil, 200)
	if !bytes.Equal(body, bytes.Repeat([]byte("x"), 12<<20)) {
		s.t.Fatal("multipart read failed")
	}
}

func (s *encryptionS3) deleteObjects(ctx context.Context, bucket string, keys []string) {
	s.t.Helper()
	type object struct {
		Key string `xml:"Key"`
	}
	request := struct {
		XMLName xml.Name `xml:"Delete"`
		Objects []object `xml:"Object"`
	}{}
	for _, k := range keys {
		request.Objects = append(request.Objects, object{Key: k})
	}
	payload, err := xml.Marshal(request)
	if err != nil {
		s.t.Fatal("DeleteObjects body failed")
	}
	sum := md5.Sum(payload) //nolint:gosec // S3 requires MD5 for this wire checksum.
	_, body := s.require(ctx, "POST", bucket, "", url.Values{"delete": {""}}, http.Header{"Content-MD5": {base64.StdEncoding.EncodeToString(sum[:])}, "Content-Type": {"application/xml"}}, payload, 200)
	var result struct {
		XMLName xml.Name
		Deleted []object `xml:"Deleted"`
		Errors  []object `xml:"Error"`
	}
	if xml.Unmarshal(body, &result) != nil || result.XMLName.Local != "DeleteResult" || len(result.Errors) != 0 || len(result.Deleted) != len(keys) {
		s.t.Fatal("DeleteObjects failed")
	}
}

// empty lists uploads and versions even after a lost write response. It refuses
// a truncated listing instead of deleting a bucket whose contents are unknown.
func (s *encryptionS3) empty(ctx context.Context, bucket string) error {
	status, _, body, err := s.request(ctx, "GET", bucket, "", url.Values{"uploads": {""}}, nil, nil)
	if err != nil || status != 200 {
		return errors.New("uploads unconfirmed")
	}
	var uploads struct {
		XMLName   xml.Name
		Truncated *bool `xml:"IsTruncated"`
		Uploads   []struct {
			Key string `xml:"Key"`
			ID  string `xml:"UploadId"`
		} `xml:"Upload"`
	}
	if xml.Unmarshal(body, &uploads) != nil || uploads.XMLName.Local != "ListMultipartUploadsResult" || uploads.Truncated == nil || *uploads.Truncated { //nolint:gosec // Bounded XML decodes into fixed fields without external entities.
		return errors.New("uploads listing incomplete")
	}
	for _, u := range uploads.Uploads {
		status, _, _, err = s.request(ctx, "DELETE", bucket, u.Key, url.Values{"uploadId": {u.ID}}, nil, nil)
		if err != nil || status != 204 {
			return fmt.Errorf("upload %s/%s", u.Key, u.ID)
		}
	}
	status, _, body, err = s.request(ctx, "GET", bucket, "", url.Values{"versions": {""}}, nil, nil)
	if err != nil || status != 200 {
		return errors.New("versions unconfirmed")
	}
	type version struct {
		Key string `xml:"Key"`
		ID  string `xml:"VersionId"`
	}
	var versions struct {
		XMLName   xml.Name
		Truncated *bool     `xml:"IsTruncated"`
		Versions  []version `xml:"Version"`
		Markers   []version `xml:"DeleteMarker"`
	}
	if xml.Unmarshal(body, &versions) != nil || versions.XMLName.Local != "ListVersionsResult" || versions.Truncated == nil || *versions.Truncated { //nolint:gosec // Bounded XML decodes into fixed fields without external entities.
		return errors.New("versions listing incomplete")
	}
	for _, v := range append(versions.Versions, versions.Markers...) {
		status, _, _, err = s.request(ctx, "DELETE", bucket, v.Key, url.Values{"versionId": {v.ID}}, nil, nil)
		if err != nil || status != 204 {
			return fmt.Errorf("version %s/%s", v.Key, v.ID)
		}
	}
	status, _, body, err = s.request(ctx, "GET", bucket, "", url.Values{"list-type": {"2"}}, nil, nil)
	if err != nil || status != 200 {
		return errors.New("objects unconfirmed")
	}
	var listing struct {
		XMLName   xml.Name
		Truncated *bool `xml:"IsTruncated"`
		Contents  []struct {
			Key string `xml:"Key"`
		} `xml:"Contents"`
	}
	if xml.Unmarshal(body, &listing) != nil || listing.XMLName.Local != "ListBucketResult" || listing.Truncated == nil || *listing.Truncated { //nolint:gosec // Bounded XML decodes into fixed fields without external entities.
		return errors.New("objects listing incomplete")
	}
	for _, o := range listing.Contents {
		status, _, _, err = s.request(ctx, "DELETE", bucket, o.Key, nil, nil, nil)
		if err != nil || status != 204 {
			return fmt.Errorf("object %s", o.Key)
		}
	}
	// Re-list all three resource kinds before deleting their bucket.
	for _, q := range []url.Values{{"uploads": {""}}, {"versions": {""}}, {"list-type": {"2"}}} {
		status, _, body, err = s.request(ctx, "GET", bucket, "", q, nil, nil)
		expected := "ListBucketResult"
		if q.Has("uploads") {
			expected = "ListMultipartUploadsResult"
		}
		if q.Has("versions") {
			expected = "ListVersionsResult"
		}
		var remaining struct {
			XMLName   xml.Name
			Truncated *bool      `xml:"IsTruncated"`
			Uploads   []struct{} `xml:"Upload"`
			Versions  []struct{} `xml:"Version"`
			Markers   []struct{} `xml:"DeleteMarker"`
			Objects   []struct{} `xml:"Contents"`
		}
		if err != nil || status != 200 || xml.Unmarshal(body, &remaining) != nil || remaining.XMLName.Local != expected || remaining.Truncated == nil || *remaining.Truncated || len(remaining.Uploads)+len(remaining.Versions)+len(remaining.Markers)+len(remaining.Objects) != 0 { //nolint:gosec // Bounded XML decodes into fixed fields without external entities.
			return errors.New("objects, versions, markers, or uploads remain")
		}
	}
	return nil
}
