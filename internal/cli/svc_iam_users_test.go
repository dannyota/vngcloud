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
	"time"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/iam"
)

func TestIAMUserOutcomePrecedence(t *testing.T) {
	for _, outcome := range []error{iam.ErrCreateUnconfirmed, iam.ErrUserCreateUncertain} {
		for _, cause := range []error{vngcloud.ErrAuth, vngcloud.ErrNotFound, context.Canceled} {
			err := errors.Join(outcome, cause)
			if exitCode(err) != 1 || classify(err).Code != "RequestFailed" {
				t.Fatalf("wrong outcome mapping: %v", err)
			}
		}
	}
}

func TestIAMUserPasswordFlagsBeforeJSON(t *testing.T) {
	for _, args := range [][]string{
		{"--cli-input-json", "file:///missing-input"},
		{"--password-file", "-", "--cli-input-json", "file:///missing-input"},
	} {
		var out bytes.Buffer
		root := newRootCmd(bytes.NewReader(nil), &out, &out)
		root.SetArgs(append([]string{"iam", "create-user"}, args...))
		err := root.Execute()
		if err == nil || exitCode(err) != 2 {
			t.Fatalf("expected usage error: %v", err)
		}
		if bytes.Contains([]byte(err.Error()), []byte("missing-input")) {
			t.Fatal("JSON read preceded password flag check")
		}
	}
}

func TestIAMUserCreateModesAndOutcomes(t *testing.T) {
	for _, supplied := range []bool{false, true} {
		for _, outcome := range []string{"success", "unconfirmed", "uncertain", "collision", "rejected"} {
			t.Run(fmt.Sprintf("supplied=%t/%s", supplied, outcome), func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "password")
				canary := "synthetic password input"
				var sentPassword string
				posts := 0
				fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
					"/accounts-api/v1/iam-users": func(w http.ResponseWriter, r *http.Request) {
						posts++
						if r.Method != http.MethodPost {
							t.Error("unexpected method")
						}
						var body struct{ Username, Password string }
						if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
							t.Error(err)
						}
						sentPassword = body.Password
						if body.Username != "new-user" || sentPassword == "" || (supplied && sentPassword != canary) {
							t.Error("wrong create body")
						}
						if outcome == "collision" {
							if err := os.WriteFile(path, []byte("existing"), 0o600); err != nil {
								t.Error(err)
							}
						}
						switch outcome {
						case "uncertain":
							w.WriteHeader(500)
							_, _ = fmt.Fprintf(w, `{"message":%q}`, sentPassword)
						case "rejected":
							w.WriteHeader(400)
							_, _ = fmt.Fprintf(w, `{"code":%q,"message":%q}`, sentPassword, sentPassword)
						default:
							w.WriteHeader(201)
							_, _ = w.Write([]byte(`{"id":"user-2"}`))
						}
					},
					"/accounts-api/v1/iam-users/user-2": func(w http.ResponseWriter, r *http.Request) {
						if r.Method != http.MethodGet {
							t.Error("cloud cleanup attempted")
						}
						if outcome == "unconfirmed" {
							w.WriteHeader(404)
							return
						}
						_, _ = w.Write([]byte(`{"id":"user-2","username":"new-user","from":"iam","enabled":true}`))
					},
				})
				root, stdout, stderr := newSvcRoot(t, fixture)
				args := []string{"--region", "hcm-3", "--debug", "iam", "create-user", "--username", "new-user"}
				if supplied {
					root.SetIn(strings.NewReader(canary + "\r\n"))
					args = append(args, "--password-file", "-")
				} else {
					args = append(args, "--secret-file", path)
				}
				root.SetArgs(args)
				err := root.Execute()
				wantErr := outcome != "success" && (!supplied || outcome != "collision")
				if (err != nil) != wantErr {
					t.Fatalf("unexpected result: %v", err)
				}
				if posts != 1 {
					t.Fatalf("POST count = %d", posts)
				}
				if sentPassword == "" || strings.Contains(stdout.String()+stderr.String(), sentPassword) {
					t.Fatal("password leaked or not sent")
				}
				if err != nil {
					if strings.Contains(err.Error(), sentPassword) || stdout.Len() != 0 {
						t.Fatal("password or partial object leaked")
					}
					if outcome == "unconfirmed" || outcome == "uncertain" {
						if exitCode(err) != 1 {
							t.Fatal("wrong outcome exit")
						}
					}
				}
				if !supplied && outcome != "rejected" {
					data, readErr := os.ReadFile(path)
					if readErr != nil {
						t.Fatal(readErr)
					}
					if outcome == "collision" {
						if string(data) != "existing" || classify(err).Code != "SecretFileFailed" || !strings.Contains(err.Error(), "delete-user --user-id user-2 --yes") {
							t.Fatal("wrong collision recovery")
						}
					} else if string(data) != sentPassword+"\n" {
						t.Fatal("generated password not saved")
					}
				}
				if supplied && strings.Contains(stdout.String(), "SecretFile") {
					t.Fatal("supplied mode printed secret path")
				}
			})
		}
	}
}

