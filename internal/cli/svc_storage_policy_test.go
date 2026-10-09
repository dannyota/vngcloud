package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

const (
	storagePolicyPath = storageBucketPath + "/policy"

	policyNamed  = `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"AWS":["arn:aws:iam:::user/acct-user:sa-app"]},"Action":["s3:GetObject"],"Resource":["arn:aws:s3:::bucket-a/*"]}]}`
	policyStar   = `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":"*","Action":["s3:GetObject"],"Resource":["arn:aws:s3:::bucket-a/*"]}]}`
	policyAWSAll = `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"AWS":"*"},"Action":["s3:GetObject"],"Resource":["arn:aws:s3:::bucket-a/*"]}]}`
	policyAWSArr = `{"Version":"2012-10-17","Statement":[{"Sid":"a","Effect":"Allow","Principal":{"AWS":["arn:aws:iam:::user/acct-user:sa-app"]},"Action":["s3:ListBucket"],"Resource":["arn:aws:s3:::bucket-a"]},` +
		`{"Sid":"b","Effect":"Allow","Principal":{"AWS":["arn:aws:iam:::user/acct-user:sa-app","*"]},"Action":["s3:GetObject"],"Resource":["arn:aws:s3:::bucket-a/*"]}]}`
	policyOKBody = `{"code":200,"success":true}`
)

// policyRoutes serves the policy path, answers with body, and
// records each call as "METHOD body".
func policyRoutes(calls *[]string, body string) map[string]func(http.ResponseWriter, *http.Request) {
	var mu sync.Mutex
	return map[string]func(http.ResponseWriter, *http.Request){
		storagePolicyPath: func(w http.ResponseWriter, r *http.Request) {
			b, _ := io.ReadAll(r.Body)
			mu.Lock()
			*calls = append(*calls, r.Method+" "+string(b))
			mu.Unlock()
			jsonHandler(http.StatusOK, body)(w, r)
		},
	}
}

func putArgs(extra ...string) []string {
	return append([]string{"--project-id", "proj-s1", "storage", "put-bucket-policy", "--bucket", "bucket-a"}, extra...)
}

func wantPut(t *testing.T, calls []string, policy string) {
	t.Helper()
	want, _ := json.Marshal(map[string]string{"policy": policy})
	if len(calls) != 1 || calls[0] != "PUT "+string(want) {
		t.Fatalf("calls = %q, want one PUT %s", calls, want)
	}
}

func TestStorageGetBucketPolicyPrintsTheString(t *testing.T) {
	data, _ := json.Marshal(policyNamed)
	var calls []string
	r := runStorage(t, policyRoutes(&calls, `{"code":200,"success":true,"data":`+string(data)+`}`),
		"--project-id", "proj-s1", "storage", "get-bucket-policy", "--bucket", "bucket-a")
	if r.err != nil {
		t.Fatalf("execute: %v (%s)", r.err, r.stderr)
	}
	var out struct{ Policy string }
	if err := json.Unmarshal([]byte(r.stdout), &out); err != nil || out.Policy != policyNamed {
		t.Fatalf("stdout = %s, err = %v, want Policy to be the document string", r.stdout, err)
	}
	if len(calls) != 1 || !strings.HasPrefix(calls[0], "GET") {
		t.Fatalf("calls = %q, want one GET", calls)
	}
}

func TestStorageGetBucketPolicyEmptyWhenNone(t *testing.T) {
	var calls []string
	r := runStorage(t, policyRoutes(&calls, `{"code":200,"success":true}`),
		"--project-id", "proj-s1", "storage", "get-bucket-policy", "--bucket", "bucket-a")
	if r.err != nil {
		t.Fatalf("execute: %v (%s)", r.err, r.stderr)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(r.stdout), &out); err != nil || out["Policy"] != "" {
		t.Fatalf("stdout = %s, err = %v, want Policy to be an empty string", r.stdout, err)
	}
}

func TestStoragePutBucketPolicyInline(t *testing.T) {
	var calls []string
	r := runStorage(t, policyRoutes(&calls, policyOKBody), putArgs("--policy", policyNamed)...)
	if r.err != nil {
		t.Fatalf("execute: %v (%s)", r.err, r.stderr)
	}
	wantPut(t, calls, policyNamed)
}

func TestStoragePutBucketPolicyFromFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "policy.json")
	if err := os.WriteFile(path, []byte(policyNamed), 0o600); err != nil {
		t.Fatal(err)
	}
	var calls []string
	r := runStorage(t, policyRoutes(&calls, policyOKBody), putArgs("--policy", "file://"+path)...)
	if r.err != nil {
		t.Fatalf("execute: %v (%s)", r.err, r.stderr)
	}
	wantPut(t, calls, policyNamed)
}

func TestStoragePutBucketPolicyFileProblemsExitTwoWithZeroRequests(t *testing.T) {
	dir := t.TempDir()
	empty := filepath.Join(dir, "empty.json")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for name, ref := range map[string]string{
		"missing":   "file://" + filepath.Join(dir, "absent.json"),
		"empty":     "file://" + empty,
		"directory": "file://" + dir,
	} {
		t.Run(name, func(t *testing.T) {
			var calls []string
			r := runStorage(t, policyRoutes(&calls, policyOKBody), putArgs("--policy", ref)...)
			if r.err == nil || exitCode(r.err) != 2 {
				t.Fatalf("err = %v, exit = %d, want exit 2", r.err, exitCode(r.err))
			}
			if n := r.fixture.requestCount(); n != 0 {
				t.Fatalf("requestCount = %d, want 0", n)
			}
		})
	}
}

func TestStoragePutBucketPolicyPublicPrincipalNeedsYes(t *testing.T) {
	for name, policy := range map[string]string{
		"star":          policyStar,
		"aws star":      policyAWSAll,
		"aws list star": policyAWSArr,
	} {
		t.Run(name, func(t *testing.T) {
			var calls []string
			r := runStorage(t, policyRoutes(&calls, policyOKBody), putArgs("--policy", policy)...)
			if r.err == nil || exitCode(r.err) != 2 || !strings.Contains(r.err.Error(), "--yes") {
				t.Fatalf("err = %v, exit = %d, want exit 2 naming --yes", r.err, exitCode(r.err))
			}
			if n := r.fixture.requestCount(); n != 0 {
				t.Fatalf("requestCount = %d, want 0", n)
			}

			calls = nil
			r = runStorage(t, policyRoutes(&calls, policyOKBody), append([]string{"--yes"}, putArgs("--policy", policy)...)...)
			if r.err != nil {
				t.Fatalf("with --yes: %v (%s)", r.err, r.stderr)
			}
			wantPut(t, calls, policy)
		})
	}
}

func TestStoragePutBucketPolicyDenyOnlyStarNeedsNoYes(t *testing.T) {
	deny := `{"Statement":[{"Effect":"Deny","Principal":"*","Action":["s3:*"],"Resource":["arn:aws:s3:::bucket-a/*"]}]}`
	var calls []string
	r := runStorage(t, policyRoutes(&calls, policyOKBody), putArgs("--policy", deny)...)
	if r.err != nil {
		t.Fatalf("execute: %v (%s)", r.err, r.stderr)
	}
	wantPut(t, calls, deny)
}

func TestStoragePutBucketPolicyPublicFileNeedsYes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "policy.json")
	if err := os.WriteFile(path, []byte(policyStar), 0o600); err != nil {
		t.Fatal(err)
	}
	var calls []string
	r := runStorage(t, policyRoutes(&calls, policyOKBody), putArgs("--policy", "file://"+path)...)
	if r.err == nil || exitCode(r.err) != 2 || !strings.Contains(r.err.Error(), "--yes") {
		t.Fatalf("err = %v, exit = %d, want exit 2 naming --yes", r.err, exitCode(r.err))
	}
	if n := r.fixture.requestCount(); n != 0 {
		t.Fatalf("requestCount = %d, want 0", n)
	}
}

func TestStoragePutBucketPolicyNamedPrincipalsNeedNoYes(t *testing.T) {
	starResource := `{"Statement":[{"Effect":"Allow","Principal":{"AWS":["arn:aws:iam:::user/acct-user:sa-app"]},"Action":"s3:*","Resource":"*"}]}`
	for name, policy := range map[string]string{"named": policyNamed, "star in resource only": starResource} {
		t.Run(name, func(t *testing.T) {
			var calls []string
			r := runStorage(t, policyRoutes(&calls, policyOKBody), putArgs("--policy", policy)...)
			if r.err != nil {
				t.Fatalf("execute: %v (%s)", r.err, r.stderr)
			}
			wantPut(t, calls, policy)
		})
	}
}

