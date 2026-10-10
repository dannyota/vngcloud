package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/spf13/cobra"

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
		"list-project-types":   {},
		"quote-create-project": {"type", "quota-gb"},
		"list-regions":         {},
		"list-projects":        {},
		"list-buckets":         {},
		"get-bucket":           {"bucket"},
		"create-bucket":        {"bucket"},
		"delete-bucket":        {"bucket", "no-wait"},
		"list-s3-keys":         {},
		"create-s3-key":        {"secret-file", "service-account-id"},
		"delete-s3-key":        {"user-key-id"},

		"attach-s3-key":                    {"user-key-id", "service-account-id"},
		"detach-s3-key":                    {"user-key-id"},
		"ensure-service-account-principal": {"service-account-id"},
		"get-bucket-policy":                {"bucket"},
		"put-bucket-policy":                {"bucket", "policy"},
		"delete-bucket-policy":             {"bucket"},
		"get-bucket-versioning":            {"bucket"},
		"put-bucket-versioning":            {"bucket", "enabled"},
		"get-bucket-cors":                  {"bucket"},
		"put-bucket-cors":                  {"bucket"},
		"delete-bucket-cors":               {"bucket"},
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
		wantKind := kindRead
		switch op.name {
		case "create-bucket", "delete-bucket", "create-s3-key", "delete-s3-key",
			"attach-s3-key", "detach-s3-key", "ensure-service-account-principal",
			"put-bucket-policy", "delete-bucket-policy", "put-bucket-versioning",
			"put-bucket-cors", "delete-bucket-cors":
			wantKind = kindWrite
		}
		if op.kind != wantKind {
			t.Errorf("%s has the wrong kind", op.name)
		}
		switch op.name {
		case "delete-bucket", "delete-s3-key", "attach-s3-key", "detach-s3-key":
			if !op.destructive {
				t.Errorf("%s must need --yes", op.name)
			}
		default:
			if op.destructive {
				t.Errorf("%s must not need --yes", op.name)
			}
		}
		specs, err := flagSpecsFor(op.newInput())
		if err != nil {
			t.Fatal(err)
		}
		got := []string{}
		for _, s := range withoutNoFlag(specs, op.noFlag) {
			got = append(got, s.flagName)
		}
		for _, f := range extraDocFields(op.extraFlags) {
			got = append(got, f.name)
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
		// get-bucket-cors wraps its rule list, which --query Rules selects.
		if op.queryField != "" && op.name != "get-bucket-cors" {
			t.Errorf("%s: queryField = %q, want none (the output is flat)", op.name, op.queryField)
		}
	}
}

const (
	storageBucketPath  = "/internal/v1/ceph/projects/proj-s1/buckets/bucket-a"
	storageDetailsPath = "/internal/v1/ceph/projects/proj-s1/bucket-a/details"
	storageEmptyBucket = `{"code":200,"success":true,"data":{"name":"bucket-a","count":0,"size":0,"type":"ceph"}}`
)

// storageWriteRoutes serves a bucket path that records the method and body of
// each write and answers the details read with body.
func storageWriteRoutes(details string, writes *[]string) map[string]func(http.ResponseWriter, *http.Request) {
	return map[string]func(http.ResponseWriter, *http.Request){
		storageBucketPath: func(w http.ResponseWriter, r *http.Request) {
			b, _ := io.ReadAll(r.Body)
			*writes = append(*writes, r.Method+" "+string(b))
			jsonHandler(http.StatusOK, `{"code":200,"success":true}`)(w, r)
		},
		storageDetailsPath: jsonHandler(http.StatusOK, details),
	}
}

func TestStorageCreateBucketSendsConsoleBodyAndReadsBack(t *testing.T) {
	var writes []string
	r := runStorage(t, storageWriteRoutes(storageEmptyBucket, &writes),
		"--project-id", "proj-s1", "storage", "create-bucket", "--bucket", "bucket-a")
	if r.err != nil {
		t.Fatalf("execute: %v (%s)", r.err, r.stderr)
	}
	if want := []string{`POST {"status":"Disabled"}`}; !slices.Equal(writes, want) {
		t.Fatalf("writes = %q, want %q", writes, want)
	}
	if !strings.Contains(r.stdout, `"Name": "bucket-a"`) {
		t.Fatalf("stdout = %s", r.stdout)
	}
	if m, _ := r.fixture.methodFor(storageDetailsPath); m != http.MethodGet {
		t.Fatalf("the bucket was not read back: method = %q", m)
	}
}

