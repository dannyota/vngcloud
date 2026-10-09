package cli

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"

	"danny.vn/vngcloud/storage"
)

const (
	storageRegionsBody = `{"code":200,"success":true,"datas":[` +
		`{"regionId":"region-han","regionName":"HAN02","status":1,"backendType":"ceph"},` +
		`{"regionId":"region-hcm","regionName":"HCM04","status":1,"backendType":"ceph"}]}`
	storageProjectsBody = `{"code":200,"success":true,"datas":[` +
		`{"projectId":"proj-s1","projectName":"main","regionId":"region-hcm","regionName":"HCM04","status":1,"totalQuota":100}]}`
	storageBucketsBody = `{"code":200,"success":true,"isNext":false,"datas":[` +
		`{"name":"bucket-a","count":12,"size":3456,"isPublic":false,"isVersioned":true,"type":"ceph"}]}`
	storageBucketBody = `{"code":200,"success":true,"data":{"name":"bucket-a","count":12,"size":3456,"isVersioned":true,"type":"ceph"}}`
)

func exampleStorageBucket() storage.Bucket {
	return storage.Bucket{
		Name: "bucket-a", ObjectCount: 12, SizeBytes: 3456, IsVersioned: true,
		CreatedDate: "2026-01-01T00:00:00Z", LastModified: "2026-01-02T00:00:00Z", Type: "ceph",
	}
}

func TestGoldenStorageListBuckets(t *testing.T) {
	v := &storage.ListBucketsOutput{Items: []storage.Bucket{exampleStorageBucket()}}
	checkGolden(t, "storage-list-buckets.json.golden", "json", "", v)
	checkGolden(t, "storage-list-buckets.table.golden", "table", "", v)
}

func TestGoldenStorageGetBucket(t *testing.T) {
	v := &storage.GetBucketOutput{Bucket: exampleStorageBucket()}
	checkGolden(t, "storage-get-bucket.json.golden", "json", "", v)
	checkGolden(t, "storage-get-bucket.table.golden", "table", "", v)
}

func TestStorageCommandsMatchDesignTable(t *testing.T) {
	wantFlags := map[string][]string{
		"list-regions":  {},
		"list-projects": {},
		"list-buckets":  {},
		"get-bucket":    {"bucket"},
	}
	if len(storageOps) != len(wantFlags) {
		t.Fatalf("storageOps has %d ops, want %d", len(storageOps), len(wantFlags))
	}
	for _, op := range storageOps {
		want, ok := wantFlags[op.name]
		if !ok {
			t.Errorf("unexpected op %q", op.name)
			continue
		}
		if op.kind != kindRead {
			t.Errorf("%s is not a read", op.name)
		}
		specs, err := flagSpecsFor(op.newInput())
		if err != nil {
			t.Fatal(err)
		}
		got := []string{}
		for _, s := range withoutNoFlag(specs, op.noFlag) {
			got = append(got, s.flagName)
		}
		if !slices.Equal(got, want) {
			t.Errorf("%s flags = %v, want %v", op.name, got, want)
		}
	}
}

type storageRun struct {
	fixture *svcFixture
	stdout  string
	stderr  string
	err     error
}

func runStorage(t *testing.T, routes map[string]func(http.ResponseWriter, *http.Request), args ...string) storageRun {
	t.Helper()
	routes["/internal/v1/regions"] = jsonHandler(http.StatusOK, storageRegionsBody)
	fixture := newSvcFixture(routes)
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs(append([]string{"--region", "hcm-3"}, args...))
	err := root.ExecuteContext(context.Background())
	return storageRun{fixture: fixture, stdout: stdout.String(), stderr: stderr.String(), err: err}
}

func TestStorageListRegions(t *testing.T) {
	r := runStorage(t, map[string]func(http.ResponseWriter, *http.Request){}, "storage", "list-regions")
	if r.err != nil {
		t.Fatalf("execute: %v (%s)", r.err, r.stderr)
	}
	if !strings.Contains(r.stdout, `"Name": "HCM04"`) {
		t.Fatalf("stdout = %s", r.stdout)
	}
}

func TestStorageListProjects(t *testing.T) {
	r := runStorage(t, map[string]func(http.ResponseWriter, *http.Request){
		"/internal/v1/projects": jsonHandler(http.StatusOK, storageProjectsBody),
	}, "storage", "list-projects")
	if r.err != nil {
		t.Fatalf("execute: %v (%s)", r.err, r.stderr)
	}
	if !strings.Contains(r.stdout, `"ID": "proj-s1"`) {
		t.Fatalf("stdout = %s", r.stdout)
	}
}

func TestStorageListBucketsUsesGlobalProjectID(t *testing.T) {
	r := runStorage(t, map[string]func(http.ResponseWriter, *http.Request){
		"/internal/v1/ceph/projects/proj-s1": jsonHandler(http.StatusOK, storageBucketsBody),
	}, "--project-id", "proj-s1", "storage", "list-buckets")
	if r.err != nil {
		t.Fatalf("execute: %v (%s)", r.err, r.stderr)
	}
	if !strings.Contains(r.stdout, `"Name": "bucket-a"`) {
		t.Fatalf("stdout = %s", r.stdout)
	}
}