func TestStoragePutBucketPolicySDKRefusalExitsTwoWithZeroRequests(t *testing.T) {
	for name, policy := range map[string]string{
		"not json":        "{not json",
		"no statement":    `{"Version":"2012-10-17"}`,
		"empty statement": `{"Statement":[]}`,
		"array":           `[]`,
	} {
		t.Run(name, func(t *testing.T) {
			var calls []string
			r := runStorage(t, policyRoutes(&calls, policyOKBody), putArgs("--policy", policy)...)
			if r.err == nil || exitCode(r.err) != 2 || !strings.Contains(r.err.Error(), "Statement") {
				t.Fatalf("err = %v, exit = %d, want exit 2 with the SDK message", r.err, exitCode(r.err))
			}
			if n := r.fixture.requestCount(); n != 0 {
				t.Fatalf("requestCount = %d, want 0", n)
			}
		})
	}
}

func TestStoragePutBucketPolicyServerRefusalExitsOne(t *testing.T) {
	for code, msg := range map[string]string{"400": "MalformedPolicy", "114": "Cannot save the policy"} {
		t.Run(code, func(t *testing.T) {
			var calls []string
			body := `{"code":` + code + `,"success":false,"errorMsg":"` + msg + `"}`
			r := runStorage(t, policyRoutes(&calls, body), putArgs("--policy", policyNamed)...)
			if r.err == nil || exitCode(r.err) != 1 {
				t.Fatalf("err = %v, exit = %d, want exit 1", r.err, exitCode(r.err))
			}
			if env := classify(r.err); env.Code != code || !strings.Contains(env.Message, msg) {
				t.Fatalf("code = %q, message = %q, want %s with the server message", env.Code, env.Message, code)
			}
		})
	}
}

func TestStorageDeleteBucketPolicySendsDelete(t *testing.T) {
	var calls []string
	r := runStorage(t, policyRoutes(&calls, policyOKBody),
		"--project-id", "proj-s1", "storage", "delete-bucket-policy", "--bucket", "bucket-a")
	if r.err != nil {
		t.Fatalf("execute: %v (%s)", r.err, r.stderr)
	}
	if len(calls) != 1 || calls[0] != "DELETE " {
		t.Fatalf("calls = %q, want one DELETE with no body", calls)
	}
}

func TestStoragePolicyWritesReadOnlyRefusedWithZeroRequests(t *testing.T) {
	for _, args := range [][]string{
		putArgs("--policy", policyNamed),
		putArgs("--policy", policyStar),
		{"--project-id", "proj-s1", "storage", "delete-bucket-policy", "--bucket", "bucket-a"},
	} {
		var calls []string
		r := runStorage(t, policyRoutes(&calls, policyOKBody), append([]string{"--read-only", "--yes"}, args...)...)
		if r.err == nil || exitCode(r.err) != 2 || classify(r.err).Code != "ReadOnly" {
			t.Fatalf("%v: err = %v, exit = %d, want ReadOnly exit 2", args, r.err, exitCode(r.err))
		}
		if n := r.fixture.requestCount(); n != 0 {
			t.Errorf("%v: requestCount = %d, want 0", args, n)
		}
	}
}

func TestStoragePolicyCommandsMissingProjectIDStopBeforeAnyRequest(t *testing.T) {
	for _, args := range [][]string{
		{"storage", "get-bucket-policy", "--bucket", "bucket-a"},
		{"storage", "put-bucket-policy", "--bucket", "bucket-a", "--policy", policyNamed},
		{"storage", "delete-bucket-policy", "--bucket", "bucket-a"},
	} {
		var calls []string
		r := runStorage(t, policyRoutes(&calls, policyOKBody), args...)
		if r.err == nil || exitCode(r.err) != 2 || !strings.Contains(r.err.Error(), "--project-id") {
			t.Fatalf("%v: err = %v, exit = %d, want exit 2 naming --project-id", args, r.err, exitCode(r.err))
		}
		if n := r.fixture.requestCount(); n != 0 {
			t.Errorf("%v: requestCount = %d, want 0", args, n)
		}
	}
}
