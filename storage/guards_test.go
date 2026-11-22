package storage

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/endpoints"
	"danny.vn/vngcloud/internal/testutil"
	"danny.vn/vngcloud/internal/transport"
)

func TestDeleteBucketSendsOnceAfterEmptyRead(t *testing.T) {
	for _, status := range []int{307, 308, 401, 429, 503} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			var deletes, reads atomic.Int32
			h := serve(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet {
					reads.Add(1)
					_, _ = w.Write([]byte(presentBody))
					return
				}
				deletes.Add(1)
				w.Header().Set("Location", bucketsPath)
				w.WriteHeader(status)
			})
			cfg := testutil.NewRetryConfig(t, h)
			_, err := New(cfg).DeleteBucket(context.Background(), &DeleteBucketInput{ProjectID: "proj-1", Bucket: "my-bucket", NoWait: true})
			if err == nil || deletes.Load() != 1 || reads.Load() != 1 {
				t.Fatalf("error = %v, deletes = %d, reads = %d", err, deletes.Load(), reads.Load())
			}
			if status == 503 && (!errors.Is(err, ErrNotSettled) || !strings.Contains(err.Error(), "may have happened")) {
				t.Fatalf("ambiguous delete lacks recovery: %v", err)
			}
		})
	}
}

func TestDeleteBucketSendsOnceAfterAuthFailure(t *testing.T) {
	var deletes atomic.Int32
	tokens := &credentialTestTokens{values: []string{"synthetic-token-one", "synthetic-token-two"}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(presentBody))
			return
		}
		deletes.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()
	c := New(core.NewTestConfig("hcm-3", "project-1", endpoints.Set{Storage: server.URL + "/"}, transport.New(transport.Config{HTTPClient: server.Client(), TokenSource: tokens, RetryCount: 3})))
	c.regionIDs = map[string]string{"HCM04": "region-1"}
	_, err := c.DeleteBucket(context.Background(), &DeleteBucketInput{ProjectID: "proj-1", Bucket: "my-bucket", NoWait: true})
	if err == nil || deletes.Load() != 1 || tokens.calls.Load() != 1 {
		t.Fatalf("error = %v, deletes = %d, token reads = %d", err, deletes.Load(), tokens.calls.Load())
	}
}

func TestDeleteProjectRequiresExplicitUnambiguousLists(t *testing.T) {
	for _, body := range []string{
		`{"success":true}`,
		`{"success":true,"datas":null}`,
		`{"success":true,"data":[{"name":"occupied"}],"datas":[]}`,
		`{"success":true,"datas":[{"name":"occupied"}],"datas":[]}`,
		`{"success":true,"datas":[{"name":"occupied"}],"DATAS":[]}`,
		`{"success":true,"datas":[],"isNext":true,"ISNEXT":false}`,
		`{"success":false,"SUCCESS":true,"datas":[]}`,
	} {
		t.Run(body, func(t *testing.T) {
			s := &projectWriteServer{before: projectList(newProjectJSON), buckets: body}
			_, err := newTestClient(t, s.handler(t)).DeleteProject(context.Background(), &DeleteProjectInput{ProjectID: "new-1", NoWait: true})
			if err == nil || s.deletes != 0 {
				t.Fatalf("error = %v, deletes = %d", err, s.deletes)
			}
		})
	}
}

func TestDeleteBucketRejectsConflictingUsage(t *testing.T) {
	for _, fields := range []string{`"count":1,"count":0`, `"count":1,"COUNT":0`, `"count":-1`, `"count":0,"size":-1`, `"count":0,"usedCapacity":1,"USEDCAPACITY":0`} {
		s := &bucketServer{status: 200, body: okEnvelope, detail: func(w http.ResponseWriter) {
			_, _ = w.Write([]byte(`{"success":true,"data":{` + fields + `}}`))
		}}
		_, err := newDeleteClient(t, s.handler(t)).DeleteBucket(context.Background(), &DeleteBucketInput{ProjectID: "proj-1", Bucket: "my-bucket", NoWait: true})
		if err == nil || s.writes.Load() != 0 {
			t.Fatalf("error = %v, writes = %d", err, s.writes.Load())
		}
	}
}

func TestDeleteProjectRejectsCaseVariantIdentity(t *testing.T) {
	project := strings.Replace(newProjectJSON, `"projectId":"new-1"`, `"projectId":"other","PROJECTID":"new-1"`, 1)
	s := &projectWriteServer{before: projectList(project)}
	_, err := newTestClient(t, s.handler(t)).DeleteProject(context.Background(), &DeleteProjectInput{ProjectID: "new-1", NoWait: true})
	if err == nil || s.deletes != 0 {
		t.Fatalf("error = %v, deletes = %d", err, s.deletes)
	}
}

func TestRegionRejectsCaseVariantIdentity(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"success":true,"datas":[{"regionId":"other","REGIONID":"region-1","regionName":"HCM04"}]}`))
	}))
	if _, err := c.ListRegions(context.Background(), nil); err == nil {
		t.Fatal("conflicting region identity accepted")
	}
}
