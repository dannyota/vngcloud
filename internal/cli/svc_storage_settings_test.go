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
	storageVersioningPath = storageBucketPath + "/versioning"
	storageCORSPath       = storageBucketPath + "/cors"

	settingsOKBody  = `{"code":200,"success":true}`
	corsOneRule     = `{"Rules":[{"AllowedOrigins":["https://app.example.com"],"AllowedMethods":["GET","PUT"],"AllowedHeaders":["*"],"MaxAgeSeconds":600}]}`
	corsOneRuleBody = `[{"AllowedOrigins":["https://app.example.com"],"AllowedMethods":["GET","PUT"],"AllowedHeaders":["*"],"MaxAgeSeconds":600}]`
)

// settingsRoutes serves path, answers with body, and records each call as
// "METHOD body".
func settingsRoutes(path string, calls *[]string, body string) map[string]func(http.ResponseWriter, *http.Request) {
	var mu sync.Mutex
	return map[string]func(http.ResponseWriter, *http.Request){
		path: func(w http.ResponseWriter, r *http.Request) {
			b, _ := io.ReadAll(r.Body)
			mu.Lock()
			*calls = append(*calls, r.Method+" "+string(b))
			mu.Unlock()
			jsonHandler(http.StatusOK, body)(w, r)
		},
	}
}

func settingsArgs(command string, extra ...string) []string {
	return append([]string{"--project-id", "proj-s1", "storage", command, "--bucket", "bucket-a"}, extra...)
}

func wantOneCall(t *testing.T, calls []string, want string) {
	t.Helper()
	if len(calls) != 1 || calls[0] != want {
		t.Fatalf("calls = %q, want one %q", calls, want)
	}
}

func TestStorageGetBucketVersioningPrintsState(t *testing.T) {
	for status, enabled := range map[string]bool{"Off": false, "Enabled": true, "Suspended": false} {
		t.Run(status, func(t *testing.T) {
			var calls []string
			body := `{"code":200,"success":true,"data":{"versioning":` + map[bool]string{true: "true", false: "false"}[enabled] +
				`,"versioningStatus":"` + status + `"}}`
			r := runStorage(t, settingsRoutes(storageVersioningPath, &calls, body), settingsArgs("get-bucket-versioning")...)
			if r.err != nil {
				t.Fatalf("execute: %v (%s)", r.err, r.stderr)
			}
			var out struct {
				Enabled bool
				Status  string
			}
			if err := json.Unmarshal([]byte(r.stdout), &out); err != nil || out.Enabled != enabled || out.Status != status {
				t.Fatalf("stdout = %s, err = %v, want Enabled %v and Status %s", r.stdout, err, enabled, status)
			}
			wantOneCall(t, calls, "GET ")
		})
	}
}

func TestStoragePutBucketVersioningSendsEnable(t *testing.T) {
	for value, want := range map[string]string{"true": `{"enable":true}`, "false": `{"enable":false}`} {
		t.Run(value, func(t *testing.T) {
			var calls []string
			r := runStorage(t, settingsRoutes(storageVersioningPath, &calls, settingsOKBody),
				settingsArgs("put-bucket-versioning", "--enabled="+value)...)
			if r.err != nil {
				t.Fatalf("execute: %v (%s)", r.err, r.stderr)
			}
			wantOneCall(t, calls, "PUT "+want)
		})
	}
}

func TestStoragePutBucketVersioningNeedsEnabled(t *testing.T) {
	for name, args := range map[string][]string{
		"missing":       settingsArgs("put-bucket-versioning"),
		"separate word": settingsArgs("put-bucket-versioning", "--enabled", "false"),
	} {
		t.Run(name, func(t *testing.T) {
			var calls []string
			r := runStorage(t, settingsRoutes(storageVersioningPath, &calls, settingsOKBody), args...)
			if r.err == nil || exitCode(r.err) != 2 {
				t.Fatalf("err = %v, exit = %d, want exit 2", r.err, exitCode(r.err))
			}
			if n := r.fixture.requestCount(); n != 0 {
				t.Fatalf("requestCount = %d, want 0", n)
			}
		})
	}
}

