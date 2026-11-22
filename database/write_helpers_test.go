package database

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"danny.vn/vngcloud"
)

const testPassword = "Canarycanarycanary1"

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type recorded struct {
	Method, Path, Query string
	Header              http.Header
	Body                string
}

type fakeClock struct {
	mu     sync.Mutex
	now    time.Time
	sleeps []time.Duration
	onSlp  func(int)
}

func (f *fakeClock) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

func (f *fakeClock) Sleep(ctx context.Context, d time.Duration) error {
	f.mu.Lock()
	f.sleeps = append(f.sleeps, d)
	n := len(f.sleeps)
	hook := f.onSlp
	f.mu.Unlock()
	if hook != nil {
		hook(n)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	f.mu.Lock()
	f.now = f.now.Add(d)
	f.mu.Unlock()
	return nil
}

type harness struct {
	t        *testing.T
	client   *Client
	clock    *fakeClock
	mu       sync.Mutex
	requests []recorded
}

func (h *harness) log() []recorded {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]recorded(nil), h.requests...)
}

func (h *harness) count(method, path string) int {
	n := 0
	for _, r := range h.log() {
		if r.Method == method && r.Path == path {
			n++
		}
	}
	return n
}

func (h *harness) posts() int {
	n := 0
	for _, r := range h.log() {
		if r.Method == http.MethodPost {
			n++
		}
	}
	return n
}

func iamConfig(t *testing.T, rt http.RoundTripper, extra ...vngcloud.LoadOption) vngcloud.Config {
	t.Helper()
	auth := roundTrip(func(r *http.Request) (*http.Response, error) {
		w := httptest.NewRecorder()
		switch {
		case r.URL.Host == "signin.greennode.ai" && r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`<html><input name="_csrf" value="csrf"></html>`))
		case r.URL.Host == "signin.greennode.ai":
			w.Header().Set("Location", "https://dashboard.console.greennode.ai/?code=synthetic")
			w.WriteHeader(http.StatusFound)
		default:
			_, _ = w.Write([]byte(`{"accessToken":"bearer-synthetic","expiresIn":3600}`))
		}
		return w.Result(), nil
	})
	opts := []vngcloud.LoadOption{vngcloud.WithRegion("hcm-3"), vngcloud.WithIAMUser(&vngcloud.IAMUserAuth{RootEmail: "root", Username: "user", Password: "synthetic", HTTPClient: &http.Client{Transport: auth}}), vngcloud.WithTransport(rt), vngcloud.WithRetry(0, 0)}
	cfg, err := vngcloud.NewConfig(append(opts, extra...)...)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func newHarness(t *testing.T, handler func(w http.ResponseWriter, r *http.Request, h *harness), extra ...vngcloud.LoadOption) *harness {
	t.Helper()
	h := &harness{t: t, clock: &fakeClock{now: time.Unix(1000, 0)}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		h.mu.Lock()
		h.requests = append(h.requests, recorded{r.Method, r.URL.Path, r.URL.RawQuery, r.Header.Clone(), string(body)})
		h.mu.Unlock()
		handler(w, r, h)
	}))
	t.Cleanup(server.Close)
	target, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	api := roundTrip(func(r *http.Request) (*http.Response, error) {
		clone := r.Clone(r.Context())
		u := *r.URL
		clone.URL = &u
		clone.URL.Scheme = target.Scheme
		clone.URL.Host = target.Host
		clone.URL.Path = "/" + strings.TrimPrefix(clone.URL.Path, "/vdb/")
		clone.Host = target.Host
		return server.Client().Transport.RoundTrip(clone)
	})
	h.client = New(iamConfig(t, api, extra...))
	h.client.now = h.clock.Now
	h.client.sleep = h.clock.Sleep
	return h
}

const (
	pathVersions = "/vdb-relational/v1/database-instances/datastore"
	pathZones    = "/vdb-relational/v1/database-instances/zones"
	pathFlavors  = "/vdb-relational/v1/database-instances/flavors"
	pathStorage  = "/vdb-relational/v1/database-instances/volume/types"
	pathNetworks = "/vdb-relational/v1/database-instances/networks/subnets"
	pathCreate   = "/vdb-relational/v1/payment/database-instances"
	pathDetail   = "/vdb-relational/v1/database-instances/id/db-1"
	pathBackups  = "/vdb-relational/v1/backups/insId/db-1"
	pathDelete   = "/vdb-relational/v1/database-instances/db-1/delete"
)

