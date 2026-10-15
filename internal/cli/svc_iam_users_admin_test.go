package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/iam"
)

func TestIAMResetPasswordConsent(t *testing.T) {
	fixture := newSvcFixture(nil)
	root, _, _ := newSvcRoot(t, fixture)
	root.SetIn(iamUnreadableStdin{t})
	root.SetArgs([]string{"iam", "reset-user-password", "--user-id", "user-1", "--password-file", "-"})
	err := root.Execute()
	if err == nil || exitCode(err) != 2 || !strings.Contains(err.Error(), "--yes") || fixture.requestCount() != 0 {
		t.Fatalf("missing consent allowed input or request: %v", err)
	}
}

func TestIAMResetPasswordAcceptance(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/accounts-api/v1/auth/userinfo":                              jsonHandler(200, `{"userId":"caller","userType":"iam-user"}`),
		"/accounts-api/v1/iam-users/user-1":                           jsonHandler(200, `{"id":"user-1","from":"iam"}`),
		"/policies-api/v1/actions":                                    jsonHandler(200, `[{"action":"CreatePolicy","label":"Write"}]`),
		"/policies-api/v1/user-attachments/iam-users/user-1/policies": jsonHandler(200, `{"data":[],"totalItems":0}`),
		"/policies-api/v1/user-attachments/iam-users/user-1/groups":   jsonHandler(200, `[]`),
		"/accounts-api/v1/iam-users/user-1/reset-password":            jsonHandler(204, ""),
	})
	root, stdout, _ := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "iam", "reset-user-password", "--user-id", "user-1", "--secret-file", filepath.Join(t.TempDir(), "password"), "--yes"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), `"Password": "[redacted]"`) || !strings.Contains(stdout.String(), `"SecretFile":`) {
		t.Fatal("generated output missing redaction or saved path")
	}
}

func iamResetFixture(post func(http.ResponseWriter, *http.Request)) *svcFixture {
	return newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/accounts-api/v1/auth/userinfo":                              jsonHandler(200, `{"userId":"caller","userType":"iam-user"}`),
		"/accounts-api/v1/iam-users/user-1":                           jsonHandler(200, `{"id":"user-1","from":"iam"}`),
		"/policies-api/v1/actions":                                    jsonHandler(200, `[{"action":"CreatePolicy","label":"Write"}]`),
		"/policies-api/v1/user-attachments/iam-users/user-1/policies": jsonHandler(200, `{"data":[],"totalItems":0}`),
		"/policies-api/v1/user-attachments/iam-users/user-1/groups":   jsonHandler(200, `[]`),
		"/accounts-api/v1/iam-users/user-1/reset-password":            post,
	})
}

func TestIAMResetPasswordModesAndOutcomes(t *testing.T) {
	for _, mode := range []string{"generated", "stdin", "file"} {
		for _, status := range []int{204, 400, 401, 403, 404, 409, 500} {
			t.Run(fmt.Sprintf("%s/%d", mode, status), func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "password")
				const canary = "synthetic replacement password"
				sent := ""
				fixture := iamResetFixture(func(w http.ResponseWriter, r *http.Request) {
					var body map[string]string
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
					}
					sent = body["password"]
					if r.Method != "POST" || len(body) != 1 || sent == "" {
						t.Error("wrong reset body")
					}
					w.WriteHeader(status)
					if status != 204 {
						_, _ = fmt.Fprintf(w, `{"code":%q,"message":%q}`, sent, sent)
					}
				})
				root, stdout, stderr := newSvcRoot(t, fixture)
				args := []string{"--region", "hcm-3", "--debug", "iam", "reset-user-password", "--user-id", "user-1", "--yes"}
				switch mode {
				case "generated":
					args = append(args, "--secret-file", path, "--password-length", "8")
				case "stdin":
					root.SetIn(strings.NewReader(canary + "\r\n"))
					args = append(args, "--password-file", "-")
				case "file":
					if err := os.WriteFile(path, []byte(canary+"\n"), 0o600); err != nil {
						t.Fatal(err)
					}
					args = append(args, "--password-file", path)
				}
				root.SetArgs(args)
				err := root.Execute()
				if (err != nil) != (status != 204) || fixture.requestCount() != 6 {
					t.Fatalf("unexpected result or follow-up: %v", err)
				}
				if mode != "generated" && sent != canary {
					t.Fatal("supplied value changed")
				}
				if strings.Contains(stdout.String()+stderr.String(), sent) {
					t.Fatal("password leaked")
				}
				if err != nil {
					if stdout.Len() != 0 {
						t.Fatal("partial object printed")
					}
					assertIAMResetErrorPrivate(t, err, sent)
				}
				if status == 500 && (!errors.Is(err, iam.ErrUserWriteUncertain) || exitCode(err) != 1) {
					t.Fatal("uncertainty identity lost")
				}
				if mode == "generated" && (status == 204 || status == 500) {
					data, readErr := os.ReadFile(path)
					if readErr != nil || string(data) != sent+"\n" || len(sent) != 8 {
						t.Fatal("generated password not saved")
					}
					info, statErr := os.Stat(path)
					if statErr != nil || info.Mode().Perm() != 0o600 {
						t.Fatal("wrong persistence mode")
					}
					if err != nil && !strings.Contains(err.Error(), "saved to "+path) {
						t.Fatal("saved partial result not reported")
					}
				}
				if mode == "generated" && status >= 400 && status < 500 {
					if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
						t.Fatal("rejection created file")
					}
				}
				if mode != "generated" && (strings.Contains(stdout.String(), "Password") || strings.Contains(stdout.String(), "SecretFile")) {
					t.Fatal("supplied output has secret fields")
				}
			})
		}
	}
}

