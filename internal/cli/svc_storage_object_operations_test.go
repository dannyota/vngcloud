package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/storage"
)

type objectOperationsStub struct {
	list  func(context.Context, *storage.ListObjectsInput) (*storage.ListObjectsOutput, error)
	head  func(context.Context, *storage.HeadObjectInput) (*storage.HeadObjectOutput, error)
	calls int
}

func (s *objectOperationsStub) ListObjects(ctx context.Context, in *storage.ListObjectsInput) (*storage.ListObjectsOutput, error) {
	s.calls++
	if s.list != nil {
		return s.list(ctx, in)
	}
	return &storage.ListObjectsOutput{Items: []storage.ObjectSummary{}, CommonPrefixes: []string{}, MaxKeys: in.MaxKeys}, nil
}
func (s *objectOperationsStub) HeadObject(ctx context.Context, in *storage.HeadObjectInput) (*storage.HeadObjectOutput, error) {
	s.calls++
	if s.head != nil {
		return s.head(ctx, in)
	}
	return &storage.HeadObjectOutput{ContentLength: -1, Metadata: map[string]string{}}, nil
}

func objectOperationsFixture(t *testing.T, operation string, s *objectOperationsStub, stdout io.Writer, flags *globalFlags, check func(storage.ObjectConfig)) (*cobra.Command, []string, *bytes.Buffer) {
	t.Helper()
	if !objectFilesSupported() {
		t.Skip("object credential files require Linux or macOS")
	}
	home := withCleanEnv(t)
	credentials := filepath.Join(home, "s3.credentials")
	if err := os.WriteFile(credentials, []byte("[default]\naws_access_key_id=synthetic-access\naws_secret_access_key=synthetic-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	stderr := new(bytes.Buffer)
	e := &env{flags: flags, stdout: stdout, stderr: stderr}
	factory := func(cfg storage.ObjectConfig) (objectOperationsClient, error) {
		if check != nil {
			check(cfg)
		}
		return s, nil
	}
	root := &cobra.Command{Use: "test", SilenceUsage: true, SilenceErrors: true}
	root.SetFlagErrorFunc(flagErrorFunc)
	root.PersistentFlags().StringVar(&flags.region, "region", "", "region")
	root.PersistentFlags().StringVar(&flags.projectID, "project-id", "", "project")
	cmd := newStorageListObjectsCmdWithClient(e, factory)
	if operation == "head-object" {
		cmd = newStorageHeadObjectCmdWithClient(e, factory)
	}
	root.AddCommand(cmd)
	args := []string{operation, "--s3-region", "HCM04", "--s3-credentials-file", credentials, "--bucket", "bucket-1"}
	if operation == "head-object" {
		args = append(args, "--key", "/a//../雪%2F+?#\x00")
	}
	return root, args, stderr
}

func TestStorageObjectOperationsPreflight(t *testing.T) {
	for _, op := range []string{"list-objects", "head-object"} {
		cases := []struct {
			name   string
			extra  []string
			remove string
			flags  globalFlags
		}{
			{name: "region case", extra: []string{"--s3-region", "hcm04"}},
			{name: "missing region", remove: "--s3-region"},
			{name: "missing credentials", remove: "--s3-credentials-file"},
			{name: "missing bucket", remove: "--bucket"},
			{name: "credentials stdin", extra: []string{"--s3-credentials-file", "-"}},
			{name: "credentials url", extra: []string{"--s3-credentials-file", "file://bad"}},
			{name: "bucket", extra: []string{"--bucket", "../bad"}},
			{name: "timeout small", extra: []string{"--transfer-timeout", "999ms"}},
			{name: "timeout large", extra: []string{"--transfer-timeout", "1h1s"}},
			{name: "global region", extra: []string{"--region="}},
			{name: "global project", extra: []string{"--project-id", "p"}},
			{name: "json input", extra: []string{"--cli-input-json", "{}"}},
			{name: "body", extra: []string{"--body", "never-open"}},
			{name: "destination", extra: []string{"--destination", "never-open"}},
			{name: "replace", extra: []string{"--replace"}},
			{name: "all", extra: []string{"--all"}},
			{name: "positional", extra: []string{"extra"}},
			{name: "query", flags: globalFlags{query: "["}},
			{name: "format", flags: globalFlags{output: "bad"}},
			{name: "profile name", flags: globalFlags{profile: "bad/name"}},
		}
		if op == "list-objects" {
			for _, pair := range [][]string{{"--max-keys", "0"}, {"--max-keys", "-1"}, {"--max-keys", "1001"}, {"--prefix", string([]byte{255})}, {"--prefix", strings.Repeat("x", 1025)}, {"--delimiter", ":"}, {"--continuation-token", "bad\n"}, {"--continuation-token", strings.Repeat("x", 8193)}, {"--continuation-token", string([]byte{255})}, {"--key", "key"}} {
				cases = append(cases, struct {
					name   string
					extra  []string
					remove string
					flags  globalFlags
				}{name: pair[0] + pair[1], extra: pair})
			}
		} else {
			cases = append(cases, struct {
				name   string
				extra  []string
				remove string
				flags  globalFlags
			}{name: "missing key", remove: "--key"})
			for _, key := range []string{"", string([]byte{255}), strings.Repeat("x", 1025)} {
				cases = append(cases, struct {
					name   string
					extra  []string
					remove string
					flags  globalFlags
				}{name: "bad key" + key, extra: []string{"--key", key}})
			}
		}
		for _, tc := range cases {
			t.Run(op+"/"+tc.name, func(t *testing.T) {
				s := new(objectOperationsStub)
				made := 0
				root, args, _ := objectOperationsFixture(t, op, s, io.Discard, &tc.flags, func(storage.ObjectConfig) { made++ })
				for i, a := range args {
					if a == tc.remove {
						args = append(args[:i], args[i+2:]...)
						break
					}
				}
				// Invalid flags must win over missing credentials.
				for i, a := range args {
					if a == "--s3-credentials-file" {
						args[i+1] = "never-open"
					}
				}
				err := executeObjectCommand(root, append(args, tc.extra...))
				if err == nil || exitCode(err) != 2 || s.calls != 0 || made != 0 {
					t.Fatalf("err=%v exit=%d calls=%d constructed=%d", err, exitCode(err), s.calls, made)
				}
			})
		}
	}
}

func TestStorageObjectOperationsReadOnly(t *testing.T) {
	for _, op := range []string{"list-objects", "head-object"} {
		for _, mode := range []string{"flag", "env", "profile", "bad env", "bad profile", "bad output", "malformed profile", "profile output", "flag output wins"} {
			t.Run(op+"/"+mode, func(t *testing.T) {
				flags := new(globalFlags)
				s := new(objectOperationsStub)
				out := new(bytes.Buffer)
				made := 0
				root, args, _ := objectOperationsFixture(t, op, s, out, flags, func(storage.ObjectConfig) { made++ })
				switch mode {
				case "flag":
					flags.readOnly = true
				case "env":
					t.Setenv(envReadOnly, "true")
				case "bad env":
					t.Setenv(envReadOnly, "bad")
				case "profile":
					writeConfigFile(t, os.Getenv("HOME"), "[default]\nread_only=true\n")
				case "bad profile":
					writeConfigFile(t, os.Getenv("HOME"), "[default]\nread_only=bad\n")
				case "bad output":
					writeConfigFile(t, os.Getenv("HOME"), "[default]\noutput=bad\n")
				case "malformed profile":
					writeConfigFile(t, os.Getenv("HOME"), "[default\n")
				case "profile output":
					writeConfigFile(t, os.Getenv("HOME"), "[default]\noutput=text\n")
				case "flag output wins":
					flags.output = "json"
					writeConfigFile(t, os.Getenv("HOME"), "[default]\noutput=bad\n")
				}
				bad := strings.HasPrefix(mode, "bad") || mode == "malformed profile"
				if bad {
					for i, a := range args {
						if a == "--s3-credentials-file" {
							args[i+1] = "never-open"
						}
					}
				}
				err := executeObjectCommand(root, args)
				if bad {
					if exitCode(err) != 2 || made != 0 || s.calls != 0 {
						t.Fatalf("err=%v constructed=%d calls=%d", err, made, s.calls)
					}
				} else if err != nil || made != 1 || s.calls != 1 {
					t.Fatalf("err=%v constructed=%d calls=%d", err, made, s.calls)
				}
				if mode == "profile output" && strings.HasPrefix(out.String(), "{") {
					t.Fatal("profile output ignored")
				}
			})
		}
	}
}

func TestStorageObjectOperationsIsolation(t *testing.T) {
	for _, op := range []string{"list-objects", "head-object"} {
		t.Run(op, func(t *testing.T) {
			flags := &globalFlags{region: "invalid-default", projectID: "ignored"}
			s := new(objectOperationsStub)
			root, args, _ := objectOperationsFixture(t, op, s, io.Discard, flags, func(cfg storage.ObjectConfig) {
				if cfg.Region != "HAN02" || cfg.Credentials.AccessKeyID.Reveal() != "synthetic-access" || cfg.Credentials.SecretAccessKey.Reveal() != "synthetic-secret" {
					t.Fatal("ambient configuration reached S3 client")
				}
			})
			home := filepath.Dir(args[4])
			writeConfigFile(t, home, "[default]\nregion=invalid\nproject_id=ignored\nauth_type=invalid\nclient_secret=ignored\n")
			writeCredentialsFile(t, home, "invalid management credentials")
			cache := filepath.Join(home, ".vngcloud", "cache")
			if err := os.WriteFile(cache, []byte("not a directory"), 0o600); err != nil {
				t.Fatal(err)
			}
			t.Setenv(envCredsFileVar, home)
			t.Setenv("VNGCLOUD_REGION", "invalid")
			t.Setenv("VNGCLOUD_PROJECT_ID", "ignored")
			t.Setenv("AWS_ACCESS_KEY_ID", "ambient-access")
			t.Setenv("AWS_SECRET_ACCESS_KEY", "ambient-secret")
			t.Setenv("AWS_SHARED_CREDENTIALS_FILE", "never-open")
			t.Setenv("PATH", t.TempDir())
			if err := executeObjectCommand(root, append(args, "--s3-region", "HAN02")); err != nil || s.calls != 1 {
				t.Fatalf("err=%v calls=%d", err, s.calls)
			}
			if data, err := os.ReadFile(cache); err != nil || string(data) != "not a directory" {
				t.Fatal("management cache changed")
			}
			// The real tree refuses bad S3 flags before any management login or request.
			real := newRootCmd(strings.NewReader(""), io.Discard, io.Discard)
			real.SetArgs([]string{"storage", op, "--s3-region", "invalid"})
			if err := real.Execute(); exitCode(err) != 2 {
				t.Fatalf("real command preflight=%v", err)
			}
		})
	}
}

func TestStorageObjectOperationsDispatch(t *testing.T) {
	for _, op := range []string{"list-objects", "head-object"} {
		t.Run(op, func(t *testing.T) {
			s := new(objectOperationsStub)
			root, args, _ := objectOperationsFixture(t, op, s, io.Discard, new(globalFlags), func(cfg storage.ObjectConfig) {
				if cfg.Timeout != 2*time.Second {
					t.Fatal("timeout not forwarded")
				}
			})
			check := func(ctx context.Context) {
				t.Helper()
				d, ok := ctx.Deadline()
				if !ok || time.Until(d) > 2*time.Second {
					t.Fatal("missing bounded context")
				}
			}
			s.list = func(ctx context.Context, in *storage.ListObjectsInput) (*storage.ListObjectsOutput, error) {
				check(ctx)
				want := &storage.ListObjectsInput{Bucket: "bucket-1", Prefix: "雪 /%2F+", Delimiter: "/", ContinuationToken: " +/%2F=&?# ", MaxKeys: 7}
				if !reflect.DeepEqual(in, want) {
					t.Fatalf("input=%+v", in)
				}
				return &storage.ListObjectsOutput{Items: []storage.ObjectSummary{}, CommonPrefixes: []string{}, MaxKeys: 7, IsTruncated: true, NextContinuationToken: "next+/%2F"}, nil
			}
			s.head = func(ctx context.Context, in *storage.HeadObjectInput) (*storage.HeadObjectOutput, error) {
				check(ctx)
				if in.Bucket != "bucket-1" || in.Key != "/a//../雪%2F+?#\x00" {
					t.Fatal("key bytes changed")
				}
				return &storage.HeadObjectOutput{ContentLength: 0, Metadata: map[string]string{}}, nil
			}
			args = append(args, "--transfer-timeout", "2s")
			if op == "list-objects" {
				args = append(args, "--prefix", "雪 /%2F+", "--delimiter", "/", "--continuation-token", " +/%2F=&?# ", "--max-keys", "7")
			}
			if err := executeObjectCommand(root, args); err != nil || s.calls != 1 {
				t.Fatalf("err=%v calls=%d", err, s.calls)
			}
		})
	}
}

func TestStorageObjectOperationsOutput(t *testing.T) {
	for _, op := range []string{"list-objects", "head-object"} {
		queries := []string{"", "LastModified", "Key", "Metadata"}
		if op == "list-objects" {
			queries = []string{"", "Items[0].Key", "Items[0].LastModified", "NextContinuationToken", "CommonPrefixes"}
		}
		for _, format := range []string{"json", "table", "text"} {
			for _, query := range queries {
				t.Run(op+"/"+format+"/"+query, func(t *testing.T) {
					s := new(objectOperationsStub)
					stdout := new(bytes.Buffer)
					flags := &globalFlags{output: format, query: query, debug: true}
					root, args, stderr := objectOperationsFixture(t, op, s, stdout, flags, nil)
					stamp := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)
					s.list = func(context.Context, *storage.ListObjectsInput) (*storage.ListObjectsOutput, error) {
						return &storage.ListObjectsOutput{Items: []storage.ObjectSummary{{Key: "雪\x1b\n%2F", Size: 0, LastModified: &stamp}, {Key: "nil-time", Size: 1}}, CommonPrefixes: []string{"雪\x1b/"}, MaxKeys: 1000, IsTruncated: true, NextContinuationToken: "next+/%2F"}, nil
					}
					s.head = func(_ context.Context, in *storage.HeadObjectInput) (*storage.HeadObjectOutput, error) {
						if in.Bucket != "synthetic-access" || in.Key != "synthetic-secret/雪\x1b\n" {
							t.Fatal("echo redaction changed request")
						}
						return &storage.HeadObjectOutput{ContentLength: -1, ETag: "opaque", ContentType: "text/plain", LastModified: &stamp, Metadata: map[string]string{"safe": "value"}, VersionID: "observed", ServerSideEncryption: "AES256"}, nil
					}
					if op == "head-object" {
						args = append(args, "--bucket", "synthetic-access", "--key", "synthetic-secret/雪\x1b\n")
					}
					if err := executeObjectCommand(root, args); err != nil {
						t.Fatal(err)
					}
					for _, stream := range []string{stdout.String(), stderr.String()} {
						for _, word := range []string{"synthetic-access", "synthetic-secret", "\x1b", "Authorization", "httpClient", "SecretAccessKey"} {
							if strings.Contains(stream, word) {
								t.Fatalf("leaked %q in %q", word, stream)
							}
						}
					}
					for _, word := range []string{"bucket-1", "雪", "next+/%2F", "synthetic", "HOME"} {
						if strings.Contains(stderr.String(), word) {
							t.Fatalf("unsafe debug=%s", stderr)
						}
					}
					wantCount, wantOp := "count=1", "storage.HeadObject"
					if op == "list-objects" {
						wantCount, wantOp = "count=3", "storage.ListObjects"
					}
					if !strings.Contains(stderr.String(), "status=200") || !strings.Contains(stderr.String(), wantCount) || !strings.Contains(stderr.String(), "operation="+wantOp) || len(strings.Fields(stderr.String())) != 5 || s.calls != 1 {
						t.Fatalf("debug=%s calls=%d", stderr, s.calls)
					}
					if format == "json" && query == "" {
						var data map[string]any
						if err := json.Unmarshal(stdout.Bytes(), &data); err != nil {
							t.Fatal(err)
						}
						if op == "list-objects" {
							for _, name := range []string{"Items", "CommonPrefixes", "MaxKeys", "IsTruncated", "NextContinuationToken"} {
								if _, ok := data[name]; !ok {
									t.Errorf("missing %s", name)
								}
							}
							if _, ok := data["Total"]; ok {
								t.Fatal("invented total")
							}
							items := data["Items"].([]any)
							if items[0].(map[string]any)["LastModified"] != "2024-01-02T03:04:05Z" || items[1].(map[string]any)["LastModified"] != nil {
								t.Fatal("accepted time/null changed")
							}
						} else if !strings.Contains(stdout.String(), "[redacted]") || data["ContentLength"] != float64(-1) || data["LastModified"] != "2024-01-02T03:04:05Z" {
							t.Fatalf("head=%s", stdout)
						}
					}
				})
			}
		}
	}
}

func TestStorageObjectOperationsErrors(t *testing.T) {
	cases := []struct {
		name   string
		status int
		cause  error
		exit   int
	}{
		{"401", 401, vngcloud.ErrNoCredentials, 3}, {"403", 403, nil, 1}, {"404", 404, vngcloud.ErrNotFound, 4}, {"500", 500, nil, 1}, {"invalid", 0, vngcloud.ErrInvalidInput, 2}, {"credentials", 0, vngcloud.ErrCredentialsFile, 3}, {"cancel", 0, context.Canceled, 1}, {"deadline", 0, context.DeadlineExceeded, 1},
		{"401 canceled", 401, errors.Join(vngcloud.ErrNoCredentials, context.Canceled), 1},
		{"404 deadline", 404, errors.Join(vngcloud.ErrNotFound, context.DeadlineExceeded), 1},
	}
	for _, op := range []string{"list-objects", "head-object"} {
		for _, tc := range cases {
			t.Run(op+"/"+tc.name, func(t *testing.T) {
				stdout := new(bytes.Buffer)
				s := new(objectOperationsStub)
				root, args, stderr := objectOperationsFixture(t, op, s, stdout, &globalFlags{debug: true}, nil)
				api := &vngcloud.APIError{Operation: "storage.HeadObject", StatusCode: tc.status, Code: "RequestFailed", Message: "safe refusal", Err: tc.cause}
				s.list = func(context.Context, *storage.ListObjectsInput) (*storage.ListObjectsOutput, error) { return nil, api }
				s.head = func(context.Context, *storage.HeadObjectInput) (*storage.HeadObjectOutput, error) { return nil, api }
				err := executeObjectCommand(root, args)
				var got *vngcloud.APIError
				if exitCode(err) != tc.exit || s.calls != 1 || stdout.Len() != 0 || !errors.As(err, &got) || got != api || (tc.cause != nil && !errors.Is(err, tc.cause)) {
					t.Fatalf("err=%v exit=%d calls=%d", err, exitCode(err), s.calls)
				}
				if strings.Contains(stderr.String(), "bucket-1") {
					t.Fatal("debug leaked identity")
				}
			})
		}
	}
	for _, op := range []string{"list-objects", "head-object"} {
		for _, mode := range []string{"query", "stdout", "credentials", "canceled before dispatch"} {
			t.Run(op+"/"+mode, func(t *testing.T) {
				flags := new(globalFlags)
				writer := io.Discard
				if mode == "query" {
					flags.query = "length(ContentLength)"
					if op == "list-objects" {
						flags.query = "length(MaxKeys)"
					}
				}
				if mode == "stdout" {
					writer = failingObjectWriter{}
				}
				s := new(objectOperationsStub)
				root, args, _ := objectOperationsFixture(t, op, s, writer, flags, nil)
				if mode == "credentials" {
					for i, a := range args {
						if a == "--s3-credentials-file" {
							if err := os.WriteFile(args[i+1], []byte("[default]\naws_secret_access_key=synthetic-secret\n"), 0o600); err != nil {
								t.Fatal(err)
							}
						}
					}
				}
				if mode == "canceled before dispatch" {
					ctx, cancel := context.WithCancel(context.Background())
					cancel()
					root.SetContext(ctx)
				}
				err := executeObjectCommand(root, args)
				wantCalls, wantExit := 1, 1
				if mode == "credentials" {
					wantCalls, wantExit = 0, 3
				}
				if mode == "canceled before dispatch" {
					wantCalls = 0
					if !errors.Is(err, context.Canceled) {
						t.Fatalf("lost context=%v", err)
					}
				}
				if err == nil || exitCode(err) != wantExit || s.calls != wantCalls || strings.Contains(err.Error(), "synthetic-secret") {
					t.Fatalf("err=%v exit=%d calls=%d", err, exitCode(err), s.calls)
				}
				if wantCalls == 1 && !strings.Contains(err.Error(), "completed") {
					t.Fatalf("lost completion=%v", err)
				}
			})
		}
	}
}

func TestStorageObjectOperationsDocs(t *testing.T) {
	root := newRootCmd(strings.NewReader(""), io.Discard, io.Discard)
	for _, op := range storageObjectOperationDocOps() {
		cmd, _, err := root.Find([]string{"storage", op.name})
		if err != nil || cmd.Name() != op.name {
			t.Fatalf("registration %s: %v", op.name, err)
		}
		for _, field := range op.fields {
			if field.viaJSON || cmd.Flags().Lookup(field.name) == nil {
				t.Errorf("reference disagrees with flag %s", field.name)
			}
		}
		for _, name := range []string{"all", "cli-input-json", "body", "destination", "replace", "version-id"} {
			if cmd.Flags().Lookup(name) != nil {
				t.Errorf("unexpected flag %s", name)
			}
		}
		if cmd.Long != storageObjectOperationNotes(op.name) {
			t.Fatal("help differs from reference")
		}
	}
	for _, name := range []string{"delete-object", "copy-object", "move-object", "rename-object"} {
		cmd, _, _ := root.Find([]string{"storage", name})
		if cmd != nil && cmd.Name() == name {
			t.Fatalf("registered %s", name)
		}
	}
}

func TestStorageObjectOperationsUnsupported(t *testing.T) {
	if objectFilesSupported() {
		t.Skip("native platform supports credential files")
	}
	withCleanEnv(t)
	for _, op := range []string{"list-objects", "head-object"} {
		e := &env{flags: new(globalFlags), stdout: io.Discard, stderr: io.Discard}
		factory := func(storage.ObjectConfig) (objectOperationsClient, error) {
			t.Fatal("constructed client on unsupported platform")
			return nil, errors.New("unexpected")
		}
		cmd := newStorageListObjectsCmdWithClient(e, factory)
		args := []string{"--s3-region", "HCM04", "--s3-credentials-file", "never-open", "--bucket", "bucket-1"}
		if op == "head-object" {
			cmd = newStorageHeadObjectCmdWithClient(e, factory)
			args = append(args, "--key", "key")
		}
		cmd.SetArgs(args)
		if err := cmd.Execute(); exitCode(err) != 2 || !strings.Contains(err.Error(), "Linux or macOS") {
			t.Fatalf("error=%v", err)
		}
	}
}

func TestStorageObjectOperationsNilFlags(t *testing.T) {
	f := &storageObjectOperationFlags{region: "HCM04", credentialsFile: "never-open", bucket: "bucket-1", key: "key", timeout: time.Minute, maxKeys: 1000}
	e := &env{flags: new(globalFlags)}
	cmd := &cobra.Command{}
	for _, tc := range []struct {
		name      string
		cmd       *cobra.Command
		env       *env
		flags     *storageObjectOperationFlags
		operation string
	}{
		{"nil command", nil, e, f, "list-objects"},
		{"nil environment", cmd, nil, f, "list-objects"},
		{"nil global flags", cmd, &env{}, f, "list-objects"},
		{"nil operation flags", cmd, e, nil, "list-objects"},
		{"unknown operation", cmd, e, f, "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			factory := func(storage.ObjectConfig) (objectOperationsClient, error) {
				t.Fatal("constructed client before validation")
				return nil, errors.New("unexpected")
			}
			if err := runStorageObjectOperation(tc.cmd, tc.env, tc.flags, tc.operation, factory); exitCode(err) != 2 {
				t.Fatalf("err=%v", err)
			}
		})
	}
	if err := runStorageObjectOperation(cmd, e, f, "list-objects", nil); exitCode(err) != 2 {
		t.Fatalf("nil factory=%v", err)
	}
}
