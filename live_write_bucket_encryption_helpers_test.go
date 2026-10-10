//go:build livewrite

package vngcloud_test

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/routes"
	"danny.vn/vngcloud/internal/transport"
	"danny.vn/vngcloud/storage"
)

type encryptionResources struct {
	cfg          vngcloud.Config
	client       *storage.Client
	report       *encryptionReport
	project      string
	regionID     string
	ready        bool
	inventory    []storage.Bucket
	baseline     map[string]bool
	buckets      []string
	s3           *encryptionS3
	keyAttempted bool
	createdKey   string
}

func newEncryptionResources(cfg vngcloud.Config, report *encryptionReport, project string) *encryptionResources {
	return &encryptionResources{cfg: cfg, client: storage.New(cfg), report: report, project: project}
}

func (r *encryptionResources) region(ctx context.Context) error {
	if r.regionID != "" {
		return nil
	}
	regions, err := r.client.ListRegions(ctx, nil)
	if err != nil {
		return errors.New("region inventory failed")
	}
	for _, region := range regions.Items {
		if region.Name == "HCM04" && region.ID != "" {
			r.regionID = region.ID
			return nil
		}
	}
	return errors.New("HCM04 region missing")
}

// inventoryBody bypasses the SDK list's nil-as-empty decoding. Key responses
// remain sensitive, in memory only, and never reach a capture hook.
func (r *encryptionResources) inventoryBody(ctx context.Context, keys bool) (json.RawMessage, error) {
	if err := r.region(ctx); err != nil {
		return nil, err
	}
	parts := []string{"ceph", "projects", r.project}
	query := url.Values{"limit": {"1000"}}
	op := "storage.ListBuckets"
	if keys {
		parts = []string{"users", "s3_keys"}
		query = url.Values{"projectId": {r.project}}
		op = "storage.ListS3Keys"
	}
	client := core.ClientOf(r.cfg)
	var body json.RawMessage
	status, err := client.DoJSONStatus(ctx, transport.Request{
		Operation: op, Method: http.MethodGet, URL: client.RouteURL(routes.Route{Product: routes.ProductStorage, Version: "internal/v1", Parts: parts, Query: query}),
		Headers: map[string]string{"region": r.regionID, "region_id": r.regionID}, OK: []int{http.StatusOK}, Sensitive: keys,
	}, &body)
	if err != nil || status != http.StatusOK {
		return nil, errors.New("inventory request failed")
	}
	return body, nil
}

func decodeEncryptionInventory[T any](body []byte, identifier string) ([]T, error) {
	invalid := errors.New("inventory is missing, malformed, or incomplete")
	var envelope map[string]json.RawMessage
	if json.Unmarshal(body, &envelope) != nil || envelope == nil {
		return nil, invalid
	}
	var success bool
	if json.Unmarshal(envelope["success"], &success) != nil || !success {
		return nil, invalid
	}
	if page, ok := envelope["isNext"]; ok {
		var next *bool
		if json.Unmarshal(page, &next) != nil || next == nil || *next {
			return nil, invalid
		}
	}
	raw, ok := envelope["datas"]
	if !ok {
		raw = envelope["data"]
	}
	var rows []json.RawMessage
	if json.Unmarshal(raw, &rows) != nil || rows == nil {
		return nil, invalid
	}
	items := make([]T, len(rows))
	seen := map[string]bool{}
	for i, row := range rows {
		var fields map[string]json.RawMessage
		var id string
		if json.Unmarshal(row, &fields) != nil || fields == nil || json.Unmarshal(fields[identifier], &id) != nil || id == "" || seen[id] || json.Unmarshal(row, &items[i]) != nil {
			return nil, invalid
		}
		seen[id] = true
	}
	return items, nil
}

func (r *encryptionResources) listBuckets(ctx context.Context) ([]storage.Bucket, error) {
	body, err := r.inventoryBody(ctx, false)
	if err != nil {
		return nil, err
	}
	return decodeEncryptionInventory[storage.Bucket](body, "name")
}
func (r *encryptionResources) listKeys(ctx context.Context) ([]storage.S3Key, error) {
	body, err := r.inventoryBody(ctx, true)
	if err != nil {
		return nil, err
	}
	return decodeEncryptionInventory[storage.S3Key](body, "userKeyId")
}