func assertIAMResetErrorPrivate(t *testing.T, err error, canary string) {
	t.Helper()
	if err == nil {
		return
	}
	if strings.Contains(err.Error(), canary) {
		t.Fatal("password reached error node")
	}
	if multi, ok := err.(interface{ Unwrap() []error }); ok {
		for _, child := range multi.Unwrap() {
			assertIAMResetErrorPrivate(t, child, canary)
		}
	} else {
		assertIAMResetErrorPrivate(t, errors.Unwrap(err), canary)
	}
}

func TestIAMResetPasswordReadOnlyBeforeInput(t *testing.T) {
	for _, source := range []string{"flag", "environment", "profile"} {
		t.Run(source, func(t *testing.T) {
			home := withCleanEnv(t)
			if source == "profile" {
				writeCredentialsFile(t, home, "[default]\nusername = synthetic\npassword = synthetic\nroot_email = synthetic@example.invalid\n")
				writeConfigFile(t, home, "[default]\nregion = hcm-3\nread_only = true\n")
			}
			if source == "environment" {
				t.Setenv("VNGCLOUD_READ_ONLY", "1")
			}
			var out bytes.Buffer
			root := newRootCmd(iamUnreadableStdin{t}, &out, &out)
			args := []string{"--region", "hcm-3", "iam", "reset-user-password", "--user-id", "user-1", "--password-file", "-", "--yes"}
			if source == "flag" {
				args = append(args, "--read-only")
			}
			root.SetArgs(args)
			if err := root.Execute(); err == nil || classify(err).Code != "ReadOnly" {
				t.Fatalf("wrong refusal: %v", err)
			}
		})
	}
}

func TestIAMResetPasswordInputAndPreflight(t *testing.T) {
	existing := filepath.Join(t.TempDir(), "existing")
	if err := os.WriteFile(existing, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{}, {"--secret-file", "new", "--password-file", "-"},
		{"--password-file", "-", "--password-length", "0"},
		{"--password-file", "-", "--cli-input-json", `{"Password":"synthetic-input"}`},
		{"--password-file", "-", "--cli-input-json", `{"PasswordLength":8}`},
		{"--password-file", "-", "--cli-input-json", "file:///missing-input"},
		{"--password", "synthetic-input"}, {"--secret-file", existing},
		{"--secret-file", filepath.Join(t.TempDir(), "absent", "password")},
	} {
		fixture := newSvcFixture(nil)
		root, _, _ := newSvcRoot(t, fixture)
		root.SetIn(iamUnreadableStdin{t})
		root.SetArgs(append([]string{"iam", "reset-user-password", "--user-id", "user-1", "--yes"}, args...))
		err := root.Execute()
		if err == nil || exitCode(err) != 2 || fixture.requestCount() != 0 || strings.Contains(err.Error(), "synthetic-input") {
			t.Fatalf("unsafe input: %v", err)
		}
	}
}