// storageDeleteRoutes serves a bucket that reads as empty on the pre-delete
// read and then as still present for stillPresent more reads, before it
// answers the code 404 envelope the real server gives a deleted bucket.
func storageDeleteRoutes(stillPresent int, writes *[]string, reads *atomic.Int32) map[string]func(http.ResponseWriter, *http.Request) {
	routes := storageWriteRoutes(storageEmptyBucket, writes)
	routes[storageDetailsPath] = func(w http.ResponseWriter, r *http.Request) {
		if int(reads.Add(1)) <= 1+stillPresent {
			jsonHandler(http.StatusOK, storageEmptyBucket)(w, r)
			return
		}
		jsonHandler(http.StatusOK, `{"code":404,"success":false,"errorMsg":"Bucket not found"}`)(w, r)
	}
	return routes
}

func TestStorageDeleteBucketSendsDeleteAndWaitsForNotFound(t *testing.T) {
	var writes []string
	var reads atomic.Int32
	r := runStorage(t, storageDeleteRoutes(0, &writes, &reads),
		"--project-id", "proj-s1", "--yes", "storage", "delete-bucket", "--bucket", "bucket-a")
	if r.err != nil {
		t.Fatalf("execute: %v (%s)", r.err, r.stderr)
	}
	if want := []string{"DELETE "}; !slices.Equal(writes, want) {
		t.Fatalf("writes = %q, want %q", writes, want)
	}
	if n := reads.Load(); n != 2 {
		t.Fatalf("reads = %d, want 2 (the pre-delete read and one poll)", n)
	}
}

func TestStorageDeleteBucketNoWaitSkipsThePoll(t *testing.T) {
	var writes []string
	var reads atomic.Int32
	r := runStorage(t, storageDeleteRoutes(0, &writes, &reads),
		"--project-id", "proj-s1", "--yes", "storage", "delete-bucket", "--bucket", "bucket-a", "--no-wait")
	if r.err != nil {
		t.Fatalf("execute: %v (%s)", r.err, r.stderr)
	}
	if want := []string{"DELETE "}; !slices.Equal(writes, want) {
		t.Fatalf("writes = %q, want %q", writes, want)
	}
	if n := reads.Load(); n != 1 {
		t.Fatalf("reads = %d, want 1 (the pre-delete read only)", n)
	}
}

func TestStorageDeleteBucketWaitSettlesAfterOneStillPresentRead(t *testing.T) {
	var writes []string
	var reads atomic.Int32
	r := runStorage(t, storageDeleteRoutes(1, &writes, &reads),
		"--project-id", "proj-s1", "--yes", "storage", "delete-bucket", "--bucket", "bucket-a")
	if r.err != nil {
		t.Fatalf("execute: %v (%s)", r.err, r.stderr)
	}
	if want := []string{"DELETE "}; !slices.Equal(writes, want) {
		t.Fatalf("writes = %q, want %q", writes, want)
	}
	if n := reads.Load(); n != 3 {
		t.Fatalf("reads = %d, want 3", n)
	}
}

func TestStorageDeleteBucketRequiresYes(t *testing.T) {
	var writes []string
	r := runStorage(t, storageWriteRoutes(storageEmptyBucket, &writes),
		"--project-id", "proj-s1", "storage", "delete-bucket", "--bucket", "bucket-a")
	if r.err == nil || exitCode(r.err) != 2 || !strings.Contains(r.err.Error(), "--yes") {
		t.Fatalf("err = %v, exit = %d, want exit 2 naming --yes", r.err, exitCode(r.err))
	}
	if n := r.fixture.requestCount(); n != 0 {
		t.Fatalf("requestCount = %d, want 0", n)
	}
}

func TestStorageDeleteBucketRefusesANonEmptyBucket(t *testing.T) {
	var writes []string
	full := `{"code":200,"success":true,"data":{"name":"bucket-a","count":3,"size":10,"type":"ceph"}}`
	r := runStorage(t, storageWriteRoutes(full, &writes),
		"--project-id", "proj-s1", "--yes", "storage", "delete-bucket", "--bucket", "bucket-a")
	if r.err == nil || exitCode(r.err) != 1 {
		t.Fatalf("err = %v, exit = %d, want exit 1", r.err, exitCode(r.err))
	}
	if env := classify(r.err); env.Code != "BucketNotEmpty" {
		t.Fatalf("code = %q, want BucketNotEmpty", env.Code)
	}
	if len(writes) != 0 {
		t.Fatalf("writes = %q, want none", writes)
	}
}

func TestStorageDeleteBucketRefusesANullObjectCount(t *testing.T) {
	var writes []string
	r := runStorage(t, storageWriteRoutes(`{"code":200,"success":true,"data":{"name":"bucket-a","count":null}}`, &writes),
		"--project-id", "proj-s1", "--yes", "storage", "delete-bucket", "--bucket", "bucket-a")
	if r.err == nil || classify(r.err).Code != "BucketNotEmpty" || exitCode(r.err) != 1 {
		t.Fatalf("err = %v, want BucketNotEmpty exit 1", r.err)
	}
	if len(writes) != 0 {
		t.Fatalf("writes = %q, want none", writes)
	}
}

