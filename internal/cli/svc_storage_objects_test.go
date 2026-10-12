package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/storage"
)

type objectCommandStub struct {
	put   func(context.Context, *storage.PutObjectInput) (*storage.PutObjectOutput, error)
	get   func(context.Context, *storage.GetObjectInput) (*storage.GetObjectOutput, error)
	calls int
}

func (s *objectCommandStub) PutObject(ctx context.Context, in *storage.PutObjectInput) (*storage.PutObjectOutput, error) {
	s.calls++
	return s.put(ctx, in)
}
func (s *objectCommandStub) GetObject(ctx context.Context, in *storage.GetObjectInput) (*storage.GetObjectOutput, error) {
	s.calls++
	return s.get(ctx, in)
}

func objectCommandFixture(t *testing.T, put bool, stub *objectCommandStub, output io.Writer, flags *globalFlags, config func(storage.ObjectConfig)) (*cobra.Command, []string, *bytes.Buffer) {
	t.Helper()
	if !objectFilesSupported() {
		t.Skip("object file runtime requires Linux or macOS")
	}
	home := withCleanEnv(t)
	creds := filepath.Join(home, "s3.credentials")
	body := filepath.Join(home, "source")
	for path, data := range map[string][]byte{creds: []byte("[default]\naws_access_key_id = synthetic-access\naws_secret_access_key = synthetic-secret\n"), body: {0, 255, 1, 10}} {
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	stderr := new(bytes.Buffer)
	e := &env{flags: flags, stdout: output, stderr: stderr}
	factory := func(c storage.ObjectConfig) (objectTransferClient, error) {
		if config != nil {
			config(c)
		}
		return stub, nil
	}
	root := &cobra.Command{Use: "test", SilenceUsage: true, SilenceErrors: true}
	root.SetFlagErrorFunc(flagErrorFunc)
	root.PersistentFlags().StringVar(&flags.region, "region", "", "region")
	root.PersistentFlags().StringVar(&flags.projectID, "project-id", "", "project")
	cmd := newStorageGetObjectCmdWithClient(e, factory)
	pathFlag, path := "--destination", filepath.Join(home, "destination")
	if put {
		cmd = newStoragePutObjectCmdWithClient(e, factory)
		pathFlag, path = "--body", body
	}
	root.AddCommand(cmd)
	args := []string{cmd.Name(), "--s3-region", "HCM04", "--s3-credentials-file", creds, "--bucket", "bucket-1", "--key", "/a//../雪%2F+?#\x00", pathFlag, path}
	return root, args, stderr
}
func executeObjectCommand(root *cobra.Command, args []string) error {
	root.SetArgs(args)
	return root.Execute()
}

func TestStorageObjectsPreflight(t *testing.T) {
	for _, put := range []bool{true, false} {
		tests := []struct {
			name   string
			extra  []string
			flags  globalFlags
			remove string
		}{
			{name: "region", extra: []string{"--s3-region", "hcm04"}}, {name: "timeout small", extra: []string{"--transfer-timeout", "999ms"}}, {name: "timeout large", extra: []string{"--transfer-timeout", "1h1s"}},
			{name: "json", extra: []string{"--cli-input-json", "{}"}}, {name: "global region", extra: []string{"--region="}}, {name: "global project", extra: []string{"--project-id", "p"}}, {name: "positional", extra: []string{"extra"}},
			{name: "query", flags: globalFlags{query: "["}}, {name: "output", flags: globalFlags{output: "bad"}}, {name: "replace", extra: []string{"--replace"}},
			{name: "bucket", extra: []string{"--bucket", "../bad"}}, {name: "key", extra: []string{"--key", ""}}, {name: "utf8", extra: []string{"--key", string([]byte{255})}},
			{name: "required region", remove: "--s3-region"}, {name: "required credentials", remove: "--s3-credentials-file"}, {name: "required bucket", remove: "--bucket"}, {name: "required key", remove: "--key"},
		}
		pathFlag := "--destination"
		if put {
			pathFlag = "--body"
		}
		tests = append(tests, struct {
			name   string
			extra  []string
			flags  globalFlags
			remove string
		}{name: "required path", remove: pathFlag})
		for _, path := range []string{"-", "file://bad", "/absent-parent/object"} {
			tests = append(tests, struct {
				name   string
				extra  []string
				flags  globalFlags
				remove string
			}{name: path, extra: []string{pathFlag, path}})
		}
		if !put {
			for _, value := range []string{"0", "-1", "9223372036854775807"} {
				tests = append(tests, struct {
					name   string
					extra  []string
					flags  globalFlags
					remove string
				}{name: value, extra: []string{"--max-download-bytes", value}})
			}
		}
		for _, tc := range tests {
			t.Run(tc.name+map[bool]string{true: " put", false: " get"}[put], func(t *testing.T) {
				stub := new(objectCommandStub)
				constructed := 0
				root, args, _ := objectCommandFixture(t, put, stub, io.Discard, &tc.flags, func(storage.ObjectConfig) { constructed++ })
				if tc.remove != "" {
					for i, a := range args {
						if a == tc.remove {
							args = append(args[:i], args[i+2:]...)
							break
						}
					}
				}
				err := executeObjectCommand(root, append(args, tc.extra...))
				if err == nil || exitCode(err) != 2 || stub.calls != 0 || constructed != 0 {
					t.Fatalf("err=%v exit=%d calls=%d constructed=%d", err, exitCode(err), stub.calls, constructed)
				}
			})
		}
	}
}

func TestStorageObjectsReadOnly(t *testing.T) {
	for _, source := range []string{"flag", "env", "profile", "bad env", "bad profile", "HAN02"} {
		t.Run(source, func(t *testing.T) {
			flags := &globalFlags{yes: true}
			stub := new(objectCommandStub)
			root, args, _ := objectCommandFixture(t, true, stub, io.Discard, flags, nil)
			switch source {
			case "flag":
				flags.readOnly = true
			case "env":
				t.Setenv(envReadOnly, "1")
			case "bad env":
				t.Setenv(envReadOnly, "bad")
			case "profile", "bad profile":
				value := "true"
				if source == "bad profile" {
					value = "bad"
				}
				writeConfigFile(t, os.Getenv("HOME"), "[default]\nread_only="+value+"\n")
			case "HAN02":
				args = append(args, "--s3-region", "HAN02")
			}
			for i, a := range args {
				if a == "--s3-credentials-file" || a == "--body" {
					args[i+1] = "must-not-open"
				}
			}
			err := executeObjectCommand(root, args)
			if err == nil || exitCode(err) != 2 || stub.calls != 0 {
				t.Fatalf("err=%v calls=%d", err, stub.calls)
			}
		})
	}
}

func TestStorageObjectsTransfer(t *testing.T) {
	for _, put := range []bool{true, false} {
		for _, empty := range []bool{true, false} {
			t.Run(map[bool]string{true: "put", false: "get"}[put]+map[bool]string{true: " empty", false: " binary"}[empty], func(t *testing.T) {
				data := []byte{0, 255, 1, 10}
				if empty {
					data = nil
				}
				sum := sha256.Sum256(data)
				stdout := new(bytes.Buffer)
				flags := &globalFlags{yes: true}
				stub := new(objectCommandStub)
				var snapshot string
				check := func(ctx context.Context, bucket, key string) {
					t.Helper()
					if bucket != "bucket-1" || key != "/a//../雪%2F+?#\x00" {
						t.Fatal("input bytes changed")
					}
					deadline, ok := ctx.Deadline()
					if !ok || time.Until(deadline) > 5*time.Minute {
						t.Fatal("missing bounded deadline")
					}
				}
				stub.put = func(ctx context.Context, in *storage.PutObjectInput) (*storage.PutObjectOutput, error) {
					check(ctx, in.Bucket, in.Key)
					if in.Replace || in.ContentLength != int64(len(data)) {
						t.Fatal("incorrect PUT options")
					}
					b, err := io.ReadAll(in.Body)
					if err != nil || !bytes.Equal(b, data) {
						t.Fatalf("body=%v err=%v", b, err)
					}
					snapshot = in.Body.(*os.File).Name()
					return &storage.PutObjectOutput{ContentLength: int64(len(data)), SHA256: hex.EncodeToString(sum[:]), ETag: `"safe"`}, nil
				}
				stub.get = func(ctx context.Context, in *storage.GetObjectInput) (*storage.GetObjectOutput, error) {
					check(ctx, in.Bucket, in.Key)
					if in.MaxBytes != storage.DefaultMaxDownloadBytes {
						t.Fatal("download bound")
					}
					return &storage.GetObjectOutput{Body: io.NopCloser(bytes.NewReader(data)), ContentLength: -1, ETag: `"safe"`}, nil
				}
				root, args, _ := objectCommandFixture(t, put, stub, stdout, flags, func(c storage.ObjectConfig) {
					if c.Region != "HCM04" || c.Timeout != 5*time.Minute || c.Credentials.AccessKeyID.Reveal() != "synthetic-access" {
						t.Fatal("config")
					}
				})
				t.Setenv("VNGCLOUD_REGION", "invalid")
				t.Setenv("VNGCLOUD_PROJECT_ID", "ignored")
				t.Setenv("AWS_ACCESS_KEY_ID", "ambient-ignored")
				if put {
					if err := os.WriteFile(args[len(args)-1], data, 0o600); err != nil {
						t.Fatal(err)
					}
				}
				if err := executeObjectCommand(root, args); err != nil {
					t.Fatal(err)
				}
				if stub.calls != 1 {
					t.Fatalf("calls=%d", stub.calls)
				}
				var m map[string]any
				if err := json.Unmarshal(stdout.Bytes(), &m); err != nil {
					t.Fatal(err)
				}
				if m["ContentLength"] != float64(len(data)) || m["SHA256"] != hex.EncodeToString(sum[:]) || m["ETag"] != `"safe"` {
					t.Fatalf("metadata=%v", m)
				}
				if put {
					if _, ok := m["Destination"]; ok {
						t.Fatal("PUT destination")
					}
					if _, err := os.Stat(snapshot); !errors.Is(err, os.ErrNotExist) {
						t.Fatalf("snapshot retained: %v", err)
					}
				} else {
					b, err := os.ReadFile(args[len(args)-1])
					if err != nil || !bytes.Equal(b, data) {
						t.Fatalf("download=%v %v", b, err)
					}
				}
			})
		}
	}
}

type failingObjectWriter struct{}

func (failingObjectWriter) Write([]byte) (int, error) {
	return 0, errors.New("synthetic-secret raw writer failure")
}

func TestStorageObjectsCompletedError(t *testing.T) {
	for _, put := range []bool{true, false} {
		for _, failure := range []string{"stdout", "query", "cleanup"} {
			if !put && failure == "cleanup" {
				continue
			}
			t.Run(map[bool]string{true: "put ", false: "get "}[put]+failure, func(t *testing.T) {
				flags := &globalFlags{}
				output := io.Discard
				if failure == "stdout" {
					output = failingObjectWriter{}
				}
				if failure == "query" {
					flags.query = "length(ContentLength)"
				}
				stub := &objectCommandStub{put: func(_ context.Context, in *storage.PutObjectInput) (*storage.PutObjectOutput, error) {
					if failure == "cleanup" {
						if err := in.Body.(*os.File).Close(); err != nil {
							t.Fatal(err)
						}
					}
					return &storage.PutObjectOutput{ContentLength: 4}, nil
				}, get: func(context.Context, *storage.GetObjectInput) (*storage.GetObjectOutput, error) {
					return &storage.GetObjectOutput{Body: io.NopCloser(strings.NewReader("data")), ContentLength: 4}, nil
				}}
				root, args, _ := objectCommandFixture(t, put, stub, output, flags, nil)
				err := executeObjectCommand(root, args)
				if err == nil || exitCode(err) != 1 || !strings.Contains(err.Error(), "completed") || strings.Contains(err.Error(), "synthetic-secret") || stub.calls != 1 {
					t.Fatalf("error=%v calls=%d", err, stub.calls)
				}
				if !put {
					if b, e := os.ReadFile(args[len(args)-1]); e != nil || string(b) != "data" {
						t.Fatalf("completed file=%q %v", b, e)
					}
				}
			})
		}
	}
}

func TestStorageObjectsOutput(t *testing.T) {
	for _, put := range []bool{true, false} {
		for _, format := range []string{"json", "table", "text"} {
			for _, query := range []string{"", "Key"} {
				t.Run(map[bool]string{true: "put ", false: "get "}[put]+format+query, func(t *testing.T) {
					stdout := new(bytes.Buffer)
					flags := &globalFlags{output: format, query: query, debug: true}
					stub := new(objectCommandStub)
					key := "synthetic-secret/雪\x1b\n%2F"
					bucket := "synthetic-access"
					stub.put = func(_ context.Context, in *storage.PutObjectInput) (*storage.PutObjectOutput, error) {
						if in.Key != key || in.Bucket != bucket {
							t.Fatal("request changed")
						}
						return &storage.PutObjectOutput{ContentLength: 4, SHA256: "digest", ETag: `"safe"`}, nil
					}
					stub.get = func(_ context.Context, in *storage.GetObjectInput) (*storage.GetObjectOutput, error) {
						if in.Key != key || in.Bucket != bucket {
							t.Fatal("request changed")
						}
						return &storage.GetObjectOutput{Body: io.NopCloser(strings.NewReader("opaque-object-bytes")), ContentLength: 19, ETag: `"safe"`}, nil
					}
					root, args, stderr := objectCommandFixture(t, put, stub, stdout, flags, nil)
					args = append(args, "--bucket", bucket, "--key", key)
					if !put {
						args = append(args, "--destination", filepath.Join(os.Getenv("HOME"), "synthetic-secret-destination"))
					}
					if err := executeObjectCommand(root, args); err != nil {
						t.Fatal(err)
					}
					for _, stream := range []string{stdout.String(), stderr.String()} {
						for _, secret := range []string{"synthetic-secret", "synthetic-access", "opaque-object-bytes", "\x1b"} {
							if strings.Contains(stream, secret) {
								t.Fatalf("leaked %q in %q", secret, stream)
							}
						}
					}
					if !strings.Contains(stdout.String(), "[redacted]") {
						t.Fatalf("metadata was not redacted: %s", stdout)
					}
					for _, word := range []string{"Bucket", "Key", "Destination", "Authorization", "http", "雪", "HOME", os.Getenv("HOME")} {
						if strings.Contains(stderr.String(), word) {
							t.Fatalf("unsafe debug %q", stderr)
						}
					}
					if !strings.Contains(stderr.String(), "status=200") {
						t.Fatalf("debug lacks success status: %s", stderr)
					}
					if stub.calls != 1 {
						t.Fatal("retry")
					}
				})
			}
		}
	}
}

type objectReadFailure struct {
	ctx      context.Context
	cancel   context.CancelFunc
	err      error
	closed   int
	deadline time.Time
}

func (b *objectReadFailure) Read(p []byte) (int, error) {
	if d, ok := b.ctx.Deadline(); !ok || !d.Equal(b.deadline) {
		return 0, errors.New("lost CLI deadline")
	}
	if b.cancel != nil {
		b.cancel()
		return 0, b.ctx.Err()
	}
	copy(p, "partial")
	return len("partial"), b.err
}
func (b *objectReadFailure) Close() error { b.closed++; return nil }

func TestStorageObjectsTransferFailure(t *testing.T) {
	for _, mode := range []string{"read", "cancel", "deadline"} {
		t.Run(mode, func(t *testing.T) {
			flags := &globalFlags{}
			stdout := new(bytes.Buffer)
			stub := new(objectCommandStub)
			var body *objectReadFailure
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			api := &vngcloud.APIError{Operation: "storage.GetObject", StatusCode: 200, Code: "IncompleteObject", Message: "object body is incomplete", Err: io.ErrUnexpectedEOF}
			stub.get = func(ctx context.Context, _ *storage.GetObjectInput) (*storage.GetObjectOutput, error) {
				d, _ := ctx.Deadline()
				body = &objectReadFailure{ctx: ctx, deadline: d, err: api}
				if mode == "cancel" {
					body.cancel = cancel
				}
				if mode == "deadline" {
					return nil, &vngcloud.APIError{Operation: "storage.GetObject", Code: "RequestFailed", Err: context.DeadlineExceeded}
				}
				return &storage.GetObjectOutput{Body: body, ContentLength: 100}, nil
			}
			root, args, _ := objectCommandFixture(t, false, stub, stdout, flags, nil)
			root.SetContext(ctx)
			err := executeObjectCommand(root, args)
			if err == nil || exitCode(err) != 1 || stub.calls != 1 || stdout.Len() != 0 {
				t.Fatalf("err=%v calls=%d output=%q", err, stub.calls, stdout)
			}
			if mode == "read" {
				var got *vngcloud.APIError
				if !errors.As(err, &got) || got.Code != "IncompleteObject" || !errors.Is(err, io.ErrUnexpectedEOF) {
					t.Fatalf("lost SDK error: %v", err)
				}
			}
			if mode == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatalf("lost cancellation: %v", err)
			}
			if mode != "deadline" && body.closed != 1 {
				t.Fatalf("closed=%d", body.closed)
			}
			if _, err := os.Lstat(args[len(args)-1]); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("partial published %v", err)
			}
			paths, err := filepath.Glob(filepath.Join(os.Getenv("HOME"), ".vngcloud-object-*"))
			if err != nil || len(paths) != 0 {
				t.Fatalf("partial paths=%v err=%v", paths, err)
			}
		})
	}
}