type iamUnreadableStdin struct{ t *testing.T }

func (r iamUnreadableStdin) Read([]byte) (int, error) {
	r.t.Error("stdin read before read-only refusal")
	return 0, errors.New("unexpected stdin read")
}

func TestIAMUserReadOnlyBeforeStdin(t *testing.T) {
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
			args := []string{"--region", "hcm-3", "iam", "create-user", "--username", "new-user", "--password-file", "-"}
			if source == "flag" {
				args = append(args, "--read-only")
			}
			root.SetArgs(args)
			err := root.Execute()
			if err == nil || classify(err).Code != "ReadOnly" {
				t.Fatalf("wrong refusal: %v", err)
			}
		})
	}
}

func TestIAMUserDeleteConsentAndReads(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/accounts-api/v1/auth/userinfo": jsonHandler(200, iamUserInfoJSON("caller-1", "iam-user")),
		"/accounts-api/v1/iam-users/user-2": func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodDelete {
				w.WriteHeader(204)
				return
			}
			_, _ = w.Write([]byte(`{"id":"user-2","username":"new-user","from":"iam"}`))
		},
		"/accounts-api/v1/iam-users": func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("name") != "new-user" {
				t.Error("missing name filter")
			}
			_, _ = w.Write([]byte(`{"data":[]}`))
		},
	})
	root, _, _ := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "iam", "delete-user", "--user-id", "user-2"})
	if err := root.Execute(); err == nil || fixture.requestCount() != 0 {
		t.Fatal("missing consent allowed request")
	}
	for _, args := range [][]string{
		{"delete-user", "--user-id", "user-2", "--yes"},
		{"get-user", "--user-id", "user-2"}, {"list-users", "--name", "new-user"},
	} {
		root, _, _ = newSvcRoot(t, fixture)
		root.SetArgs(append([]string{"--region", "hcm-3", "iam"}, args...))
		if err := root.Execute(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestIAMUserGoldenOutput(t *testing.T) {
	out := &iam.CreateUserOutput{User: iam.UserDetail{ID: "user-2", Username: "new-user", From: "iam"}, CreatedID: "user-2"}
	for _, format := range []string{"json", "table", "text"} {
		checkGolden(t, "iam-create-user-"+format+".golden", format, "", out)
		checkGolden(t, "iam-get-user-"+format+".golden", format, "User", &iam.GetUserOutput{User: out.User})
	}
	checkGolden(t, "iam-delete-user-json.golden", "json", "", &iam.DeleteUserOutput{})
}

func TestIAMUserCreateFlagsRefused(t *testing.T) {
	for _, args := range [][]string{
		{}, {"--secret-file", "new", "--password-file", "-"},
		{"--password-file", "-", "--password-length", "0"},
		{"--password-file", "-", "--cli-input-json", `{"Password":"synthetic-input"}`},
		{"--password", "synthetic-input"},
	} {
		var out bytes.Buffer
		root := newRootCmd(iamUnreadableStdin{t}, &out, &out)
		root.SetArgs(append([]string{"iam", "create-user", "--username", "new-user"}, args...))
		if err := root.Execute(); err == nil || exitCode(err) != 2 || strings.Contains(err.Error(), "synthetic-input") {
			t.Fatal("invalid password flags accepted or leaked")
		}
	}
}

func TestIAMUserCreateStatuses(t *testing.T) {
	for _, status := range []int{400, 401, 403, 409, 500} {
		fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
			"/accounts-api/v1/iam-users": jsonHandler(status, `{"message":"synthetic-input","code":"synthetic-input"}`),
		})
		root, stdout, _ := newSvcRoot(t, fixture)
		root.SetIn(strings.NewReader("synthetic-input"))
		root.SetArgs([]string{"--region", "hcm-3", "iam", "create-user", "--username", "new-user", "--password-file", "-"})
		err := root.Execute()
		if err == nil || stdout.Len() != 0 || strings.Contains(err.Error(), "synthetic-input") || fixture.requestCount() != 1 {
			t.Fatal("unsafe create status behavior")
		}
		if status == 500 && (!errors.Is(err, iam.ErrUserCreateUncertain) || exitCode(err) != 1) {
			t.Fatal("lost uncertain outcome")
		}
	}
}

