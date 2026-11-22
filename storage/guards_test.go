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

func TestDeleteBucketEnvelopeRefusalIsDefinite(t *testing.T) {
	for _, tc := range []struct {
		code int
		want error
	}{
		{112, core.ErrInvalidInput},
		{114, nil},
		{403, core.ErrPermission},
		{404, core.ErrNotFound},
		{429, core.ErrRateLimited},
	} {
		t.Run(strconv.Itoa(tc.code), func(t *testing.T) {
			var deletes, reads atomic.Int32
			h := serve(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet {
					reads.Add(1)
					_, _ = w.Write([]byte(presentBody))
					return
				}
				deletes.Add(1)
				_, _ = w.Write([]byte(`{"success":false,"code":` + strconv.Itoa(tc.code) + `,"errorMsg":"refused"}`))
			})
			_, err := newTestClient(t, h).DeleteBucket(context.Background(), &DeleteBucketInput{ProjectID: "proj-1", Bucket: "my-bucket"})
			var api *core.APIError
			if !errors.As(err, &api) || api.StatusCode != 200 || api.Code != strconv.Itoa(tc.code) || errors.Is(err, ErrNotSettled) {
				t.Fatalf("error = %v, want the definite envelope refusal", err)
			}
			if tc.want != nil && !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
			if deletes.Load() != 1 || reads.Load() != 1 {
				t.Fatalf("deletes = %d, reads = %d, want one each", deletes.Load(), reads.Load())
			}
		})
	}
}