func TestStorageObjectsPoliciesAndIsolation(t *testing.T) {
	for _, mode := range []string{"readonly get", "HAN02 replace", "HAN02 get", "occupied", "replace get", "profile output", "bad profile output", "flag output wins", "IAM trap"} {
		t.Run(mode, func(t *testing.T) {
			put := mode == "HAN02 replace"
			flags := &globalFlags{}
			stdout := new(bytes.Buffer)
			stub := new(objectCommandStub)
			stub.put = func(_ context.Context, in *storage.PutObjectInput) (*storage.PutObjectOutput, error) {
				if !in.Replace {
					t.Fatal("replace not forwarded")
				}
				return &storage.PutObjectOutput{ContentLength: 4}, nil
			}
			stub.get = func(_ context.Context, in *storage.GetObjectInput) (*storage.GetObjectOutput, error) {
				if in.MaxBytes != storage.DefaultMaxDownloadBytes {
					t.Fatal("bound")
				}
				return &storage.GetObjectOutput{Body: io.NopCloser(strings.NewReader("new")), ContentLength: 3}, nil
			}
			root, args, _ := objectCommandFixture(t, put, stub, stdout, flags, func(cfg storage.ObjectConfig) {
				want := "HCM04"
				if strings.HasPrefix(mode, "HAN02") {
					want = "HAN02"
				}
				if cfg.Region != want {
					t.Fatal("region default changed request")
				}
			})
			home := filepath.Dir(args[len(args)-1])
			switch mode {
			case "readonly get":
				flags.readOnly = true
				t.Setenv(envReadOnly, "true")
				writeConfigFile(t, home, "[default]\nread_only=true\n")
			case "HAN02 replace":
				flags.yes = true
				args = append(args, "--s3-region", "HAN02", "--replace")
			case "HAN02 get":
				args = append(args, "--s3-region", "HAN02")
			case "occupied", "replace get":
				if err := os.WriteFile(args[len(args)-1], []byte("old"), 0o600); err != nil {
					t.Fatal(err)
				}
				flags.yes = true
				if mode == "replace get" {
					args = append(args, "--replace")
				}
			case "profile output":
				writeConfigFile(t, home, "[default]\noutput=text\n")
			case "bad profile output":
				writeConfigFile(t, home, "[default]\noutput=bad\n")
				for i, a := range args {
					if a == "--s3-credentials-file" {
						args[i+1] = "never-open"
					}
				}
			case "flag output wins":
				flags.output = "json"
				writeConfigFile(t, home, "[default]\noutput=bad\n")
			case "IAM trap":
				writeConfigFile(t, home, "[default]\nregion=invalid\nproject_id=ignored\nauth_type=invalid\nclient_secret=ignored\n")
				writeCredentialsFile(t, home, "invalid credentials")
				if err := os.WriteFile(filepath.Join(home, ".vngcloud", "cache"), []byte("not a directory"), 0o600); err != nil {
					t.Fatal(err)
				}
				t.Setenv(envCredsFileVar, home)
				t.Setenv("VNGCLOUD_CLIENT_ID", "ignored")
				t.Setenv("VNGCLOUD_REGION", "invalid")
				t.Setenv("VNGCLOUD_PROJECT_ID", "ignored")
			}
			err := executeObjectCommand(root, args)
			if mode == "occupied" || mode == "bad profile output" {
				if exitCode(err) != 2 || stub.calls != 0 {
					t.Fatalf("err=%v calls=%d", err, stub.calls)
				}
				if mode == "occupied" {
					if b, e := os.ReadFile(args[len(args)-1]); e != nil || string(b) != "old" {
						t.Fatal("destination changed")
					}
				}
				return
			}
			if err != nil || stub.calls != 1 {
				t.Fatalf("err=%v calls=%d", err, stub.calls)
			}
			if mode == "profile output" && strings.Contains(stdout.String(), "{") {
				t.Fatal("profile output ignored")
			}
		})
	}
}

