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

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/storage"
)

func TestStorageDeleteObjectRegistration(t *testing.T) {
	root := newRootCmd(strings.NewReader(""), io.Discard, io.Discard)
	cmd, _, err := root.Find([]string{"storage", "delete-object"})
	if err != nil || cmd.Name() != "delete-object" {
		t.Fatalf("DELETE command missing: command=%s err=%v", cmd.Name(), err)
	}
}

type objectDeleteStub struct {
	calls int
	run   func(context.Context, *storage.DeleteObjectInput) (*storage.DeleteObjectOutput, error)
}

func (s *objectDeleteStub) DeleteObject(ctx context.Context, in *storage.DeleteObjectInput) (*storage.DeleteObjectOutput, error) {
	s.calls++
	if s.run != nil {
		return s.run(ctx, in)
	}
	return &storage.DeleteObjectOutput{}, nil
}
func deleteFixture(t *testing.T, s *objectDeleteStub, out io.Writer, flags *globalFlags, check func(storage.ObjectConfig)) (*cobra.Command, []string, *bytes.Buffer) {
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
	root.AddCommand(newStorageDeleteObjectCmdWithClient(e, func(cfg storage.ObjectConfig) (objectDeleteClient, error) {
		if check != nil {
			check(cfg)
		}
		return s, nil
	}))
	return root, []string{"delete-object", "--s3-region", "HCM04", "--s3-credentials-file", path, "--bucket", "bucket-1", "--key", "/a//../雪%2F+?#\x00", "--yes"}, stderr
}
func deleteMissingCredentials(args []string) {
	for i, a := range args {
		if a == "--s3-credentials-file" {
			args[i+1] = "never-open"
		}
	}
}
func TestStorageDeleteObjectPolicy(t *testing.T) {
	cases := []struct {
		name   string
		extra  []string
		remove string
		flags  globalFlags
	}{
		{name: "consent", remove: "--yes"},
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
	for _, name := range []string{"cli-input-json", "body", "destination", "replace", "version-id", "if-match", "if-none-match", "bypass-governance-retention", "mfa", "prefix", "all"} {
		cases = append(cases, struct {
			name   string
			extra  []string
			remove string
			flags  globalFlags
		}{name: name, extra: []string{"--" + name + "=bad"}})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := new(objectDeleteStub)
			made := 0
			root, args, _ := deleteFixture(t, s, io.Discard, &tc.flags, func(storage.ObjectConfig) { made++ })
			deleteMissingCredentials(args)
			for i, a := range args {
				if a == tc.remove {
					n := 2
					if a == "--yes" {
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
	for _, mode := range []string{"flag", "environment", "profile", "bad environment", "bad profile", "bad output", "malformed profile", "named profile", "flag cannot disable environment", "environment cannot disable profile"} {
		t.Run(mode, func(t *testing.T) {
			s := new(objectDeleteStub)
			flags := new(globalFlags)
			made := 0
			root, args, _ := deleteFixture(t, s, io.Discard, flags, func(storage.ObjectConfig) { made++ })
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
			deleteMissingCredentials(args)
			err := executeObjectCommand(root, args)
			if exitCode(err) != 2 || made != 0 || s.calls != 0 {
				t.Fatalf("err=%v made=%d calls=%d", err, made, s.calls)
			}
		})
	}
}
func TestStorageDeleteObjectIsolation(t *testing.T) {
	for _, region := range []string{"HCM04", "HAN02"} {
		t.Run(region, func(t *testing.T) {
			flags := &globalFlags{region: "ignored", projectID: "ignored", profile: "agent"}
			s := new(objectDeleteStub)
			made := 0
			root, args, _ := deleteFixture(t, s, io.Discard, flags, func(cfg storage.ObjectConfig) {
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
			s.run = func(ctx context.Context, in *storage.DeleteObjectInput) (*storage.DeleteObjectOutput, error) {
				if in.Bucket != "bucket-1" || in.Key != "/a//../雪%2F+?#\x00" {
					t.Fatal("key changed")
				}
				deadline, ok := ctx.Deadline()
				if !ok || time.Until(deadline) > 5*time.Minute {
					t.Fatal("missing deadline")
				}
				return &storage.DeleteObjectOutput{}, nil
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
func TestStorageDeleteObjectOutput(t *testing.T) {
	for _, format := range []string{"json", "table", "text"} {
		for _, query := range []string{"", "Bucket", "Key", "DeleteMarker", "VersionID"} {
			for _, marker := range []string{"nil", "true", "false"} {
				t.Run(format+"/"+query+"/"+marker, func(t *testing.T) {
					s := new(objectDeleteStub)
					out := new(bytes.Buffer)
					root, args, stderr := deleteFixture(t, s, out, &globalFlags{output: format, query: query, debug: true}, nil)
					s.run = func(_ context.Context, in *storage.DeleteObjectInput) (*storage.DeleteObjectOutput, error) {
						if in.Bucket != "synthetic-access" || in.Key != "synthetic-secret/雪\x1b\n" {
							t.Fatal("redaction altered request")
						}
						var b *bool
						if marker != "nil" {
							value := marker == "true"
							b = &value
						}
						return &storage.DeleteObjectOutput{DeleteMarker: b, VersionID: "observed"}, nil
					}
					err := executeObjectCommand(root, append(args, "--bucket", "synthetic-access", "--key", "synthetic-secret/雪\x1b\n"))
					if err != nil || s.calls != 1 {
						t.Fatalf("err=%v calls=%d", err, s.calls)
					}
					for _, stream := range []string{out.String(), stderr.String()} {
						for _, word := range []string{"synthetic-access", "synthetic-secret", "\x1b", "Authorization", "httpClient", "SecretAccessKey"} {
							if strings.Contains(stream, word) {
								t.Fatalf("leak %q: %q", word, stream)
							}
						}
					}
					debug := stderr.String()
					if !strings.Contains(debug, "operation=storage.DeleteObject") || !strings.Contains(debug, "status=204") || !strings.Contains(debug, "bytes=0") || len(strings.Fields(debug)) != 5 || strings.Contains(debug, "雪") {
						t.Fatalf("debug=%s", debug)
					}
					if format == "json" && query == "" {
						var data map[string]any
						if err := json.Unmarshal(out.Bytes(), &data); err != nil {
							t.Fatal(err)
						}
						if len(data) != 4 || data["VersionID"] != "observed" || !strings.Contains(data["Key"].(string), "[redacted]") {
							t.Fatalf("output=%s", out)
						}
						value, ok := data["DeleteMarker"]
						if !ok || (marker == "nil" && value != nil) || (marker != "nil" && value != (marker == "true")) {
							t.Fatalf("marker=%v", value)
						}
					}
				})
			}
		}
	}
}
func TestStorageDeleteObjectErrorPrecedence(t *testing.T) {
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
		api := &vngcloud.APIError{Operation: "storage.DeleteObject", StatusCode: status, Code: "RequestFailed", Message: "safe refusal", Err: cause}
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
		}{strconv.Itoa(status) + " ambiguous", errors.Join(storage.ErrObjectDeleteUnconfirmed, api), 1, "ObjectDeleteUnconfirmed"})
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
		}{cause.Error() + " ambiguous", errors.Join(storage.ErrObjectDeleteUnconfirmed, cause), 1, "ObjectDeleteUnconfirmed"})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := new(objectDeleteStub)
			out := new(bytes.Buffer)
			root, args, stderr := deleteFixture(t, s, out, &globalFlags{debug: true}, nil)
			s.run = func(context.Context, *storage.DeleteObjectInput) (*storage.DeleteObjectOutput, error) {
				return nil, tc.err
			}
			err := executeObjectCommand(root, args)
			if exitCode(err) != tc.exit || classify(err).Code != tc.code || s.calls != 1 || out.Len() != 0 || !errors.Is(err, tc.err) {
				t.Fatalf("err=%v exit=%d envelope=%+v calls=%d", err, exitCode(err), classify(err), s.calls)
			}
			if tc.code == "ObjectDeleteUnconfirmed" && (!strings.Contains(stderr.String(), "code=ObjectDeleteUnconfirmed") || classify(err).Message != storage.ErrObjectDeleteUnconfirmed.Error()) {
				t.Fatalf("ambiguity=%+v debug=%s", classify(err), stderr)
			}
		})
	}
}
func TestStorageDeleteObjectCompletedError(t *testing.T) {
	for _, mode := range []string{"query", "stdout", "credentials", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			flags := new(globalFlags)
			writer := io.Discard
			if mode == "query" {
				flags.query = "length(DeleteMarker)"
			}
			if mode == "stdout" {
				writer = failingObjectWriter{}
			}
			s := new(objectDeleteStub)
			made := 0
			root, args, _ := deleteFixture(t, s, writer, flags, func(storage.ObjectConfig) { made++ })
			if mode == "credentials" {
				deleteMissingCredentials(args)
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
			if exitCode(err) != 1 || s.calls != 1 || !strings.Contains(err.Error(), "delete completed; metadata output failed; do not retry the operation") || strings.Contains(err.Error(), "synthetic-secret") {
				t.Fatalf("err=%v calls=%d", err, s.calls)
			}
		})
	}
}

func TestStorageDeleteObjectUnsupported(t *testing.T) {
	if objectFilesSupported() {
		t.Skip("native platform supports credential files")
	}
	withCleanEnv(t)
	flags := &globalFlags{yes: true}
	e := &env{flags: flags, stdout: io.Discard, stderr: io.Discard}
	made := 0
	cmd := newStorageDeleteObjectCmdWithClient(e, func(storage.ObjectConfig) (objectDeleteClient, error) { made++; return new(objectDeleteStub), nil })
	cmd.SetArgs([]string{"--s3-region", "HCM04", "--s3-credentials-file", "never-open", "--bucket", "bucket-1", "--key", "key"})
	err := cmd.Execute()
	if exitCode(err) != 2 || made != 0 || !strings.Contains(err.Error(), "Linux or macOS") {
		t.Fatalf("err=%v made=%d", err, made)
	}
}

func TestStorageDeleteObjectConfiguration(t *testing.T) {
	for _, mode := range []string{"timeout", "profile output", "flag output", "environment profile", "factory failure", "credential mode"} {
		t.Run(mode, func(t *testing.T) {
			s := new(objectDeleteStub)
			flags := new(globalFlags)
			out := new(bytes.Buffer)
			made := 0
			wantTimeout := 5 * time.Minute
			if mode == "timeout" {
				wantTimeout = time.Second
			}
			root, args, _ := deleteFixture(t, s, out, flags, func(cfg storage.ObjectConfig) {
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
				root.AddCommand(newStorageDeleteObjectCmdWithClient(e, func(storage.ObjectConfig) (objectDeleteClient, error) { made++; return nil, vngcloud.ErrInvalidConfig }))
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