func (r *encryptionResources) prepare(ctx context.Context) error {
	r.ready = false
	if err := core.CheckPathID("bucket encryption inventory", "ProjectID", r.project); err != nil {
		return errors.New("invalid project identifier")
	}
	buckets, err := r.listBuckets(ctx)
	if err != nil {
		return err
	}
	for _, b := range buckets {
		if !strings.HasPrefix(b.Name, "vngcloud-live-") {
			return errors.New("project contains a foreign bucket")
		}
	}
	keys, err := r.listKeys(ctx)
	if err != nil {
		return err
	}
	baseline := map[string]bool{}
	for _, k := range keys {
		baseline[k.UserKeyID] = true
	}
	r.inventory = buckets
	r.baseline = baseline
	r.ready = true
	return nil
}

func (r *encryptionResources) createBucket(ctx context.Context, bucket string, encrypted bool) error {
	if !r.ready {
		return errors.New("complete inventories required before writes")
	}
	for _, existing := range r.inventory {
		if existing.Name == bucket {
			return errors.New("bucket name collision")
		}
	}
	// A lost create response can still leave a bucket, so register it first.
	r.buckets = append(r.buckets, bucket)
	_, err := r.client.CreateBucket(ctx, &storage.CreateBucketInput{Region: "HCM04", ProjectID: r.project, Bucket: bucket, Encryption: encrypted})
	return err
}

func (r *encryptionResources) createKey(ctx context.Context) (*storage.CreateS3KeyOutput, error) {
	if !r.ready {
		return nil, errors.New("complete inventories required before writes")
	}
	r.keyAttempted = true
	key, err := r.client.CreateS3Key(ctx, &storage.CreateS3KeyInput{Region: "HCM04", ProjectID: r.project})
	if key != nil && key.UserKeyID != "" {
		if r.baseline[key.UserKeyID] {
			return nil, errors.New("key create returned a pre-existing key")
		}
		r.createdKey = key.UserKeyID
	}
	return key, err
}

func (r *encryptionResources) cleanupBuckets(ctx context.Context) []string {
	leftovers := []string{}
	for _, bucket := range r.buckets {
		_, err := r.client.GetBucket(ctx, &storage.GetBucketInput{Region: "HCM04", ProjectID: r.project, Bucket: bucket})
		if errors.Is(err, vngcloud.ErrNotFound) {
			continue
		}
		if err != nil {
			leftovers = append(leftovers, "bucket "+bucket+": contents unconfirmed")
			continue
		}
		if r.s3 != nil {
			if err := r.s3.empty(ctx, bucket); err != nil {
				leftovers = append(leftovers, "bucket "+bucket+": "+err.Error())
				continue
			}
		}
		_, err = r.client.DeleteBucket(ctx, &storage.DeleteBucketInput{Region: "HCM04", ProjectID: r.project, Bucket: bucket})
		if err != nil && !errors.Is(err, vngcloud.ErrNotFound) {
			leftovers = append(leftovers, "bucket "+bucket)
			continue
		}
		if _, err := r.client.GetBucket(ctx, &storage.GetBucketInput{Region: "HCM04", ProjectID: r.project, Bucket: bucket}); !errors.Is(err, vngcloud.ErrNotFound) {
			leftovers = append(leftovers, "bucket "+bucket)
		}
	}
	if len(r.buckets) > 0 {
		buckets, err := r.listBuckets(ctx)
		if err != nil {
			leftovers = append(leftovers, "bucket cleanup inventory unconfirmed")
		} else {
			for _, b := range buckets {
				for _, owned := range r.buckets {
					if b.Name == owned {
						leftovers = append(leftovers, "bucket "+owned)
					}
				}
			}
		}
	}
	return leftovers
}