func iamResetRootWithOps(t *testing.T, fixture *svcFixture, ops iamPasswordFileOps, unsettled bool) (*cobra.Command, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	withCleanEnv(t)
	opts := append(newFakeServer(t, fixture.mux), vngcloud.WithStaticToken("test-token"))
	if unsettled {
		opts = append(opts, vngcloud.WithHTTPClient(&http.Client{Transport: saCLITransport(func(r *http.Request) (*http.Response, error) {
			resp, err := refusingTransport{base: http.DefaultTransport}.RoundTrip(r)
			if err == nil && r.Method == "POST" {
				resp.Body = iamResetCloseFailure{resp.Body}
			}
			return resp, err
		})}))
	}
	withTestOptions(t, opts...)
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	e := &env{flags: &globalFlags{}, stdin: strings.NewReader(""), stdout: stdout, stderr: stderr}
	root := newTestRoot(e)
	op := iamResetUserPasswordOp()
	op.call = func(cmd *cobra.Command, client *iam.Client, ctx context.Context, input any) (any, error) {
		return callIAMResetUserPassword(cmd, client, ctx, input, ops)
	}
	root.AddCommand(Service(e, "iam", "IAM", iam.New, op))
	return root, stdout, stderr
}

type iamResetCloseFailure struct{ io.ReadCloser }

func (b iamResetCloseFailure) Close() error { _ = b.ReadCloser.Close(); return context.Canceled }

func TestIAMResetPasswordPersistenceFailures(t *testing.T) {
	for _, outcome := range []string{"accepted", "unsettled", "uncertain"} {
		for _, phase := range []string{"open", "write", "short", "sync", "close", "removal"} {
			t.Run(outcome+"/"+phase, func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "password")
				sent := ""
				fixture := iamResetFixture(func(w http.ResponseWriter, r *http.Request) {
					var body map[string]string
					_ = json.NewDecoder(r.Body).Decode(&body)
					sent = body["password"]
					if outcome == "uncertain" {
						w.WriteHeader(500)
					} else {
						w.WriteHeader(204)
					}
				})
				removed := false
				ops := iamPasswordFileOps{
					open: func(string) (iamPasswordFile, error) {
						if phase == "open" {
							return nil, errors.New("synthetic canary")
						}
						failure := phase
						if phase == "removal" {
							failure = "write"
						}
						return failingIAMPasswordFile{failure}, nil
					},
					remove: func(string) error {
						removed = true
						if phase == "removal" {
							return errors.New("synthetic canary")
						}
						return nil
					},
				}
				root, stdout, stderr := iamResetRootWithOps(t, fixture, ops, outcome == "unsettled")
				root.SetArgs([]string{"iam", "reset-user-password", "--user-id", "user-1", "--secret-file", path, "--yes", "--debug"})
				err := root.Execute()
				if err == nil || classify(err).Code != "SecretFileFailed" || exitCode(err) != 1 || stdout.Len() != 0 || fixture.requestCount() != 6 {
					t.Fatalf("wrong file outcome: %v", err)
				}
				if removed != (phase != "open") || strings.Contains(err.Error(), "partial password file may remain") != (phase == "removal") {
					t.Fatal("unsafe removal reporting")
				}
				for _, want := range []string{path, "for user user-1", "reset-user-password --user-id user-1 --secret-file <fresh-private-path> --yes"} {
					if !strings.Contains(err.Error(), want) {
						t.Fatal("missing manual recovery")
					}
				}
				if errors.Is(err, iam.ErrUserWriteUncertain) != (outcome == "uncertain") || errors.Is(err, iam.ErrNotSettled) != (outcome == "unsettled") {
					t.Fatal("outcome identity lost")
				}
				if strings.Contains(err.Error(), "reset was accepted") != (outcome != "uncertain") {
					t.Fatal("uncertain reset claimed acceptance")
				}
				assertIAMResetErrorPrivate(t, err, sent)
				assertIAMResetErrorPrivate(t, err, "synthetic canary")
				if strings.Contains(stdout.String()+stderr.String(), sent) {
					t.Fatal("password leaked")
				}
			})
		}
	}
}

func TestIAMResetPasswordUnsettledSaved(t *testing.T) {
	path := filepath.Join(t.TempDir(), "password")
	fixture := iamResetFixture(jsonHandler(204, ""))
	root, stdout, _ := iamResetRootWithOps(t, fixture, userPasswordFileOps(), true)
	root.SetArgs([]string{"iam", "reset-user-password", "--user-id", "user-1", "--secret-file", path, "--yes"})
	err := root.Execute()
	if !errors.Is(err, iam.ErrNotSettled) || classify(err).Code != "NotSettled" || stdout.Len() != 0 || fixture.requestCount() != 6 || !strings.Contains(err.Error(), "saved to "+path) {
		t.Fatalf("accepted partial result lost: %v", err)
	}
	data, readErr := os.ReadFile(path)
	if readErr != nil || len(data) != 33 {
		t.Fatal("accepted password lost")
	}
	assertIAMResetErrorPrivate(t, err, strings.TrimSuffix(string(data), "\n"))
}