func TestStorageWritesMissingProjectIDStopBeforeAnyRequest(t *testing.T) {
	for _, args := range [][]string{
		{"--yes", "storage", "create-bucket", "--bucket", "bucket-a"},
		{"--yes", "storage", "delete-bucket", "--bucket", "bucket-a"},
	} {
		var writes []string
		r := runStorage(t, storageWriteRoutes(storageEmptyBucket, &writes), args...)
		if r.err == nil || exitCode(r.err) != 2 || !strings.Contains(r.err.Error(), "--project-id") {
			t.Fatalf("%v: err = %v, exit = %d, want exit 2 naming --project-id", args, r.err, exitCode(r.err))
		}
		if n := r.fixture.requestCount(); n != 0 {
			t.Errorf("%v: requestCount = %d, want 0", args, n)
		}
	}
}

func TestStorageWritesReadOnlyRefusedWithZeroRequests(t *testing.T) {
	for _, op := range []string{"create-bucket", "delete-bucket"} {
		t.Run(op, func(t *testing.T) {
			var writes []string
			r := runStorage(t, storageWriteRoutes(storageEmptyBucket, &writes),
				"--read-only", "--yes", "--project-id", "proj-s1", "storage", op, "--bucket", "bucket-a")
			if r.err == nil || exitCode(r.err) != 2 || classify(r.err).Code != "ReadOnly" {
				t.Fatalf("err = %v, exit = %d, want ReadOnly exit 2", r.err, exitCode(r.err))
			}
			if n := r.fixture.requestCount(); n != 0 {
				t.Fatalf("requestCount = %d, want 0", n)
			}
		})
	}
}

func TestStorageCreateBucketServerInputRefusalExitsTwoWithCode112(t *testing.T) {
	routes := map[string]func(http.ResponseWriter, *http.Request){
		storageBucketPath: jsonHandler(http.StatusOK, `{"code":112,"success":false,`+
			`"errorMsg":"Invalid input error (Bucket name must be all lowercase letters, numbers or hyphens)"}`),
	}
	r := runStorage(t, routes, "--project-id", "proj-s1", "storage", "create-bucket", "--bucket", "bucket-a")
	if r.err == nil || exitCode(r.err) != 2 {
		t.Fatalf("err = %v, exit = %d, want exit 2", r.err, exitCode(r.err))
	}
	env := classify(r.err)
	if env.Code != "112" || !strings.Contains(env.Message, "lowercase letters") {
		t.Fatalf("code = %q, message = %q, want 112 with the server message", env.Code, env.Message)
	}
}

func TestStorageBucketNameShapeIsAUsageErrorBeforeTheWrite(t *testing.T) {
	var writes []string
	r := runStorage(t, storageWriteRoutes(storageEmptyBucket, &writes),
		"--project-id", "proj-s1", "storage", "create-bucket", "--bucket", "bad/name")
	if r.err == nil || exitCode(r.err) != 2 {
		t.Fatalf("err = %v, exit = %d, want exit 2", r.err, exitCode(r.err))
	}
	if len(writes) != 0 {
		t.Fatalf("writes = %q, want none", writes)
	}
}

// TestStorageDeleteBucketNotSettledPrintsNoOutput checks the CLI side of the
// SDK's unsettled delete: ErrNotSettled with a nil Output prints nothing on
// stdout, and the NotSettled envelope on stderr exits 1. The SDK's 30 s wait
// is not run; the op's call is replaced with the result it ends with.
func TestStorageDeleteBucketNotSettledPrintsNoOutput(t *testing.T) {
	h := newFakeHarness(t)
	var op Op[storage.Client]
	for _, o := range storageOps {
		if o.name == "delete-bucket" {
			op = o
		}
	}
	op.call = func(_ *cobra.Command, _ *storage.Client, _ context.Context, _ any) (any, error) {
		return (*storage.DeleteBucketOutput)(nil), fmt.Errorf("%w: storage.DeleteBucket: still readable", storage.ErrNotSettled)
	}
	root := newTestRoot(h.e)
	root.AddCommand(Service(h.e, "storage", "test", storage.New, op))

	err := execCmd(t, root, []string{"--project-id", "proj-s1", "--yes", "storage", "delete-bucket", "--bucket", "bucket-a"})
	if err == nil || exitCode(err) != 1 {
		t.Fatalf("err = %v, exit = %d, want exit 1", err, exitCode(err))
	}
	if got := h.stdout.String(); got != "" {
		t.Fatalf("stdout = %q, want empty", got)
	}
	var stderr bytes.Buffer
	printError(&stderr, err)
	var got struct {
		Error errorEnvelope `json:"error"`
	}
	if jerr := json.Unmarshal(stderr.Bytes(), &got); jerr != nil || got.Error.Code != "NotSettled" {
		t.Fatalf("stderr = %q, want a NotSettled envelope", stderr.String())
	}
}
