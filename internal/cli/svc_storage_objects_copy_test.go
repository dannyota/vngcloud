package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/storage"
)

func TestStorageCopyObjectRegistration(t *testing.T) {
	root := newRootCmd(strings.NewReader(""), io.Discard, io.Discard)
	cmd, _, err := root.Find([]string{"storage", "copy-object"})
	if err != nil || cmd.Name() != "copy-object" {
		t.Fatalf("COPY command missing: command=%s err=%v", cmd.Name(), err)
	}
	want := map[string]bool{"s3-region": true, "s3-credentials-file": true, "bucket": true, "key": true, "transfer-timeout": true, "source-bucket": true, "source-key": true, "replace": true}
	cmd.Flags().VisitAll(func(flag *pflag.Flag) {
		if !want[flag.Name] {
			t.Errorf("unexpected local flag %s", flag.Name)
		}
		delete(want, flag.Name)
	})
	if len(want) != 0 {
		t.Fatalf("missing flags=%v", want)
	}
	if cmd.Flags().Lookup("replace").DefValue != "false" || cmd.Flags().Lookup("transfer-timeout").DefValue != "5m0s" {
		t.Fatal("unsafe defaults")
	}
	if cmd.Long != storageCopyObjectNotes() || cmd.Example != storageCopyObjectExample() {
		t.Fatal("help helpers differ")
	}
	count := 0
	for _, sibling := range cmd.Parent().Commands() {
		if sibling.Name() == "copy-object" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("registrations=%d", count)
	}
}

type objectCopyStub struct {
	calls int
	run   func(context.Context, *storage.CopyObjectInput) (*storage.CopyObjectOutput, error)
}