func TestStorageProjectIDFlagOverridesCLIInputJSON(t *testing.T) {
	r := runStorage(t, map[string]func(http.ResponseWriter, *http.Request){
		"/internal/v1/ceph/projects/proj-b": jsonHandler(http.StatusOK, storageBucketsBody),
	}, "--project-id", "proj-b", "storage", "list-buckets", "--cli-input-json", `{"ProjectID":"proj-a"}`)
	if r.err != nil {
		t.Fatalf("execute: %v (%s)", r.err, r.stderr)
	}
}

func TestStorageRegionComesFromCLIInputJSON(t *testing.T) {
	r := runStorage(t, map[string]func(http.ResponseWriter, *http.Request){
		"/internal/v1/ceph/projects/proj-s1": jsonHandler(http.StatusOK, storageBucketsBody),
	}, "--project-id", "proj-s1", "storage", "list-buckets", "--cli-input-json", `{"Region":"HAN02"}`)
	if r.err != nil {
		t.Fatalf("execute: %v (%s)", r.err, r.stderr)
	}
}

func TestStorageGetBucket(t *testing.T) {
	r := runStorage(t, map[string]func(http.ResponseWriter, *http.Request){
		"/internal/v1/ceph/projects/proj-s1/bucket-a/details": jsonHandler(http.StatusOK, storageBucketBody),
	}, "--project-id", "proj-s1", "storage", "get-bucket", "--bucket", "bucket-a")
	if r.err != nil {
		t.Fatalf("execute: %v (%s)", r.err, r.stderr)
	}
	if !strings.Contains(r.stdout, `"Name": "bucket-a"`) || !strings.Contains(r.stdout, `"IsVersioned": true`) {
		t.Fatalf("stdout = %s", r.stdout)
	}
}

func TestStorageMissingProjectIDStopsBeforeAnyRequest(t *testing.T) {
	for _, args := range [][]string{
		{"storage", "list-buckets"},
		{"storage", "get-bucket", "--bucket", "bucket-a"},
	} {
		r := runStorage(t, map[string]func(http.ResponseWriter, *http.Request){}, args...)
		if r.err == nil || exitCode(r.err) != 2 {
			t.Fatalf("%v: err = %v, exit = %d, want exit 2", args, r.err, exitCode(r.err))
		}
		if !strings.Contains(r.err.Error(), "--project-id") {
			t.Errorf("%v: error %q does not name --project-id", args, r.err)
		}
		if n := r.fixture.requestCount(); n != 0 {
			t.Errorf("%v: requestCount = %d, want 0", args, n)
		}
	}
}

func TestStorageEnvProjectIDDoesNotFillTheStorageProject(t *testing.T) {
	t.Setenv("VNGCLOUD_PROJECT_ID", "pro-vserver")
	r := runStorage(t, map[string]func(http.ResponseWriter, *http.Request){}, "storage", "list-buckets")
	if r.err == nil || exitCode(r.err) != 2 {
		t.Fatalf("err = %v, want exit 2", r.err)
	}
}

func TestStorageGetBucketNotFoundExitsFour(t *testing.T) {
	r := runStorage(t, map[string]func(http.ResponseWriter, *http.Request){
		"/internal/v1/ceph/projects/proj-s1/bucket-a/details": jsonHandler(http.StatusOK, `{"code":404,"success":false,"errorMsg":"no such bucket"}`),
	}, "--project-id", "proj-s1", "storage", "get-bucket", "--bucket", "bucket-a")
	if r.err == nil || exitCode(r.err) != 4 {
		t.Fatalf("err = %v, exit = %d, want 4", r.err, exitCode(r.err))
	}
	if env := classify(r.err); env.Code != "NotFound" {
		t.Fatalf("code = %q", env.Code)
	}
}

func TestStoragePermissionDeniedMapsToItsCode(t *testing.T) {
	r := runStorage(t, map[string]func(http.ResponseWriter, *http.Request){
		"/internal/v1/ceph/projects/proj-s1": jsonHandler(http.StatusForbidden, `[{"code":"IAM_PERMISSION_DENIED","message":"IAM denied action"}]`),
	}, "--project-id", "proj-s1", "storage", "list-buckets")
	if r.err == nil || exitCode(r.err) != 1 {
		t.Fatalf("err = %v, exit = %d, want 1", r.err, exitCode(r.err))
	}
	env := classify(r.err)
	if env.Code != "IAM_PERMISSION_DENIED" || env.Status != http.StatusForbidden {
		t.Fatalf("envelope = %+v", env)
	}
}

func TestStorageGetBucketExampleSelectsNoWrapperKey(t *testing.T) {
	for _, op := range buildDocService("storage", storageOps).ops {
		if op.queryField != "" {
			t.Errorf("%s: queryField = %q, want none (the output is flat)", op.name, op.queryField)
		}
	}
}