func writeJSON(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, body)
}

func catalog(w http.ResponseWriter, r *http.Request) bool {
	switch r.URL.Path {
	case pathVersions:
		writeJSON(w, 200, `{"code":200,"data":[{"name":"MySQL","type":"MySQL","version":"5.7","versionName":"5.7"},{"name":"MySQL","type":"MySQL","version":"8.0","versionName":"8.0"}]}`)
	case pathZones:
		writeJSON(w, 200, `{"code":200,"data":[{"uuid":"zone-1","name":"HCM03-1A","status":"ACTIVE"}]}`)
	case pathFlavors:
		writeJSON(w, 200, `{"code":200,"data":[{"id":211,"name":"small","flavorId":"f-1","vcpus":1,"ram":2,"packageSku":"sku","locateZoneId":"zone-1"}]}`)
	case pathStorage:
		writeJSON(w, 200, `{"code":200,"data":{"projectId":"project-1","data":[{"id":1,"type":"Gen2-NVMe2-IOPS3000","minVolumeSize":20,"maxVolumeSize":1000,"zoneId":"zone-1","volumeTypeZoneId":"vtz-1"}]}}`)
	case pathNetworks:
		writeJSON(w, 200, `{"code":200,"data":[{"uuid":"vpc-1","zoneId":"zone-1","status":"ACTIVE","subnets":[{"uuid":"subnet-1","networkUuid":null,"zoneId":"zone-1"}]}]}`)
	default:
		return false
	}
	return true
}

func detailBody(status string, patch map[string]any) string {
	m := map[string]any{"id": "db-1", "name": "demo-db", "status": status, "datastoreType": "MySQL", "datastoreVersion": "8.0", "quotaPackageId": "211", "publicAccess": false, "backupAuto": false, "subnetId": "subnet-1", "zoneId": "zone-1", "projectId": "project-1", "volumeType": "Gen2-NVMe2-IOPS3000", "volumeSize": 20, "owner": true, "deleted": false}
	for k, v := range patch {
		if v == nil {
			delete(m, k)
		} else {
			m[k] = v
		}
	}
	b, _ := json.Marshal(map[string]any{"code": 200, "data": m})
	return string(b)
}

func validCreateInput() *CreateDatabaseInstanceInput {
	return &CreateDatabaseInstanceInput{Name: "demo-db", ZoneID: "zone-1", FlavorID: 211, StorageTypeID: "Gen2-NVMe2-IOPS3000", StorageSize: 20, VPCID: "vpc-1", SubnetID: "subnet-1", DatabaseName: "appdb", Username: "admin_user", Password: testPassword}
}

const createReply = `{"code":200,"message":"ok","data":[{"resourceId":"db-1","orderId":"order-1","orderUrl":""}]}`

type script struct {
	create        func(w http.ResponseWriter, r *http.Request)
	detail        []string
	detailDefault string
	del           func(w http.ResponseWriter, r *http.Request)
	backups       string
	details       int
}

func (s *script) handler() func(http.ResponseWriter, *http.Request, *harness) {
	return func(w http.ResponseWriter, r *http.Request, h *harness) {
		if catalog(w, r) {
			return
		}
		switch {
		case r.Method == http.MethodPost && r.URL.Path == pathCreate:
			if s.create != nil {
				s.create(w, r)
				return
			}
			writeJSON(w, 200, createReply)
		case r.Method == http.MethodPost && r.URL.Path == pathDelete:
			if s.del != nil {
				s.del(w, r)
				return
			}
			writeJSON(w, 200, `{"code":200,"data":[{"databaseInstances":"db-1","action":"delete","status":"DELETING","success":true,"code":200}]}`)
		case r.URL.Path == pathBackups:
			if s.backups != "" {
				writeJSON(w, 200, s.backups)
				return
			}
			writeJSON(w, 200, `{"code":200,"data":[]}`)
		case r.URL.Path == pathDetail:
			i := s.details
			s.details++
			body := s.detailDefault
			if i < len(s.detail) {
				body = s.detail[i]
			}
			if body == "404" {
				writeJSON(w, 404, `{"code":404,"message":"not found"}`)
				return
			}
			writeJSON(w, 200, body)
		default:
			w.WriteHeader(http.StatusTeapot)
		}
	}
}