func TestStorageObjectsPublishedError(t *testing.T) {
	if !objectFilesSupported() {
		t.Skip("publication requires Linux or macOS")
	}
	withCleanEnv(t)
	destination := filepath.Join(t.TempDir(), "download")
	ops := defaultObjectFileOps()
	ops.syncDir = func(string) error { return errors.New("synthetic disk sync failure") }
	result, err := downloadObjectFileWithOps(context.Background(), io.NopCloser(strings.NewReader("completed")), destination, false, ops)
	got := objectDownloadError(result, err)
	if !result.Published || got == nil || exitCode(got) != 1 || !strings.Contains(got.Error(), "completed") || !strings.Contains(got.Error(), "do not retry") || !strings.Contains(got.Error(), "directory sync") {
		t.Fatalf("result=%+v err=%v", result, got)
	}
	if b, e := os.ReadFile(destination); e != nil || string(b) != "completed" {
		t.Fatalf("file=%q err=%v", b, e)
	}
}

func TestStorageObjectsSDKFailures(t *testing.T) {
	for _, put := range []bool{true, false} {
		for _, tc := range []struct {
			status int
			code   string
			cause  error
			exit   int
		}{{401, "InvalidAccessKeyId", vngcloud.ErrAuth, 3}, {403, "AccessDenied", vngcloud.ErrPermission, 1}, {404, "NoSuchKey", vngcloud.ErrNotFound, 4}, {412, "ObjectExists", storage.ErrObjectExists, 1}, {500, "InternalError", nil, 1}} {
			t.Run(map[bool]string{true: "put ", false: "get "}[put]+tc.code, func(t *testing.T) {
				api := &vngcloud.APIError{Operation: "storage.GetObject", StatusCode: tc.status, Code: tc.code, Message: "safe server refusal", Err: tc.cause}
				var sdkErr error = api
				if put {
					api.Operation = "storage.PutObject"
					if tc.status == 500 {
						sdkErr = fmt.Errorf("%w: download and compare before retrying: %w", storage.ErrObjectWriteUnconfirmed, api)
					}
				}
				stub := new(objectCommandStub)
				var snapshot string
				stub.put = func(_ context.Context, in *storage.PutObjectInput) (*storage.PutObjectOutput, error) {
					snapshot = in.Body.(*os.File).Name()
					return nil, sdkErr
				}
				stub.get = func(context.Context, *storage.GetObjectInput) (*storage.GetObjectOutput, error) { return nil, sdkErr }
				stdout := new(bytes.Buffer)
				root, args, stderr := objectCommandFixture(t, put, stub, stdout, &globalFlags{debug: true}, nil)
				err := executeObjectCommand(root, args)
				if !errors.Is(err, sdkErr) || exitCode(err) != tc.exit || stub.calls != 1 || stdout.Len() != 0 {
					t.Fatalf("error=%v exit=%d calls=%d", err, exitCode(err), stub.calls)
				}
				got := classify(err)
				if got.Status != tc.status || got.Operation != api.Operation {
					t.Fatalf("envelope=%+v", got)
				}
				if put && tc.status == 500 && (got.Code != "ObjectWriteUnconfirmed" || !strings.Contains(got.Message, "compare before retrying")) {
					t.Fatalf("recovery=%+v", got)
				}
				if put {
					if _, err := os.Stat(snapshot); !errors.Is(err, os.ErrNotExist) {
						t.Fatalf("snapshot=%v", err)
					}
				}
				if strings.Contains(stderr.String(), "bucket") || strings.Contains(stderr.String(), "Key") {
					t.Fatalf("debug=%s", stderr)
				}
			})
		}
	}
}