func TestDeleteBucketMalformedRefusalIsUncertain(t *testing.T) {
	for _, body := range []string{
		`{"code":404}`,
		`{"success":null,"code":404}`,
		`{"success":false,"code":null}`,
		`{"success":false,"code":{}}`,
		`{"success":false,"code":503}`,
		`{"success":true,"success":false,"code":404}`,
		`{"success":false,"code":409,"CODE":404}`,
	} {
		t.Run(body, func(t *testing.T) {
			h := serve(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet {
					_, _ = w.Write([]byte(presentBody))
					return
				}
				_, _ = w.Write([]byte(body))
			})
			_, err := newTestClient(t, h).DeleteBucket(context.Background(), &DeleteBucketInput{ProjectID: "proj-1", Bucket: "my-bucket", NoWait: true})
			if !errors.Is(err, ErrNotSettled) {
				t.Fatalf("error = %v, want uncertainty", err)
			}
		})
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

func TestDeleteProjectRejectsNullPagination(t *testing.T) {
	for _, stage := range []string{"projects", "buckets"} {
		for _, key := range []string{"isNext", "ISNEXT"} {
			t.Run(stage+"/"+key, func(t *testing.T) {
				s := &projectWriteServer{before: projectList(newProjectJSON), buckets: `{"success":true,"datas":[]}`}
				if stage == "projects" {
					s.before = strings.TrimSuffix(s.before, "}") + `,"` + key + `": null}`
				} else {
					s.buckets = strings.TrimSuffix(s.buckets, "}") + `,"` + key + `": null}`
				}
				_, err := newTestClient(t, s.handler(t)).DeleteProject(context.Background(), &DeleteProjectInput{ProjectID: "new-1", NoWait: true})
				if err == nil || s.deletes != 0 {
					t.Fatalf("error = %v, deletes = %d, want an error and no DELETE", err, s.deletes)
				}
			})
		}
	}
}

func TestDeleteProjectAcceptsOmittedOrFalsePagination(t *testing.T) {
	for _, marker := range []string{"", `,"isNext":false`} {
		t.Run(marker, func(t *testing.T) {
			s := &projectWriteServer{
				before:  strings.TrimSuffix(projectList(newProjectJSON), "}") + marker + "}",
				buckets: `{"success":true,"datas":[]` + marker + "}",
			}
			_, err := newTestClient(t, s.handler(t)).DeleteProject(context.Background(), &DeleteProjectInput{ProjectID: "new-1", NoWait: true})
			if err != nil || s.deletes != 1 {
				t.Fatalf("error = %v, deletes = %d, want one accepted DELETE", err, s.deletes)
			}
		})
	}
}

func TestDeleteBucketRejectsMismatchedName(t *testing.T) {
	s := &bucketServer{status: 200, body: okEnvelope, detail: func(w http.ResponseWriter) {
		_, _ = w.Write([]byte(`{"success":true,"data":{"name":"other-bucket","count":0}}`))
	}}
	_, err := newDeleteClient(t, s.handler(t)).DeleteBucket(context.Background(), &DeleteBucketInput{ProjectID: "proj-1", Bucket: "my-bucket", NoWait: true})
	if err == nil || s.writes.Load() != 0 {
		t.Fatalf("error = %v, writes = %d, want an error and no DELETE", err, s.writes.Load())
	}
}

func TestDeleteProjectRejectsEvidenceCode403(t *testing.T) {
	for _, stage := range []string{"projects", "buckets"} {
		t.Run(stage, func(t *testing.T) {
			s := &projectWriteServer{before: projectList(newProjectJSON), buckets: `{"success":true,"datas":[]}`}
			if stage == "projects" {
				s.before = strings.TrimSuffix(s.before, "}") + `,"code":403}`
			} else {
				s.buckets = `{"success":true,"code":403,"datas":[]}`
			}
			_, err := newTestClient(t, s.handler(t)).DeleteProject(context.Background(), &DeleteProjectInput{ProjectID: "new-1", NoWait: true})
			if err == nil || s.deletes != 0 {
				t.Fatalf("error = %v, deletes = %d, want error and no DELETE", err, s.deletes)
			}
		})
	}
}

func TestDeleteProjectRejectsBucketListRedirect(t *testing.T) {
	s := &projectWriteServer{before: projectList(newProjectJSON)}
	base := s.handler(t)
	var redirected atomic.Int32
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/internal/v1/ceph/projects/new-1" {
			http.Redirect(w, r, "/other-project", http.StatusFound)
			return
		}
		if r.URL.Path == "/other-project" {
			redirected.Add(1)
			_, _ = w.Write([]byte(`{"success":true,"datas":[]}`))
			return
		}
		base.ServeHTTP(w, r)
	})
	_, err := newTestClient(t, h).DeleteProject(context.Background(), &DeleteProjectInput{ProjectID: "new-1", NoWait: true})
	if err == nil || s.deletes != 0 || redirected.Load() != 0 {
		t.Fatalf("error = %v, deletes = %d, redirected requests = %d", err, s.deletes, redirected.Load())
	}
}

func TestDeleteEvidenceRejectsOtherRedirects(t *testing.T) {
	for _, stage := range []string{"projects", "bucket-details"} {
		t.Run(stage, func(t *testing.T) {
			var deletes, redirected atomic.Int32
			h := serve(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodDelete {
					deletes.Add(1)
					_, _ = w.Write([]byte(okEnvelope))
					return
				}
				if r.URL.Path == "/other" {
					redirected.Add(1)
					_, _ = w.Write([]byte(`{"success":true,"data":{"name":"my-bucket","count":0}}`))
					return
				}
				http.Redirect(w, r, "/other", http.StatusFound)
			})
			var err error
			if stage == "projects" {
				_, err = newTestClient(t, h).DeleteProject(context.Background(), &DeleteProjectInput{ProjectID: "new-1", NoWait: true})
			} else {
				_, err = newDeleteClient(t, h).DeleteBucket(context.Background(), &DeleteBucketInput{ProjectID: "proj-1", Bucket: "my-bucket", NoWait: true})
			}
			if err == nil || deletes.Load() != 0 || redirected.Load() != 0 {
				t.Fatalf("error = %v, deletes = %d, redirected requests = %d", err, deletes.Load(), redirected.Load())
			}
		})
	}
}