func TestIAMUserSharedStdinRefusedBeforeRead(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close(); _ = w.Close() }()
	fixture := newSvcFixture(nil)
	root, _, _ := newSvcRoot(t, fixture)
	root.SetIn(r)
	root.SetArgs([]string{"iam", "create-user", "--username", "new-user", "--password-file", "-", "--cli-input-json", "file:///missing"})
	done := make(chan error, 1)
	go func() { done <- root.Execute() }()
	select {
	case err := <-done:
		if err == nil || exitCode(err) != 2 || fixture.requestCount() != 0 {
			t.Fatal("shared stdin accepted")
		}
	case <-time.After(time.Second):
		t.Fatal("shared stdin stalled")
	}
}

type iamStartedStdin struct {
	reader  io.Reader
	started chan struct{}
}

func (r *iamStartedStdin) Read(p []byte) (int, error) {
	select {
	case <-r.started:
	default:
		close(r.started)
	}
	return r.reader.Read(p)
}

func TestIAMUserStalledCommandCancellation(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close(); _ = w.Close() }()
	fixture := newSvcFixture(nil)
	root, _, _ := newSvcRoot(t, fixture)
	started := make(chan struct{})
	root.SetIn(&iamStartedStdin{reader: r, started: started})
	root.SetArgs([]string{"--region", "hcm-3", "iam", "create-user", "--username", "new-user", "--password-file", "-"})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- root.ExecuteContext(ctx) }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("password input never started")
	}
	cancel()
	select {
	case err := <-done:
		if err == nil || fixture.requestCount() != 0 {
			t.Fatal("cancellation sent a request")
		}
	case <-time.After(time.Second):
		t.Fatal("command cancellation stalled")
	}
}

func TestIAMUserSavedPasswordSurvivesRenderingFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "password")
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/accounts-api/v1/iam-users":        jsonHandler(201, `{"id":"user-2"}`),
		"/accounts-api/v1/iam-users/user-2": jsonHandler(200, `{"id":"user-2","username":"new-user","from":"iam"}`),
	})
	root, stdout, _ := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "--query", "sort(User)", "iam", "create-user", "--username", "new-user", "--secret-file", path})
	err := root.Execute()
	if err == nil || classify(err).Code != "QueryFailed" || stdout.Len() != 0 {
		t.Fatal("expected rendering failure")
	}
	data, readErr := os.ReadFile(path)
	if readErr != nil || len(data) != 33 {
		t.Fatal("rendering failure lost generated password")
	}
	if strings.Contains(err.Error(), strings.TrimSuffix(string(data), "\n")) {
		t.Fatal("password leaked through rendering error")
	}
}