func TestStorageObjectsCallerDeadline(t *testing.T) {
	for _, put := range []bool{true, false} {
		t.Run(map[bool]string{true: "put", false: "get"}[put], func(t *testing.T) {
			deadline := time.Now().Add(time.Minute)
			ctx, cancel := context.WithDeadline(context.Background(), deadline)
			defer cancel()
			check := func(c context.Context) {
				t.Helper()
				d, ok := c.Deadline()
				if !ok || !d.Equal(deadline) {
					t.Fatalf("deadline=%v want=%v", d, deadline)
				}
			}
			var sourcePath string
			stub := &objectCommandStub{put: func(c context.Context, in *storage.PutObjectInput) (*storage.PutObjectOutput, error) {
				check(c)
				if err := os.WriteFile(sourcePath, []byte("changed after snapshot"), 0o600); err != nil {
					t.Fatal(err)
				}
				b, err := io.ReadAll(in.Body)
				if err != nil || !bytes.Equal(b, []byte{0, 255, 1, 10}) {
					t.Fatalf("snapshot changed: %v %v", b, err)
				}
				return &storage.PutObjectOutput{ContentLength: 4}, nil
			}, get: func(c context.Context, in *storage.GetObjectInput) (*storage.GetObjectOutput, error) {
				check(c)
				if in.MaxBytes != 9223372036854775806 {
					t.Fatal("explicit bound")
				}
				return &storage.GetObjectOutput{Body: &objectDeadlineBody{Reader: strings.NewReader("body"), ctx: c, check: check}, ContentLength: 4}, nil
			}}
			root, args, _ := objectCommandFixture(t, put, stub, io.Discard, &globalFlags{}, func(cfg storage.ObjectConfig) {
				if cfg.Timeout != time.Hour {
					t.Fatal("explicit timeout")
				}
			})
			sourcePath = args[len(args)-1]
			root.SetContext(ctx)
			args = append(args, "--transfer-timeout", "1h")
			if !put {
				args = append(args, "--max-download-bytes", "9223372036854775806")
			}
			if err := executeObjectCommand(root, args); err != nil {
				t.Fatal(err)
			}
		})
	}
}

