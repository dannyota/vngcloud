package cli

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

const (
	keysPathS3      = "/internal/v1/users/s3_keys"
	keyPathS3       = keysPathS3 + "/key-1"
	keyFixtureToken = "<secret>"

	s3KeyListBody = `{"code":200,"success":true,"datas":[` +
		`{"regionId":"region-hcm","projectId":"proj-s1","userId":"user-1","subUserId":null,` +
		`"userKeyId":"key-1","accessKey":"access-1","secretKey":"` + keyFixtureToken + `",` +
		`"createdDate":"01/01/2026 00:00","status":1}]}`
	s3KeyCreateBody = `{"code":200,"success":true,"data":{"userId":null,"subUserId":null,"regionId":null,` +
		`"projectId":null,"userKeyId":"key-1","accessKey":"access-1","secretKey":"` + keyFixtureToken + `",` +
		`"createdDate":null,"status":null}}`
	s3KeyCreateNoSecretBody = `{"code":200,"success":true,"data":{"userKeyId":"key-1","accessKey":"access-1","secretKey":null}}`
	s3KeyDeleteBody         = `{"code":200,"success":true,"data":"key-1"}`
	s3KeyRepeatDeleteBody   = `{"code":114,"success":false,"errorMsg":"Could not delete s3 keys. InvalidAccessKeyId"}`

	s3CredentialsFile = "[default]\naws_access_key_id = access-1\naws_secret_access_key = " + keyFixtureToken + "\n"
)

// keyRecorder records each S3 key call as "METHOD body" and fails the test
// when a call lacks the region headers the server needs.
type keyRecorder struct {
	t     *testing.T
	mu    sync.Mutex
	calls []string
}

func (k *keyRecorder) record(r *http.Request, body string) {
	if r.Header.Get("region") != "region-hcm" || r.Header.Get("region_id") != "region-hcm" {
		k.t.Errorf("%s %s: region headers = %q, %q", r.Method, r.URL.Path, r.Header.Get("region"), r.Header.Get("region_id"))
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	k.calls = append(k.calls, r.Method+" "+r.URL.RawQuery+body)
}

func (k *keyRecorder) seen() []string {
	k.mu.Lock()
	defer k.mu.Unlock()
	return append([]string(nil), k.calls...)
}

func (k *keyRecorder) deletes() int {
	n := 0
	for _, c := range k.seen() {
		if strings.HasPrefix(c, "DELETE") {
			n++
		}
	}
	return n
}

// keyRoutes answers the collection path: GET with list, POST with create.
// The key path answers DELETE with del.
func keyRoutes(t *testing.T, list, create, del string) (map[string]func(http.ResponseWriter, *http.Request), *keyRecorder) {
	t.Helper()
	rec := &keyRecorder{t: t}
	body := func(r *http.Request) string {
		b := make([]byte, 512)
		n, _ := r.Body.Read(b)
		return string(b[:n])
	}
	return map[string]func(http.ResponseWriter, *http.Request){
		keysPathS3: func(w http.ResponseWriter, r *http.Request) {
			rec.record(r, body(r))
			if r.Method == http.MethodGet {
				jsonHandler(http.StatusOK, list)(w, r)
				return
			}
			jsonHandler(http.StatusOK, create)(w, r)
		},
		keyPathS3: func(w http.ResponseWriter, r *http.Request) {
			rec.record(r, body(r))
			jsonHandler(http.StatusOK, del)(w, r)
		},
	}, rec
}

func noLeak(t *testing.T, r storageRun) {
	t.Helper()
	if strings.Contains(r.stdout, keyFixtureToken) || strings.Contains(r.stderr, keyFixtureToken) {
		t.Fatalf("the secret leaked: stdout=%s stderr=%s", r.stdout, r.stderr)
	}
	if r.err != nil && strings.Contains(r.err.Error(), keyFixtureToken) {
		t.Fatalf("the secret leaked into the error: %v", r.err)
	}
}

func TestStorageListS3KeysPrintsNoSecret(t *testing.T) {
	routes, rec := keyRoutes(t, s3KeyListBody, "", "")
	r := runStorage(t, routes, "--project-id", "proj-s1", "storage", "list-s3-keys")
	if r.err != nil {
		t.Fatalf("execute: %v (%s)", r.err, r.stderr)
	}
	if !strings.Contains(r.stdout, `"AccessKey": "access-1"`) || !strings.Contains(r.stdout, `"UserKeyID": "key-1"`) {
		t.Fatalf("stdout = %s", r.stdout)
	}
	if strings.Contains(strings.ToLower(r.stdout), "secret") {
		t.Fatalf("stdout holds a secret field: %s", r.stdout)
	}
	noLeak(t, r)
	if got := rec.seen(); len(got) != 1 || got[0] != "GET projectId=proj-s1" {
		t.Fatalf("calls = %q", got)
	}
}

func TestStorageCreateS3KeyWritesCredentialsFile(t *testing.T) {
	routes, rec := keyRoutes(t, "", s3KeyCreateBody, "")
	path := filepath.Join(t.TempDir(), "credentials")
	r := runStorage(t, routes, "--debug", "--project-id", "proj-s1", "storage", "create-s3-key", "--secret-file", path)
	if r.err != nil {
		t.Fatalf("execute: %v (%s)", r.err, r.stderr)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o, want 0600", info.Mode().Perm())
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != s3CredentialsFile {
		t.Fatalf("file = %q, err = %v, want %q", data, err, s3CredentialsFile)
	}
	for _, want := range []string{`"SecretKey": "[redacted]"`, `"SecretFile": "` + path + `"`, `"AccessKey": "access-1"`, `"UserKeyID": "key-1"`} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("stdout = %s, want %s", r.stdout, want)
		}
	}
	noLeak(t, r)
	if want := []string{`POST {"projectId":"proj-s1"}`}; len(rec.seen()) != 1 || rec.seen()[0] != want[0] {
		t.Fatalf("calls = %q, want %q", rec.seen(), want)
	}
}