func (r *encryptionResources) cleanupKeys(ctx context.Context) []string {
	if !r.keyAttempted {
		return nil
	}
	if r.baseline == nil {
		return []string{"temporary key baseline incomplete"}
	}
	keys, err := r.listKeys(ctx)
	if err != nil {
		return []string{"temporary key cleanup inventory unconfirmed"}
	}
	leftovers := []string{}
	// A delta alone cannot prove ownership: another client can create a key.
	// A lost create response leaves an unknown key for manual cleanup.
	if r.createdKey != "" && !r.baseline[r.createdKey] {
		for _, k := range keys {
			if k.UserKeyID == r.createdKey {
				if _, err := r.client.DeleteS3Key(ctx, &storage.DeleteS3KeyInput{Region: "HCM04", ProjectID: r.project, UserKeyID: r.createdKey}); err != nil {
					leftovers = append(leftovers, "key "+r.createdKey)
				}
			}
		}
	}
	keys, err = r.listKeys(ctx)
	if err != nil {
		return append(leftovers, "temporary key cleanup inventory unconfirmed")
	}
	for _, k := range keys {
		if !r.baseline[k.UserKeyID] {
			leftovers = append(leftovers, "key "+k.UserKeyID)
		}
	}
	if r.createdKey == "" {
		leftovers = append(leftovers, "temporary key creation unconfirmed")
	}
	return leftovers
}

func (s *encryptionS3) probeCopy(ctx context.Context, bucket, source, target string) (bool, error) {
	headers := http.Header{"X-Amz-Copy-Source": {"/" + bucket + "/" + source}}
	status, _, body, err := s.request(ctx, "PUT", bucket, target, nil, headers, nil)
	if err != nil {
		return false, err
	}
	s.report.check(s.t)
	var result struct {
		XMLName xml.Name
		ETag    string `xml:"ETag"`
		Code    string `xml:"Code"`
	}
	decoded := xml.Unmarshal(body, &result) == nil
	if status >= 400 || (decoded && result.XMLName.Local == "Error" && result.Code != "") {
		return false, nil
	}
	if status != 200 || !decoded || result.XMLName.Local != "CopyObjectResult" || result.ETag == "" {
		return false, errors.New("S3 copy response unconfirmed")
	}
	return true, nil
}

func (s *encryptionS3) compareStates(ctx context.Context, bucket string, payload []byte, toggle func(bool) error) error {
	compare := func(keys []string) {
		s.t.Helper()
		for _, key := range keys {
			s.require(ctx, "HEAD", bucket, key, nil, nil, nil, 200)
			_, body := s.require(ctx, "GET", bucket, key, nil, nil, nil, 200)
			if string(body) != string(payload) {
				s.t.Fatal("object read failed")
			}
		}
	}
	s.require(ctx, "PUT", bucket, "before", nil, nil, payload, 200)
	compare([]string{"before"})
	if err := toggle(true); err != nil {
		return err
	}
	compare([]string{"before"})
	s.require(ctx, "PUT", bucket, "during", nil, nil, payload, 200)
	keys := []string{"before", "during"}
	copied, err := s.probeCopy(ctx, bucket, "before", "copy")
	if err != nil {
		return err
	}
	if copied {
		keys = append(keys, "copy")
	}
	// A separate source lets move run even when the first copy was refused.
	s.require(ctx, "PUT", bucket, "move-source", nil, nil, payload, 200)
	moved, err := s.probeCopy(ctx, bucket, "move-source", "moved")
	if err != nil {
		return err
	}
	if moved {
		s.require(ctx, "DELETE", bucket, "move-source", nil, nil, nil, 204)
		s.require(ctx, "HEAD", bucket, "move-source", nil, nil, nil, 404)
		keys = append(keys, "moved")
	} else {
		keys = append(keys, "move-source")
	}
	compare(keys)
	if err := toggle(false); err != nil {
		return err
	}
	compare(keys)
	s.require(ctx, "PUT", bucket, "after", nil, nil, payload, 200)
	keys = append(keys, "after")
	compare(keys)
	if err := toggle(true); err != nil {
		return err
	}
	compare(keys)
	return nil
}