type objectDeadlineBody struct {
	io.Reader
	ctx   context.Context
	check func(context.Context)
}

func (b *objectDeadlineBody) Read(p []byte) (int, error) { b.check(b.ctx); return b.Reader.Read(p) }
func (b *objectDeadlineBody) Close() error               { b.check(b.ctx); return nil }

func TestStorageObjectsDestinationPreserved(t *testing.T) {
	for _, kind := range []string{"regular", "directory", "symlink", "dangling"} {
		for _, replace := range []bool{true, false} {
			if kind == "regular" && replace {
				continue
			}
			t.Run(kind+strconv.FormatBool(replace), func(t *testing.T) {
				stub := new(objectCommandStub)
				root, args, _ := objectCommandFixture(t, false, stub, io.Discard, &globalFlags{yes: true}, nil)
				destination := args[len(args)-1]
				switch kind {
				case "regular":
					if err := os.WriteFile(destination, []byte("old"), 0o600); err != nil {
						t.Fatal(err)
					}
				case "directory":
					if err := os.Mkdir(destination, 0o700); err != nil {
						t.Fatal(err)
					}
				default:
					target := filepath.Join(filepath.Dir(destination), "target")
					if kind == "symlink" {
						if err := os.WriteFile(target, []byte("old"), 0o600); err != nil {
							t.Fatal(err)
						}
					}
					if err := os.Symlink(target, destination); err != nil {
						t.Fatal(err)
					}
				}
				info, err := os.Lstat(destination)
				if err != nil {
					t.Fatal(err)
				}
				if replace {
					args = append(args, "--replace")
				}
				err = executeObjectCommand(root, args)
				if exitCode(err) != 2 || stub.calls != 0 {
					t.Fatalf("err=%v calls=%d", err, stub.calls)
				}
				after, err := os.Lstat(destination)
				if err != nil || !os.SameFile(info, after) {
					t.Fatalf("destination changed %v", err)
				}
			})
		}
	}
}