func TestIAMResetPasswordCollision(t *testing.T) {
	path := filepath.Join(t.TempDir(), "password")
	fixture := iamResetFixture(func(w http.ResponseWriter, _ *http.Request) {
		if err := os.WriteFile(path, []byte("existing"), 0o600); err != nil {
			t.Error(err)
		}
		w.WriteHeader(204)
	})
	root, stdout, _ := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "iam", "reset-user-password", "--user-id", "user-1", "--secret-file", path, "--yes"})
	err := root.Execute()
	data, readErr := os.ReadFile(path)
	if err == nil || classify(err).Code != "SecretFileFailed" || stdout.Len() != 0 || fixture.requestCount() != 6 || readErr != nil || string(data) != "existing" {
		t.Fatal("collision overwrote or removed existing file")
	}
}

func TestIAMResetPasswordGoldenOutput(t *testing.T) {
	for _, format := range []string{"json", "table", "text"} {
		checkGolden(t, "iam-reset-user-password-generated-"+format+".golden", format, "", &iamResetUserPasswordOutput{SecretFile: "/private/password"})
		checkGolden(t, "iam-reset-user-password-supplied-"+format+".golden", format, "", &struct{}{})
	}
}

func TestIAMResetPasswordGuardPrivacy(t *testing.T) {
	for _, edge := range []string{
		"/accounts-api/v1/auth/userinfo", "/accounts-api/v1/iam-users/user-1",
		"/policies-api/v1/actions", "/policies-api/v1/user-attachments/iam-users/user-1/policies",
		"/policies-api/v1/user-attachments/iam-users/user-1/groups",
	} {
		t.Run(edge, func(t *testing.T) {
			posts := 0
			fixture := iamResetFixture(func(w http.ResponseWriter, _ *http.Request) { posts++; w.WriteHeader(204) })
			original := fixture.mux
			fixture.mux = http.NewServeMux()
			fixture.mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == edge {
					w.WriteHeader(403)
					_, _ = io.WriteString(w, `{"code":"synthetic-input","message":"{\\"password\\":\\"synthetic-input\\"}"}`)
					return
				}
				original.ServeHTTP(w, r)
			})
			root, stdout, stderr := newSvcRoot(t, fixture)
			root.SetIn(strings.NewReader("synthetic-input"))
			root.SetArgs([]string{"--region", "hcm-3", "--debug", "iam", "reset-user-password", "--user-id", "user-1", "--password-file", "-", "--yes"})
			err := root.Execute()
			if err == nil || posts != 0 || stdout.Len() != 0 || strings.Contains(stderr.String(), "synthetic-input") {
				t.Fatal("unsafe guard failure")
			}
			assertIAMResetErrorPrivate(t, err, "synthetic-input")
		})
	}
}

func TestIAMResetPasswordSavedAfterRenderingFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "password")
	fixture := iamResetFixture(jsonHandler(204, ""))
	root, stdout, _ := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "--query", "sort(SecretFile)", "iam", "reset-user-password", "--user-id", "user-1", "--secret-file", path, "--yes"})
	err := root.Execute()
	if err == nil || classify(err).Code != "QueryFailed" || stdout.Len() != 0 || fixture.requestCount() != 6 {
		t.Fatalf("expected rendering failure: %v", err)
	}
	data, readErr := os.ReadFile(path)
	if readErr != nil || len(data) != 33 {
		t.Fatal("rendering failure lost saved password")
	}
	assertIAMResetErrorPrivate(t, err, strings.TrimSuffix(string(data), "\n"))
}

func TestIAMResetPasswordJSONFileExclusion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "input.json")
	if err := os.WriteFile(path, []byte(`{"UserID":"user-1","Password":"synthetic-input"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	fixture := newSvcFixture(nil)
	root, _, _ := newSvcRoot(t, fixture)
	root.SetIn(iamUnreadableStdin{t})
	root.SetArgs([]string{"iam", "reset-user-password", "--secret-file", filepath.Join(t.TempDir(), "password"), "--cli-input-json", "file://" + path, "--yes"})
	err := root.Execute()
	if err == nil || exitCode(err) != 2 || fixture.requestCount() != 0 {
		t.Fatal("JSON password accepted")
	}
	assertIAMResetErrorPrivate(t, err, "synthetic-input")
}