func (s *objectCopyStub) CopyObject(ctx context.Context, in *storage.CopyObjectInput) (*storage.CopyObjectOutput, error) {
	s.calls++
	if s.run != nil {
		return s.run(ctx, in)
	}
	return &storage.CopyObjectOutput{}, nil
}
func copyFixture(t *testing.T, s *objectCopyStub, out io.Writer, flags *globalFlags, check func(storage.ObjectConfig)) (*cobra.Command, []string, *bytes.Buffer) {
	t.Helper()
	if !objectFilesSupported() {
		t.Skip("object credential files require Linux or macOS")
	}
	home := withCleanEnv(t)
	path := filepath.Join(home, "s3.credentials")
	if err := os.WriteFile(path, []byte("[default]\naws_access_key_id=synthetic-access\naws_secret_access_key=synthetic-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	stderr := new(bytes.Buffer)
	e := &env{flags: flags, stdout: out, stderr: stderr}
	root := &cobra.Command{Use: "test", SilenceUsage: true, SilenceErrors: true}
	root.SetFlagErrorFunc(flagErrorFunc)
	root.PersistentFlags().StringVar(&flags.region, "region", "", "region")
	root.PersistentFlags().StringVar(&flags.projectID, "project-id", "", "project")
	root.PersistentFlags().BoolVar(&flags.yes, "yes", false, "consent")
	root.PersistentFlags().BoolVar(&flags.readOnly, "read-only", false, "policy")
	root.AddCommand(newStorageCopyObjectCmdWithClient(e, func(cfg storage.ObjectConfig) (objectCopyClient, error) {
		if check != nil {
			check(cfg)
		}
		return s, nil
	}))
	return root, []string{"copy-object", "--s3-region", "HCM04", "--s3-credentials-file", path, "--bucket", "bucket-1", "--key", "/a//../雪%2F+?#\x00", "--source-bucket", "source-1", "--source-key", "source/雪%2F+?#\x00", "--replace", "--yes"}, stderr
}
func copyMissingCredentials(args []string) {
	for i, a := range args {
		if a == "--s3-credentials-file" {
			args[i+1] = "never-open"
		}
	}
}
func TestStorageCopyObjectPreflight(t *testing.T) {
	cases := []struct {
		name   string
		extra  []string
		remove string
		flags  globalFlags
	}{
		{name: "consent", remove: "--yes"},
		{name: "replace", remove: "--replace"},
		{name: "false replace", extra: []string{"--replace=false"}},
		{name: "source bucket required", remove: "--source-bucket"},
		{name: "source key required", remove: "--source-key"},
		{name: "source bucket invalid", extra: []string{"--source-bucket", "../bad"}},
		{name: "source key utf8", extra: []string{"--source-key", string([]byte{255})}},
		{name: "source key long", extra: []string{"--source-key", strings.Repeat("x", 1025)}},
		{name: "same targets", extra: []string{"--source-bucket", "bucket-1", "--source-key", "/a//../雪%2F+?#\x00"}},
		{name: "region", extra: []string{"--s3-region", "hcm04"}},
		{name: "missing region", remove: "--s3-region"},
		{name: "missing credentials", remove: "--s3-credentials-file"},
		{name: "missing bucket", remove: "--bucket"}, {name: "missing key", remove: "--key"},
		{name: "credentials stdin", extra: []string{"--s3-credentials-file", "-"}},
		{name: "credentials url", extra: []string{"--s3-credentials-file", "file://bad"}},
		{name: "bucket", extra: []string{"--bucket", "../bad"}},
		{name: "empty key", extra: []string{"--key", ""}},
		{name: "utf8 key", extra: []string{"--key", string([]byte{255})}},
		{name: "long key", extra: []string{"--key", strings.Repeat("x", 1025)}},
		{name: "short timeout", extra: []string{"--transfer-timeout", "999ms"}},
		{name: "long timeout", extra: []string{"--transfer-timeout", "1h1s"}},
		{name: "global region", extra: []string{"--region="}},
		{name: "global project", extra: []string{"--project-id", "p"}},
		{name: "output", flags: globalFlags{output: "bad"}},
		{name: "query", flags: globalFlags{query: "["}},
		{name: "profile", flags: globalFlags{profile: "bad/name"}},
		{name: "positional", extra: []string{"extra"}},
	}
	for _, name := range []string{"cli-input-json", "body", "destination", "version-id", "if-match", "if-none-match", "bypass-governance-retention", "mfa", "prefix", "all"} {
		cases = append(cases, struct {
			name   string
			extra  []string
			remove string
			flags  globalFlags
		}{name: name, extra: []string{"--" + name + "=bad"}})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := new(objectCopyStub)
			made := 0
			root, args, _ := copyFixture(t, s, io.Discard, &tc.flags, func(storage.ObjectConfig) { made++ })
			copyMissingCredentials(args)
			for i, a := range args {
				if a == tc.remove {
					n := 2
					if a == "--yes" || a == "--replace" {
						n = 1
					}
					args = append(args[:i], args[i+n:]...)
					break
				}
			}
			err := executeObjectCommand(root, append(args, tc.extra...))
			if exitCode(err) != 2 || made != 0 || s.calls != 0 {
				t.Fatalf("err=%v exit=%d made=%d calls=%d", err, exitCode(err), made, s.calls)
			}
		})
	}
}