func TestStorageGetBucketCORSPrintsRules(t *testing.T) {
	body := `{"code":200,"success":true,"data":{"rules":[{"allowedOrigins":["https://a.example.com"],` +
		`"allowedMethods":["GET"],"allowedHeaders":["*"],"maxAgeSeconds":30,"exposedHeaders":["ETag"]}]}}`
	var calls []string
	r := runStorage(t, settingsRoutes(storageCORSPath, &calls, body), settingsArgs("get-bucket-cors")...)
	if r.err != nil {
		t.Fatalf("execute: %v (%s)", r.err, r.stderr)
	}
	var out struct {
		Rules []struct {
			AllowedOrigins []string
			AllowedMethods []string
			MaxAgeSeconds  int
			ExposedHeaders []string
		}
	}
	if err := json.Unmarshal([]byte(r.stdout), &out); err != nil || len(out.Rules) != 1 ||
		out.Rules[0].AllowedOrigins[0] != "https://a.example.com" || out.Rules[0].MaxAgeSeconds != 30 ||
		out.Rules[0].ExposedHeaders[0] != "ETag" {
		t.Fatalf("stdout = %s, err = %v", r.stdout, err)
	}
	wantOneCall(t, calls, "GET ")
}

func TestStorageGetBucketCORSEmptyPrintsEmptyList(t *testing.T) {
	for name, body := range map[string]string{
		"no data":    settingsOKBody,
		"null rules": `{"code":200,"success":true,"data":{"rules":null}}`,
	} {
		t.Run(name, func(t *testing.T) {
			var calls []string
			r := runStorage(t, settingsRoutes(storageCORSPath, &calls, body), settingsArgs("get-bucket-cors")...)
			if r.err != nil {
				t.Fatalf("execute: %v (%s)", r.err, r.stderr)
			}
			var out map[string]any
			if err := json.Unmarshal([]byte(r.stdout), &out); err != nil {
				t.Fatalf("stdout = %s, err = %v", r.stdout, err)
			}
			rules, ok := out["Rules"].([]any)
			if !ok || len(rules) != 0 {
				t.Fatalf("stdout = %s, want {\"Rules\": []}", r.stdout)
			}
		})
	}
}

func TestStoragePutBucketCORSInline(t *testing.T) {
	var calls []string
	r := runStorage(t, settingsRoutes(storageCORSPath, &calls, settingsOKBody),
		settingsArgs("put-bucket-cors", "--cli-input-json", corsOneRule)...)
	if r.err != nil {
		t.Fatalf("execute: %v (%s)", r.err, r.stderr)
	}
	wantOneCall(t, calls, "PUT "+corsOneRuleBody)
}

func TestStoragePutBucketCORSFromFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cors.json")
	if err := os.WriteFile(path, []byte(corsOneRule), 0o600); err != nil {
		t.Fatal(err)
	}
	var calls []string
	r := runStorage(t, settingsRoutes(storageCORSPath, &calls, settingsOKBody),
		settingsArgs("put-bucket-cors", "--cli-input-json", "file://"+path)...)
	if r.err != nil {
		t.Fatalf("execute: %v (%s)", r.err, r.stderr)
	}
	wantOneCall(t, calls, "PUT "+corsOneRuleBody)
}

func TestStoragePutBucketCORSDoesNotSendExposedHeaders(t *testing.T) {
	var calls []string
	input := `{"Rules":[{"AllowedOrigins":["*"],"AllowedMethods":["GET"],"ExposedHeaders":["ETag"]}]}`
	r := runStorage(t, settingsRoutes(storageCORSPath, &calls, settingsOKBody),
		settingsArgs("put-bucket-cors", "--cli-input-json", input)...)
	if r.err != nil {
		t.Fatalf("execute: %v (%s)", r.err, r.stderr)
	}
	wantOneCall(t, calls, `PUT [{"AllowedOrigins":["*"],"AllowedMethods":["GET"]}]`)
}

