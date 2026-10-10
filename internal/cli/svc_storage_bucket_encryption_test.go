package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/storage"
)

const storageEncryptionPath = storageBucketPath + "/encryption"

func encryptionRoutes(t *testing.T, enabled bool, calls *[]string) map[string]func(http.ResponseWriter, *http.Request) {
	t.Helper()
	return map[string]func(http.ResponseWriter, *http.Request){
		storageEncryptionPath: func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("region") != "region-hcm" || r.Header.Get("region_id") != "region-hcm" {
				t.Error("incorrect region headers")
			}
			if r.Header.Get("Authorization") != "Bearer test-token" {
				t.Error("missing test authorization")
			}
			b, _ := io.ReadAll(r.Body)
			*calls = append(*calls, r.Method+" "+string(b))
			body := settingsOKBody
			if r.Method == http.MethodGet {
				body = `{"code":200,"success":true,"data":{"encryption":` + strconv.FormatBool(enabled) + `}}`
			}
			jsonHandler(http.StatusOK, body)(w, r)
		},
	}
}

func TestStorageBucketEncryptionRequests(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		value := strconv.FormatBool(enabled)
		for _, mode := range []string{"get", "get json", "flag", "json", "override"} {
			t.Run(mode+value, func(t *testing.T) {
				var calls []string
				args := settingsArgs("put-bucket-encryption", "--enabled="+value)
				want := []string{`PUT {"enable":` + value + `}`, "GET "}
				switch mode {
				case "get":
					args = settingsArgs("get-bucket-encryption")
					want = []string{"GET "}
				case "get json":
					args = []string{"storage", "get-bucket-encryption", "--cli-input-json", `{"Region":"HCM04","ProjectID":"proj-s1","BucketName":"bucket-a"}`}
					want = []string{"GET "}
				case "json":
					args = []string{"storage", "put-bucket-encryption", "--cli-input-json", `{"ProjectID":"proj-s1","BucketName":"bucket-a","Enabled":` + value + `}`}
				case "override":
					args = settingsArgs("put-bucket-encryption", "--cli-input-json", `{"BucketName":"other","Enabled":`+strconv.FormatBool(!enabled)+`}`, "--enabled="+value)
				}
				r := runStorage(t, encryptionRoutes(t, enabled, &calls), args...)
				if r.err != nil {
					t.Fatalf("error %v", r.err)
				}
				if !slices.Equal(calls, want) {
					t.Fatalf("calls %q want %q", calls, want)
				}
				if strings.HasPrefix(mode, "get") {
					var out map[string]bool
					if err := json.Unmarshal([]byte(r.stdout), &out); err != nil || len(out) != 1 || out["Enabled"] != enabled {
						t.Fatalf("output %q error %v", r.stdout, err)
					}
				}
			})
		}
	}
}

func TestStorageBucketEncryptionMissingInput(t *testing.T) {
	for name, args := range map[string][]string{
		"omitted":         settingsArgs("put-bucket-encryption"),
		"json omitted":    settingsArgs("put-bucket-encryption", "--cli-input-json", `{}`),
		"json null":       settingsArgs("put-bucket-encryption", "--cli-input-json", `{"Enabled":null}`),
		"separate false":  settingsArgs("put-bucket-encryption", "--enabled", "false"),
		"missing bucket":  {"storage", "get-bucket-encryption", "--project-id", "proj-s1"},
		"missing project": {"storage", "get-bucket-encryption", "--bucket", "bucket-a"},
	} {
		t.Run(name, func(t *testing.T) {
			r := runStorage(t, map[string]func(http.ResponseWriter, *http.Request){}, args...)
			if r.err == nil || exitCode(r.err) != 2 || r.fixture.requestCount() != 0 {
				t.Fatalf("error %v requests %d", r.err, r.fixture.requestCount())
			}
			if strings.Contains(name, "omitted") || name == "json null" {
				if !strings.Contains(r.err.Error(), "--enabled") {
					t.Fatalf("error %v", r.err)
				}
			}
		})
	}
}