func TestStorageCopyObjectReadOnly(t *testing.T) {
	for _, mode := range []string{"flag", "environment", "profile", "bad environment", "bad profile", "bad output", "malformed profile", "named profile", "flag cannot disable environment", "environment cannot disable profile"} {
		t.Run(mode, func(t *testing.T) {
			s := new(objectCopyStub)
			flags := new(globalFlags)
			made := 0
			root, args, _ := copyFixture(t, s, io.Discard, flags, func(storage.ObjectConfig) { made++ })
			switch mode {
			case "flag":
				args = append(args, "--read-only")
			case "environment":
				t.Setenv(envReadOnly, "true")
			case "profile":
				writeConfigFile(t, filepath.Dir(args[4]), "[default]\nread_only=true\n")
			case "bad environment":
				t.Setenv(envReadOnly, "bad")
			case "bad profile":
				writeConfigFile(t, filepath.Dir(args[4]), "[default]\nread_only=bad\n")
			case "bad output":
				writeConfigFile(t, filepath.Dir(args[4]), "[default]\noutput=bad\n")
			case "malformed profile":
				writeConfigFile(t, filepath.Dir(args[4]), "[default\n")
			case "named profile":
				flags.profile = "agent"
				writeConfigFile(t, filepath.Dir(args[4]), "[profile agent]\nread_only=true\n")
			case "flag cannot disable environment":
				args = append(args, "--read-only=false")
				t.Setenv(envReadOnly, "true")
			case "environment cannot disable profile":
				t.Setenv(envReadOnly, "false")
				writeConfigFile(t, filepath.Dir(args[4]), "[default]\nread_only=true\n")
			}
			copyMissingCredentials(args)
			err := executeObjectCommand(root, args)
			if exitCode(err) != 2 || made != 0 || s.calls != 0 {
				t.Fatalf("err=%v made=%d calls=%d", err, made, s.calls)
			}
		})
	}
}
func TestStorageCopyObjectDispatch(t *testing.T) {
	for _, region := range []string{"HCM04", "HAN02"} {
		t.Run(region, func(t *testing.T) {
			flags := &globalFlags{region: "ignored", projectID: "ignored", profile: "agent"}
			s := new(objectCopyStub)
			made := 0
			root, args, _ := copyFixture(t, s, io.Discard, flags, func(cfg storage.ObjectConfig) {
				made++
				if cfg.Region != region || cfg.Timeout != 5*time.Minute || cfg.Credentials.AccessKeyID.Reveal() != "synthetic-access" || cfg.Credentials.SecretAccessKey.Reveal() != "synthetic-secret" {
					t.Fatal("unexpected object configuration")
				}
			})
			home := filepath.Dir(args[4])
			writeConfigFile(t, home, "[profile agent]\nregion=invalid\nproject_id=ignored\nauth_type=invalid\nclient_secret=ignored\noutput=text\n")
			writeCredentialsFile(t, home, "invalid management credentials")
			cache := filepath.Join(home, ".vngcloud", "cache")
			if err := os.WriteFile(cache, []byte("not a directory"), 0o600); err != nil {
				t.Fatal(err)
			}
			t.Setenv(envCredsFileVar, home)
			t.Setenv("AWS_ACCESS_KEY_ID", "ambient")
			t.Setenv("AWS_SECRET_ACCESS_KEY", "ambient")
			t.Setenv("AWS_SHARED_CREDENTIALS_FILE", "never-open")
			t.Setenv("VNGCLOUD_REGION", "bad")
			t.Setenv("VNGCLOUD_PROJECT_ID", "bad")
			t.Setenv("PATH", t.TempDir())
			s.run = func(ctx context.Context, in *storage.CopyObjectInput) (*storage.CopyObjectOutput, error) {
				if in.Bucket != "bucket-1" || in.Key != "/a//../雪%2F+?#\x00" || in.SourceBucket != "source-1" || in.SourceKey != "source/雪%2F+?#\x00" || !in.Replace {
					t.Fatal("key changed")
				}
				deadline, ok := ctx.Deadline()
				if !ok || time.Until(deadline) > 5*time.Minute {
					t.Fatal("missing deadline")
				}
				return &storage.CopyObjectOutput{}, nil
			}
			if err := executeObjectCommand(root, append(args, "--s3-region", region)); err != nil || made != 1 || s.calls != 1 {
				t.Fatalf("err=%v made=%d calls=%d", err, made, s.calls)
			}
			if data, err := os.ReadFile(cache); err != nil || string(data) != "not a directory" {
				t.Fatal("management cache changed")
			}
		})
	}
}
func TestStorageCopyObjectOutput(t *testing.T) {
	for _, format := range []string{"json", "table", "text"} {
		for _, query := range []string{"", "SourceBucket", "SourceKey", "Bucket", "Key", "ETag", "LastModified", "VersionID", "SourceVersionID", "ServerSideEncryption"} {
			t.Run(format+"/"+query, func(t *testing.T) {
				s := new(objectCopyStub)
				out := new(bytes.Buffer)
				root, args, stderr := copyFixture(t, s, out, &globalFlags{output: format, query: query, debug: true}, nil)
				s.run = func(_ context.Context, in *storage.CopyObjectInput) (*storage.CopyObjectOutput, error) {
					if in.Bucket != "synthetic-access" || in.Key != "synthetic-secret/雪\x1b\n" || in.SourceBucket != "synthetic-secret" || in.SourceKey != "synthetic-access/雪\x1b\n" || !in.Replace {
						t.Fatal("redaction altered request")
					}
					return &storage.CopyObjectOutput{ETag: "quoted", VersionID: "destination-version", SourceVersionID: "source-version", ServerSideEncryption: "observed"}, nil
				}
				err := executeObjectCommand(root, append(args, "--bucket", "synthetic-access", "--key", "synthetic-secret/雪\x1b\n", "--source-bucket", "synthetic-secret", "--source-key", "synthetic-access/雪\x1b\n"))
				if err != nil || s.calls != 1 {
					t.Fatalf("err=%v calls=%d", err, s.calls)
				}
				for _, stream := range []string{out.String(), stderr.String()} {
					for _, word := range []string{"synthetic-access", "synthetic-secret", "\x1b", "Authorization", "httpClient", "SecretAccessKey", "ContentLength", "SHA256"} {
						if strings.Contains(stream, word) {
							t.Fatalf("leak %q: %q", word, stream)
						}
					}
				}
				debug := stderr.String()
				if !strings.Contains(debug, "operation=storage.CopyObject") || !strings.Contains(debug, "status=200") || !strings.Contains(debug, "count=1") || strings.Contains(debug, "bytes=") || len(strings.Fields(debug)) != 5 || strings.Contains(debug, "雪") {
					t.Fatalf("debug=%s", debug)
				}
				if format == "json" && query == "" {
					var data map[string]any
					if err := json.Unmarshal(out.Bytes(), &data); err != nil {
						t.Fatal(err)
					}
					if len(data) != 9 || data["LastModified"] != nil || data["VersionID"] != "destination-version" || data["SourceVersionID"] != "source-version" {
						t.Fatalf("output=%s", out)
					}
					for _, field := range []string{"Bucket", "Key", "SourceBucket", "SourceKey"} {
						if !strings.Contains(data[field].(string), "[redacted]") {
							t.Fatalf("unredacted %s", field)
						}
					}
				}
				if format == "json" && query == "LastModified" && strings.TrimSpace(out.String()) != "null" {
					t.Fatalf("time=%s", out)
				}
			})
		}
	}
}
func TestStorageCopyObjectOutcome(t *testing.T) {
	cases := []struct {
		name string
		err  error
		exit int
		code string
	}{
		{"credentials", vngcloud.ErrCredentialsFile, 3, "NoCredentials"},
		{"input", vngcloud.ErrInvalidInput, 2, "InvalidUsage"},
		{"config", vngcloud.ErrInvalidConfig, 2, "InvalidConfig"},
		{"ordinary", errors.New("safe failure"), 1, "RequestFailed"},
	}
	for _, status := range []int{401, 403, 404} {
		cause := error(nil)
		code := "RequestFailed"
		exit := 1
		if status == 401 {
			cause = vngcloud.ErrAuth
			exit = 3
		}
		if status == 404 {
			cause = vngcloud.ErrNotFound
			code = "NotFound"
			exit = 4
		}
		api := &vngcloud.APIError{Operation: "storage.CopyObject", StatusCode: status, Code: "RequestFailed", Message: "safe refusal", Err: cause}
		cases = append(cases, struct {
			name string
			err  error
			exit int
			code string
		}{strconv.Itoa(status), api, exit, code})
		cases = append(cases, struct {
			name string
			err  error
			exit int
			code string
		}{strconv.Itoa(status) + " ambiguous", errors.Join(storage.ErrObjectCopyUnconfirmed, api), 1, "ObjectCopyUnconfirmed"})
	}
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded} {
		cases = append(cases, struct {
			name string
			err  error
			exit int
			code string
		}{cause.Error(), cause, 1, "RequestFailed"})
		cases = append(cases, struct {
			name string
			err  error
			exit int
			code string
		}{cause.Error() + " ambiguous", errors.Join(storage.ErrObjectCopyUnconfirmed, cause), 1, "ObjectCopyUnconfirmed"})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := new(objectCopyStub)
			out := new(bytes.Buffer)
			root, args, stderr := copyFixture(t, s, out, &globalFlags{debug: true}, nil)
			s.run = func(context.Context, *storage.CopyObjectInput) (*storage.CopyObjectOutput, error) {
				return nil, tc.err
			}
			err := executeObjectCommand(root, args)
			if exitCode(err) != tc.exit || classify(err).Code != tc.code || s.calls != 1 || out.Len() != 0 || !errors.Is(err, tc.err) {
				t.Fatalf("err=%v exit=%d envelope=%+v calls=%d", err, exitCode(err), classify(err), s.calls)
			}
			if tc.code == "ObjectCopyUnconfirmed" && (!strings.Contains(stderr.String(), "code=ObjectCopyUnconfirmed") || classify(err).Message != storage.ErrObjectCopyUnconfirmed.Error()) {
				t.Fatalf("ambiguity=%+v debug=%s", classify(err), stderr)
			}
		})
	}
}
func TestStorageCopyObjectCompletedOutputFailure(t *testing.T) {
	for _, mode := range []string{"query", "stdout", "credentials", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			flags := new(globalFlags)
			writer := io.Discard
			if mode == "query" {
				flags.query = "length(LastModified)"
			}
			if mode == "stdout" {
				writer = failingObjectWriter{}
			}
			s := new(objectCopyStub)
			made := 0
			root, args, _ := copyFixture(t, s, writer, flags, func(storage.ObjectConfig) { made++ })
			if mode == "credentials" {
				copyMissingCredentials(args)
			}
			if mode == "canceled" {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				root.SetContext(ctx)
			}
			err := executeObjectCommand(root, args)
			if mode == "credentials" {
				if exitCode(err) != 3 || made != 0 || s.calls != 0 {
					t.Fatalf("err=%v made=%d calls=%d", err, made, s.calls)
				}
				return
			}
			if mode == "canceled" {
				if !errors.Is(err, context.Canceled) || made != 0 || s.calls != 0 {
					t.Fatalf("err=%v made=%d calls=%d", err, made, s.calls)
				}
				return
			}
			if exitCode(err) != 1 || s.calls != 1 || !strings.Contains(err.Error(), "copy completed; metadata output failed; do not retry the operation") || strings.Contains(err.Error(), "synthetic-secret") {
				t.Fatalf("err=%v calls=%d", err, s.calls)
			}
		})
	}
}