func TestStoragePutBucketCORSSDKRefusalExitsTwoWithZeroRequests(t *testing.T) {
	for name, input := range map[string]string{
		"bad method":   `{"Rules":[{"AllowedOrigins":["*"],"AllowedMethods":["PATCH"]}]}`,
		"no origin":    `{"Rules":[{"AllowedMethods":["GET"]}]}`,
		"two wildcard": `{"Rules":[{"AllowedOrigins":["*.*.example.com"],"AllowedMethods":["GET"]}]}`,
		"empty rules":  `{"Rules":[]}`,
		"no rules":     `{}`,
	} {
		t.Run(name, func(t *testing.T) {
			var calls []string
			r := runStorage(t, settingsRoutes(storageCORSPath, &calls, settingsOKBody),
				settingsArgs("put-bucket-cors", "--cli-input-json", input)...)
			if r.err == nil || exitCode(r.err) != 2 {
				t.Fatalf("err = %v, exit = %d, want exit 2", r.err, exitCode(r.err))
			}
			if n := r.fixture.requestCount(); n != 0 {
				t.Fatalf("requestCount = %d, want 0", n)
			}
		})
	}
}

func TestStoragePutBucketCORSServerRefusalExitsOne(t *testing.T) {
	var calls []string
	body := `{"code":114,"success":false,"errorMsg":"Cannot save the CORS rules"}`
	r := runStorage(t, settingsRoutes(storageCORSPath, &calls, body),
		settingsArgs("put-bucket-cors", "--cli-input-json", corsOneRule)...)
	if r.err == nil || exitCode(r.err) != 1 {
		t.Fatalf("err = %v, exit = %d, want exit 1", r.err, exitCode(r.err))
	}
	if env := classify(r.err); env.Code != "114" || !strings.Contains(env.Message, "Cannot save the CORS rules") {
		t.Fatalf("code = %q, message = %q, want 114 with the server message", env.Code, env.Message)
	}
}

func TestStorageDeleteBucketCORSSendsDeleteWithoutYes(t *testing.T) {
	var calls []string
	r := runStorage(t, settingsRoutes(storageCORSPath, &calls, settingsOKBody), settingsArgs("delete-bucket-cors")...)
	if r.err != nil {
		t.Fatalf("execute: %v (%s)", r.err, r.stderr)
	}
	wantOneCall(t, calls, "DELETE ")
}

func TestStorageSettingsWritesReadOnlyRefusedWithZeroRequests(t *testing.T) {
	for _, args := range [][]string{
		settingsArgs("put-bucket-versioning", "--enabled=true"),
		settingsArgs("put-bucket-cors", "--cli-input-json", corsOneRule),
		settingsArgs("delete-bucket-cors"),
	} {
		var calls []string
		r := runStorage(t, settingsRoutes(storageCORSPath, &calls, settingsOKBody), append([]string{"--read-only"}, args...)...)
		if r.err == nil || exitCode(r.err) != 2 || classify(r.err).Code != "ReadOnly" {
			t.Fatalf("%v: err = %v, exit = %d, want ReadOnly exit 2", args, r.err, exitCode(r.err))
		}
		if n := r.fixture.requestCount(); n != 0 {
			t.Errorf("%v: requestCount = %d, want 0", args, n)
		}
	}
}

func TestStorageSettingsCommandsMissingProjectIDStopBeforeAnyRequest(t *testing.T) {
	for _, args := range [][]string{
		{"storage", "get-bucket-versioning", "--bucket", "bucket-a"},
		{"storage", "put-bucket-versioning", "--bucket", "bucket-a", "--enabled=true"},
		{"storage", "get-bucket-cors", "--bucket", "bucket-a"},
		{"storage", "put-bucket-cors", "--bucket", "bucket-a", "--cli-input-json", corsOneRule},
		{"storage", "delete-bucket-cors", "--bucket", "bucket-a"},
	} {
		var calls []string
		r := runStorage(t, settingsRoutes(storageCORSPath, &calls, settingsOKBody), args...)
		if r.err == nil || exitCode(r.err) != 2 || !strings.Contains(r.err.Error(), "--project-id") {
			t.Fatalf("%v: err = %v, exit = %d, want exit 2 naming --project-id", args, r.err, exitCode(r.err))
		}
		if n := r.fixture.requestCount(); n != 0 {
			t.Errorf("%v: requestCount = %d, want 0", args, n)
		}
	}
}