func TestStorageCreateS3KeyRefusesBadSecretFileBeforeAnyRequest(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "existing")
	if err := os.WriteFile(existing, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(filepath.Join(dir, "target"), link); err != nil {
		t.Skipf("symlink: %v", err)
	}
	cases := map[string][]string{
		"existing":     {"--secret-file", existing},
		"symlink":      {"--secret-file", link},
		"no directory": {"--secret-file", filepath.Join(dir, "missing", "credentials")},
		"not given":    nil,
	}
	for name, extra := range cases {
		t.Run(name, func(t *testing.T) {
			routes, rec := keyRoutes(t, "", s3KeyCreateBody, s3KeyDeleteBody)
			args := append([]string{"--project-id", "proj-s1", "storage", "create-s3-key"}, extra...)
			r := runStorage(t, routes, args...)
			if r.err == nil || exitCode(r.err) != 2 {
				t.Fatalf("err = %v, exit = %d, want exit 2", r.err, exitCode(r.err))
			}
			if n := r.fixture.requestCount(); n != 0 || len(rec.seen()) != 0 {
				t.Fatalf("requestCount = %d, calls = %q, want none", n, rec.seen())
			}
		})
	}
	if data, _ := os.ReadFile(existing); string(data) != "x" {
		t.Fatalf("the existing file was changed: %q", data)
	}
}

func TestStorageCreateS3KeyWriteFailureDeletesTheKey(t *testing.T) {
	routes, rec := keyRoutes(t, "", s3KeyCreateBody, s3KeyDeleteBody)
	path := filepath.Join(unwritableSecretFileDir(t), "credentials")
	r := runStorage(t, routes, "--project-id", "proj-s1", "storage", "create-s3-key", "--secret-file", path)
	if r.err == nil || exitCode(r.err) != 1 || classify(r.err).Code != "SecretFileFailed" {
		t.Fatalf("err = %v, exit = %d, want SecretFileFailed exit 1", r.err, exitCode(r.err))
	}
	if !strings.Contains(r.err.Error(), "key-1") || !strings.Contains(r.err.Error(), "deleted") {
		t.Fatalf("error = %q, want the key id and that it was deleted", r.err)
	}
	if want := `DELETE {"projectId":"proj-s1"}`; rec.deletes() != 1 || rec.seen()[1] != want {
		t.Fatalf("calls = %q", rec.seen())
	}
	if _, err := os.Lstat(path); err == nil {
		t.Fatal("a file was left behind")
	}
	noLeak(t, r)
}