func TestStorageCreateBucketEncryptionSequence(t *testing.T) {
	for _, flag := range []string{"", "--encryption=false", "--encryption"} {
		t.Run(flag, func(t *testing.T) {
			var calls []string
			routes := encryptionRoutes(t, true, &calls)
			routes[storageBucketPath] = func(w http.ResponseWriter, r *http.Request) {
				b, _ := io.ReadAll(r.Body)
				calls = append(calls, r.Method+" "+string(b))
				jsonHandler(http.StatusOK, settingsOKBody)(w, r)
			}
			routes[storageDetailsPath] = func(w http.ResponseWriter, r *http.Request) {
				calls = append(calls, r.Method+" details")
				jsonHandler(http.StatusOK, storageEmptyBucket)(w, r)
			}
			args := settingsArgs("create-bucket")
			if flag != "" {
				args = append(args, flag)
			}
			r := runStorage(t, routes, args...)
			want := []string{`POST {"status":"Disabled"}`, "GET details"}
			if flag == "--encryption" {
				want = []string{`POST {"status":"Disabled"}`, `PUT {"enable":true}`, "GET ", "GET details"}
			}
			if r.err != nil || !slices.Equal(calls, want) || !strings.Contains(r.stdout, `"Name": "bucket-a"`) {
				t.Fatalf("error %v calls %q output %q", r.err, calls, r.stdout)
			}
		})
	}
}

func TestStorageBucketEncryptionReadOnlyProfile(t *testing.T) {
	for _, args := range [][]string{settingsArgs("put-bucket-encryption", "--enabled=false"), settingsArgs("create-bucket", "--encryption")} {
		t.Run(args[3], func(t *testing.T) {
			home := withCleanEnv(t)
			writeConfigFile(t, home, "[profile agent]\nregion = hcm-3\nread_only = true\n")
			fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){})
			opts := newFakeServer(t, fixture.mux)
			withTestOptions(t, append(opts, vngcloud.WithStaticToken("test-token"))...)
			root := newRootCmd(strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
			root.SetArgs(append([]string{"--profile", "agent"}, args...))
			err := root.ExecuteContext(context.Background())
			if err == nil || exitCode(err) != 2 || classify(err).Code != "ReadOnly" || fixture.requestCount() != 0 {
				t.Fatalf("error %v requests %d", err, fixture.requestCount())
			}
		})
	}
}

func TestStorageBucketEncryptionIncompletePrecedence(t *testing.T) {
	for _, cause := range []error{vngcloud.ErrPermission, context.Canceled, context.DeadlineExceeded, storage.ErrNotSettled} {
		t.Run(cause.Error(), func(t *testing.T) {
			h := newFakeHarness(t)
			var op Op[storage.Client]
			for _, o := range storageOps {
				if o.name == "create-bucket" {
					op = o
				}
			}
			op.call = func(_ *cobra.Command, _ *storage.Client, _ context.Context, _ any) (any, error) {
				return (*storage.CreateBucketOutput)(nil), fmt.Errorf("%w: %w", storage.ErrBucketEncryptionIncomplete, cause)
			}
			root := newTestRoot(h.e)
			root.AddCommand(Service(h.e, "storage", "test", storage.New, op))
			err := execCmd(t, root, settingsArgs("create-bucket", "--encryption"))
			if !errors.Is(err, cause) || exitCode(err) != 1 || classify(err).Code != "BucketEncryptionIncomplete" || h.stdout.Len() != 0 {
				t.Fatalf("error %v output %q", err, h.stdout.String())
			}
			var stderr bytes.Buffer
			printError(&stderr, err)
			if !strings.Contains(stderr.String(), `"code":"BucketEncryptionIncomplete"`) {
				t.Fatalf("stderr %q", stderr.String())
			}
		})
	}
}

func TestStorageCreateBucketEncryptionRefusedPrintsNoOutput(t *testing.T) {
	var writes []string
	routes := storageWriteRoutes(storageEmptyBucket, &writes)
	routes[storageEncryptionPath] = jsonHandler(http.StatusOK, `{"code":403,"success":false,"errorMsg":"denied"}`)
	r := runStorage(t, routes, settingsArgs("create-bucket", "--encryption")...)
	if r.err == nil || exitCode(r.err) != 1 || classify(r.err).Code != "BucketEncryptionIncomplete" || r.stdout != "" {
		t.Fatalf("error %v output %q", r.err, r.stdout)
	}
}

func TestStorageBucketEncryptionHelp(t *testing.T) {
	for command, phrases := range map[string][]string{
		"get-bucket-encryption": {"encryption", "Enabled", "--bucket"},
		"put-bucket-encryption": {"encryption", "--enabled=true", "--enabled=false", "future uploads", "existing objects", "unverified"},
		"create-bucket":         {"--encryption", "existing bucket", "does not disable"},
	} {
		r := runStorage(t, map[string]func(http.ResponseWriter, *http.Request){}, "storage", command, "--help")
		if r.err != nil || r.fixture.requestCount() != 0 {
			t.Fatalf("error %v", r.err)
		}
		for _, phrase := range phrases {
			if !strings.Contains(r.stdout, phrase) {
				t.Errorf("%s help missing %q", command, phrase)
			}
		}
	}
}