func TestStorageCopyObjectUnsupported(t *testing.T) {
	if objectFilesSupported() {
		t.Skip("native platform supports credential files")
	}
	withCleanEnv(t)
	flags := &globalFlags{yes: true}
	e := &env{flags: flags, stdout: io.Discard, stderr: io.Discard}
	made := 0
	cmd := newStorageCopyObjectCmdWithClient(e, func(storage.ObjectConfig) (objectCopyClient, error) { made++; return new(objectCopyStub), nil })
	cmd.SetArgs([]string{"--s3-region", "HCM04", "--s3-credentials-file", "never-open", "--bucket", "bucket-1", "--key", "key", "--source-bucket", "source-1", "--source-key", "source", "--replace"})
	err := cmd.Execute()
	if exitCode(err) != 2 || made != 0 || !strings.Contains(err.Error(), "Linux or macOS") {
		t.Fatalf("err=%v made=%d", err, made)
	}
}

func TestStorageCopyObjectConfiguration(t *testing.T) {
	for _, mode := range []string{"timeout", "profile output", "flag output", "environment profile", "factory failure", "credential mode"} {
		t.Run(mode, func(t *testing.T) {
			s := new(objectCopyStub)
			flags := new(globalFlags)
			out := new(bytes.Buffer)
			made := 0
			wantTimeout := 5 * time.Minute
			if mode == "timeout" {
				wantTimeout = time.Second
			}
			root, args, _ := copyFixture(t, s, out, flags, func(cfg storage.ObjectConfig) {
				made++
				if cfg.Timeout != wantTimeout {
					t.Fatal("timeout changed")
				}
			})
			switch mode {
			case "timeout":
				args = append(args, "--transfer-timeout", "1s")
			case "profile output":
				writeConfigFile(t, filepath.Dir(args[4]), "[default]\noutput=text\n")
			case "flag output":
				flags.output = "json"
				writeConfigFile(t, filepath.Dir(args[4]), "[default]\noutput=bad\n")
			case "environment profile":
				t.Setenv(envProfile, "agent")
				writeConfigFile(t, filepath.Dir(args[4]), "[profile agent]\noutput=text\n")
			case "factory failure":
				e := &env{flags: flags, stdout: out, stderr: io.Discard}
				root.RemoveCommand(root.Commands()[0])
				root.AddCommand(newStorageCopyObjectCmdWithClient(e, func(storage.ObjectConfig) (objectCopyClient, error) { made++; return nil, vngcloud.ErrInvalidConfig }))
			case "credential mode":
				if err := os.Chmod(args[4], 0o644); err != nil { //nolint:gosec // Deliberately insecure synthetic credentials verify refusal.
					t.Fatal(err)
				}
			}
			err := executeObjectCommand(root, args)
			if mode == "factory failure" {
				if exitCode(err) != 2 || made != 1 || s.calls != 0 {
					t.Fatalf("err=%v made=%d calls=%d", err, made, s.calls)
				}
				return
			}
			if mode == "credential mode" {
				if exitCode(err) != 3 || made != 0 || s.calls != 0 {
					t.Fatalf("err=%v made=%d calls=%d", err, made, s.calls)
				}
				return
			}
			if err != nil || made != 1 || s.calls != 1 {
				t.Fatalf("err=%v made=%d calls=%d", err, made, s.calls)
			}
			if (mode == "profile output" || mode == "environment profile") && strings.HasPrefix(out.String(), "{") {
				t.Fatal("profile output ignored")
			}
		})
	}
}