func TestStorageCreateS3KeyWriteFailureNamesTheKeyWhenTheDeleteFails(t *testing.T) {
	routes, rec := keyRoutes(t, "", s3KeyCreateBody, `{"code":500,"success":false,"errorMsg":"boom <secret>"}`)
	path := filepath.Join(unwritableSecretFileDir(t), "credentials")
	r := runStorage(t, routes, "--project-id", "proj-s1", "storage", "create-s3-key", "--secret-file", path)
	if r.err == nil || exitCode(r.err) != 1 || classify(r.err).Code != "SecretFileFailed" {
		t.Fatalf("err = %v, want SecretFileFailed exit 1", r.err)
	}
	if !strings.Contains(r.err.Error(), "key-1") || !strings.Contains(r.err.Error(), "could not be deleted") {
		t.Fatalf("error = %q, want the key id and the failed delete", r.err)
	}
	if rec.deletes() != 1 {
		t.Fatalf("calls = %q", rec.seen())
	}
	noLeak(t, r)
}

func TestStorageCreateS3KeyNoSecretDeletesTheKeyAndWritesNoFile(t *testing.T) {
	routes, rec := keyRoutes(t, "", s3KeyCreateNoSecretBody, s3KeyDeleteBody)
	path := filepath.Join(t.TempDir(), "credentials")
	r := runStorage(t, routes, "--project-id", "proj-s1", "storage", "create-s3-key", "--secret-file", path)
	if r.err == nil || exitCode(r.err) != 1 || classify(r.err).Code != "SecretFileFailed" {
		t.Fatalf("err = %v, exit = %d, want SecretFileFailed exit 1", r.err, exitCode(r.err))
	}
	if !strings.Contains(r.err.Error(), "key-1") {
		t.Fatalf("error = %q, want the key id", r.err)
	}
	if rec.deletes() != 1 {
		t.Fatalf("calls = %q, want one DELETE", rec.seen())
	}
	if _, err := os.Lstat(path); err == nil {
		t.Fatal("a file was written")
	}
	if r.stdout != "" {
		t.Fatalf("stdout = %q, want empty", r.stdout)
	}
}

func TestStorageCreateS3KeyNoSecretNamesTheKeyWhenTheDeleteFails(t *testing.T) {
	routes, rec := keyRoutes(t, "", s3KeyCreateNoSecretBody, `{"code":500,"success":false,"errorMsg":"boom"}`)
	path := filepath.Join(t.TempDir(), "credentials")
	r := runStorage(t, routes, "--project-id", "proj-s1", "storage", "create-s3-key", "--secret-file", path)
	if r.err == nil || exitCode(r.err) != 1 || classify(r.err).Code != "SecretFileFailed" {
		t.Fatalf("err = %v, want SecretFileFailed exit 1", r.err)
	}
	if !strings.Contains(r.err.Error(), "key-1") || !strings.Contains(r.err.Error(), "could not be deleted") {
		t.Fatalf("error = %q, want the key id and the failed delete", r.err)
	}
	if rec.deletes() != 1 {
		t.Fatalf("calls = %q", rec.seen())
	}
}

func TestStorageDeleteS3Key(t *testing.T) {
	routes, rec := keyRoutes(t, "", "", s3KeyDeleteBody)
	r := runStorage(t, routes, "--yes", "--project-id", "proj-s1", "storage", "delete-s3-key", "--user-key-id", "key-1")
	if r.err != nil {
		t.Fatalf("execute: %v (%s)", r.err, r.stderr)
	}
	if want := []string{`DELETE {"projectId":"proj-s1"}`}; len(rec.seen()) != 1 || rec.seen()[0] != want[0] {
		t.Fatalf("calls = %q, want %q", rec.seen(), want)
	}
}

func TestStorageDeleteS3KeyRequiresYes(t *testing.T) {
	routes, rec := keyRoutes(t, "", "", s3KeyDeleteBody)
	r := runStorage(t, routes, "--project-id", "proj-s1", "storage", "delete-s3-key", "--user-key-id", "key-1")
	if r.err == nil || exitCode(r.err) != 2 || !strings.Contains(r.err.Error(), "--yes") {
		t.Fatalf("err = %v, exit = %d, want exit 2 naming --yes", r.err, exitCode(r.err))
	}
	if r.fixture.requestCount() != 0 || len(rec.seen()) != 0 {
		t.Fatalf("calls = %q, want none", rec.seen())
	}
}