func TestStorageObjectsCredentialsFailClosed(t *testing.T) {
	for _, put := range []bool{true, false} {
		t.Run(strconv.FormatBool(put), func(t *testing.T) {
			stub := new(objectCommandStub)
			root, args, _ := objectCommandFixture(t, put, stub, io.Discard, &globalFlags{}, nil)
			for i, a := range args {
				if a == "--s3-credentials-file" {
					if err := os.WriteFile(args[i+1], []byte("[default]\naws_secret_access_key=synthetic-secret\n"), 0o600); err != nil {
						t.Fatal(err)
					}
				}
			}
			err := executeObjectCommand(root, args)
			if !errors.Is(err, vngcloud.ErrCredentialsFile) || exitCode(err) != 3 || stub.calls != 0 || strings.Contains(err.Error(), "synthetic-secret") {
				t.Fatalf("err=%v calls=%d", err, stub.calls)
			}
		})
	}
}

func TestStorageObjectsUnsupported(t *testing.T) {
	if objectFilesSupported() {
		t.Skip("native platform supports object files")
	}
	withCleanEnv(t)
	for _, put := range []bool{true, false} {
		flags := &globalFlags{}
		e := &env{flags: flags, stdout: io.Discard, stderr: io.Discard}
		factory := func(storage.ObjectConfig) (objectTransferClient, error) {
			t.Fatal("constructed client on unsupported platform")
			return nil, errors.New("unexpected")
		}
		cmd := newStorageGetObjectCmdWithClient(e, factory)
		pathFlag := "--destination"
		if put {
			cmd = newStoragePutObjectCmdWithClient(e, factory)
			pathFlag = "--body"
		}
		cmd.SetArgs([]string{"--s3-region", "HCM04", "--s3-credentials-file", "never-open", "--bucket", "bucket-1", "--key", "key", pathFlag, "never-open"})
		if err := cmd.Execute(); exitCode(err) != 2 || !strings.Contains(err.Error(), "Linux or macOS") {
			t.Fatalf("error=%v", err)
		}
	}
}