func TestDeleteBucketRejectsEvidenceCode403(t *testing.T) {
	s := &bucketServer{status: 200, body: okEnvelope, detail: func(w http.ResponseWriter) {
		_, _ = w.Write([]byte(`{"success":true,"code":403,"data":{"name":"my-bucket","count":0}}`))
	}}
	_, err := newDeleteClient(t, s.handler(t)).DeleteBucket(context.Background(), &DeleteBucketInput{ProjectID: "proj-1", Bucket: "my-bucket", NoWait: true})
	if err == nil || s.writes.Load() != 0 {
		t.Fatalf("error = %v, deletes = %d", err, s.writes.Load())
	}
}

func TestDeleteBucketRejectsUnverifiedEvidenceEnvelope(t *testing.T) {
	for _, field := range []string{`"code":null`, `"code":403`, `"unexpected":true`, `"İsNext":true`, `"isNext":true`} {
		t.Run(field, func(t *testing.T) {
			s := &bucketServer{status: 200, body: okEnvelope, detail: func(w http.ResponseWriter) {
				_, _ = w.Write([]byte(`{"success":true,"data":{"name":"my-bucket","count":0},` + field + `}`))
			}}
			_, err := newDeleteClient(t, s.handler(t)).DeleteBucket(context.Background(), &DeleteBucketInput{ProjectID: "proj-1", Bucket: "my-bucket", NoWait: true})
			if err == nil || s.writes.Load() != 0 {
				t.Fatalf("error = %v, deletes = %d", err, s.writes.Load())
			}
		})
	}
}

func TestDeleteProjectRejectsUnsupportedPagination(t *testing.T) {
	for _, stage := range []string{"projects", "buckets"} {
		for _, marker := range []string{`"hasMore":true`, `"HASMORE":true`, `"nextPage":1`, `"next":"token"`, `"total":2`, `"totalItems":2`, `"TOTALITEMS":2`, `"unexpected":true`, `"UNEXPECTED":true`, `"İsNext":true`} {
			t.Run(stage+"/"+marker, func(t *testing.T) {
				s := &projectWriteServer{before: projectList(newProjectJSON), buckets: `{"success":true,"datas":[]}`}
				if stage == "projects" {
					s.before = strings.TrimSuffix(s.before, "}") + "," + marker + "}"
				} else {
					s.buckets = strings.TrimSuffix(s.buckets, "}") + "," + marker + "}"
				}
				_, err := newTestClient(t, s.handler(t)).DeleteProject(context.Background(), &DeleteProjectInput{ProjectID: "new-1", NoWait: true})
				if err == nil || s.deletes != 0 {
					t.Fatalf("error = %v, deletes = %d, want an error and no DELETE", err, s.deletes)
				}
			})
		}
	}
}

func TestDeleteBucketRejectsConflictingUsage(t *testing.T) {
	for _, fields := range []string{`"count":1,"count":0`, `"count":1,"COUNT":0`, `"count":-1`, `"count":0,"size":-1`, `"count":0,"usedCapacity":1,"USEDCAPACITY":0`} {
		s := &bucketServer{status: 200, body: okEnvelope, detail: func(w http.ResponseWriter) {
			_, _ = w.Write([]byte(`{"success":true,"data":{"name":"my-bucket",` + fields + `}}`))
		}}
		_, err := newDeleteClient(t, s.handler(t)).DeleteBucket(context.Background(), &DeleteBucketInput{ProjectID: "proj-1", Bucket: "my-bucket", NoWait: true})
		if err == nil || s.writes.Load() != 0 {
			t.Fatalf("error = %v, writes = %d", err, s.writes.Load())
		}
		if strings.Contains(fields, ":-1") && !errors.Is(err, ErrBucketNotEmpty) {
			t.Fatalf("fields = %s, error = %v, want ErrBucketNotEmpty", fields, err)
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