func TestStorageDeleteS3KeyRepeatDeleteExitsOneWithCode114(t *testing.T) {
	routes, _ := keyRoutes(t, "", "", s3KeyRepeatDeleteBody)
	r := runStorage(t, routes, "--yes", "--project-id", "proj-s1", "storage", "delete-s3-key", "--user-key-id", "key-1")
	if r.err == nil || exitCode(r.err) != 1 {
		t.Fatalf("err = %v, exit = %d, want exit 1", r.err, exitCode(r.err))
	}
	if env := classify(r.err); env.Code != "114" || !strings.Contains(env.Message, "InvalidAccessKeyId") {
		t.Fatalf("envelope = %+v, want code 114 with the server message", env)
	}
}

func TestStorageS3KeysMissingProjectIDStopBeforeAnyRequest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials")
	for _, args := range [][]string{
		{"storage", "list-s3-keys"},
		{"storage", "create-s3-key", "--secret-file", path},
		{"--yes", "storage", "delete-s3-key", "--user-key-id", "key-1"},
	} {
		routes, rec := keyRoutes(t, s3KeyListBody, s3KeyCreateBody, s3KeyDeleteBody)
		r := runStorage(t, routes, args...)
		if r.err == nil || exitCode(r.err) != 2 || !strings.Contains(r.err.Error(), "--project-id") {
			t.Fatalf("%v: err = %v, exit = %d, want exit 2 naming --project-id", args, r.err, exitCode(r.err))
		}
		if r.fixture.requestCount() != 0 || len(rec.seen()) != 0 {
			t.Errorf("%v: calls = %q, want none", args, rec.seen())
		}
	}
}

func TestStorageS3KeyWritesReadOnlyRefusedWithZeroRequests(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials")
	for name, args := range map[string][]string{
		"create-s3-key": {"create-s3-key", "--secret-file", path},
		"delete-s3-key": {"delete-s3-key", "--user-key-id", "key-1"},
	} {
		t.Run(name, func(t *testing.T) {
			routes, rec := keyRoutes(t, "", s3KeyCreateBody, s3KeyDeleteBody)
			r := runStorage(t, routes, append([]string{"--read-only", "--yes", "--project-id", "proj-s1", "storage"}, args...)...)
			if r.err == nil || exitCode(r.err) != 2 || classify(r.err).Code != "ReadOnly" {
				t.Fatalf("err = %v, exit = %d, want ReadOnly exit 2", r.err, exitCode(r.err))
			}
			if r.fixture.requestCount() != 0 || len(rec.seen()) != 0 {
				t.Fatalf("calls = %q, want none", rec.seen())
			}
		})
	}
	if _, err := os.Lstat(path); err == nil {
		t.Fatal("a file was written")
	}
}

// A cleanup delete answered with code 114 means the key is already gone, so
// the failure is reported as a deleted key, not as a failed delete.
func TestStorageCreateS3KeyCleanupTreatsCode114AsDeleted(t *testing.T) {
	cases := map[string]struct {
		create string
		path   func(*testing.T) string
	}{
		"no secret":     {s3KeyCreateNoSecretBody, func(t *testing.T) string { return filepath.Join(t.TempDir(), "credentials") }},
		"write failure": {s3KeyCreateBody, func(t *testing.T) string { return filepath.Join(unwritableSecretFileDir(t), "credentials") }},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			routes, rec := keyRoutes(t, "", tc.create, s3KeyRepeatDeleteBody)
			r := runStorage(t, routes, "--project-id", "proj-s1", "storage", "create-s3-key", "--secret-file", tc.path(t))
			if r.err == nil || exitCode(r.err) != 1 || classify(r.err).Code != "SecretFileFailed" {
				t.Fatalf("err = %v, exit = %d, want SecretFileFailed exit 1", r.err, exitCode(r.err))
			}
			if strings.Contains(r.err.Error(), "could not be deleted") || !strings.Contains(r.err.Error(), "key-1") {
				t.Fatalf("error = %q, want the key id and no failed-delete wording", r.err)
			}
			if rec.deletes() != 1 {
				t.Fatalf("calls = %q, want one DELETE", rec.seen())
			}
			noLeak(t, r)
		})
	}
}