func TestStorageCopyObjectAcceptedTime(t *testing.T) {
	for _, format := range []string{"json", "table", "text"} {
		for _, query := range []string{"", "LastModified"} {
			t.Run(format+"/"+query, func(t *testing.T) {
				s := new(objectCopyStub)
				out := new(bytes.Buffer)
				root, args, _ := copyFixture(t, s, out, &globalFlags{output: format, query: query}, nil)
				accepted := time.Date(2025, time.January, 2, 3, 4, 5, 123000000, time.UTC)
				s.run = func(context.Context, *storage.CopyObjectInput) (*storage.CopyObjectOutput, error) {
					return &storage.CopyObjectOutput{LastModified: &accepted}, nil
				}
				if err := executeObjectCommand(root, args); err != nil || s.calls != 1 {
					t.Fatalf("err=%v calls=%d", err, s.calls)
				}
				if !strings.Contains(out.String(), "2025-01-02T03:04:05.123Z") {
					t.Fatalf("time=%s", out)
				}
			})
		}
	}
}

func TestStorageCopyObjectInternalPreflight(t *testing.T) {
	flags := &globalFlags{yes: true}
	e := &env{flags: flags, stdout: io.Discard, stderr: io.Discard}
	f := &storageCopyObjectFlags{storageObjectOperationFlags: storageObjectOperationFlags{region: "HCM04", credentialsFile: "never-open", bucket: "bucket-1", key: "key", timeout: time.Second}, sourceBucket: "source-1", sourceKey: "key", replace: true}
	cmd := &cobra.Command{}
	factory := func(storage.ObjectConfig) (objectCopyClient, error) {
		t.Fatal("constructed before preflight")
		return nil, nil
	}
	for _, tc := range []struct {
		cmd *cobra.Command
		e   *env
		f   *storageCopyObjectFlags
	}{{nil, e, f}, {cmd, nil, f}, {cmd, &env{}, f}, {cmd, e, nil}} {
		if err := runStorageCopyObject(tc.cmd, tc.e, tc.f, factory); exitCode(err) != 2 {
			t.Fatalf("err=%v", err)
		}
	}
	if err := runStorageCopyObject(cmd, e, f, nil); exitCode(err) != 2 {
		t.Fatalf("nil factory err=%v", err)
	}
}
