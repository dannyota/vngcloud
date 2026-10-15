package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/iam"
)

func TestIAMUserMFALocalRefusals(t *testing.T) {
	for _, op := range []string{"enable-user-mfa", "disable-user-mfa"} {
		for _, tc := range []struct {
			name   string
			args   []string
			source string
		}{
			{name: "consent", args: []string{"--user-id", "user-2", "--method", "google"}},
			{name: "user", args: []string{"--yes", "--method", "google"}},
			{name: "method", args: []string{"--yes", "--user-id", "user-2"}},
			{name: "invalid-id", args: []string{"--yes", "--user-id", "../x", "--method", "google"}},
			{name: "email", args: []string{"--yes", "--user-id", "user-2", "--method", "email"}},
			{name: "case", args: []string{"--yes", "--user-id", "user-2", "--method", "Google"}},
			{name: "flag", source: "flag"}, {name: "environment", source: "environment"}, {name: "profile", source: "profile"},
		} {
			t.Run(op+"/"+tc.name, func(t *testing.T) {
				fixture := newSvcFixture(nil)
				root, stdout, _ := newSvcRoot(t, fixture)
				args := tc.args
				if tc.source != "" {
					args = []string{"--yes", "--user-id", "user-2", "--method", "google"}
					switch tc.source {
					case "flag":
						args = append(args, "--read-only")
					case "environment":
						t.Setenv("VNGCLOUD_READ_ONLY", "true")
						args = append(args, "--read-only=false")
					case "profile":
						path := filepath.Join(t.TempDir(), "config")
						if err := os.WriteFile(path, []byte("[default]\nread_only = true\n"), 0o600); err != nil {
							t.Fatal(err)
						}
						t.Setenv("VNGCLOUD_CONFIG_FILE", path)
						args = append(args, "--read-only=false")
					}
				}
				root.SetArgs(append([]string{"--region", "hcm-3", "iam", op}, args...))
				err := root.Execute()
				if err == nil || exitCode(err) != 2 || fixture.requestCount() != 0 || stdout.Len() != 0 {
					t.Fatalf("refusal: %v requests=%d", err, fixture.requestCount())
				}
				if tc.source != "" && classify(err).Code != "ReadOnly" {
					t.Fatalf("wrong read-only refusal: %v", err)
				}
			})
		}
	}
}

// Factor state and enrolled responses here are synthetic.
func cliMFAUser(verified, enabled bool) string {
	return fmt.Sprintf(`{"id":"user-2","from":"iam","twoFactorAuth":{"googleAuthenticator":{"verified":%t,"enabled":%t}}}`, verified, enabled)
}

func TestIAMUserMFACommands(t *testing.T) {
	for _, enable := range []bool{false, true} {
		op, suffix := "disable-user-mfa", "deactivate-google"
		if enable {
			op, suffix = "enable-user-mfa", "activate-google"
		}
		for _, outcome := range []string{"changed", "noop", "unknown", "unverified", "self", "protected", "origin", "uncertain", "confirm-401", "confirm-404", "rejected"} {
			for _, format := range []string{"json", "table", "text"} {
				t.Run(op+"/"+outcome+"/"+format, func(t *testing.T) {
					posts, targets := 0, 0
					caller := iamUserInfoJSON("caller-1", "iam-user")
					if outcome == "self" {
						caller = iamUserInfoJSON("user-2", "iam-user")
					}
					policies := iamNoPoliciesJSON
					if outcome == "protected" {
						policies = `{"data":[{"id":"policy-1"}],"totalItems":1}`
					}
					fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
						"/accounts-api/v1/auth/userinfo":                              jsonHandler(200, caller),
						"/policies-api/v1/actions":                                    jsonHandler(200, iamWriteActionsJSON),
						"/policies-api/v1/user-attachments/iam-users/user-2/policies": jsonHandler(200, policies),
						"/policies-api/v1/user-attachments/iam-users/user-2/groups":   jsonHandler(200, `[]`),
						"/policies-api/v1/policies/policy-1":                          jsonHandler(200, `{"id":"policy-1","statements":[{"effect":"allow","actions":["iam:CreatePolicy"]}]}`),
						"/accounts-api/v1/iam-users/user-2": func(w http.ResponseWriter, r *http.Request) {
							targets++
							if posts > 0 && strings.HasPrefix(outcome, "confirm-") {
								if outcome == "confirm-401" {
									w.WriteHeader(401)
								} else {
									w.WriteHeader(404)
								}
								_, _ = io.WriteString(w, `{"message":"hostile-canary test-token"}`)
								return
							}
							state := !enable
							if outcome == "noop" || posts > 0 {
								state = enable
							}
							body := cliMFAUser(outcome != "unverified", state)
							if outcome == "unknown" {
								body = `{"id":"user-2","from":"iam"}`
							}
							if outcome == "origin" {
								body = `{"id":"user-2","from":"idp"}`
							}
							_, _ = io.WriteString(w, body)
						},
						"/accounts-api/v1/iam-users/user-2/" + suffix: func(w http.ResponseWriter, r *http.Request) {
							posts++
							body, err := io.ReadAll(r.Body)
							if err != nil || len(body) != 0 || r.Method != http.MethodPost {
								t.Error("wrong MFA request")
							}
							status := 204
							if outcome == "uncertain" {
								status = 500
							}
							if outcome == "rejected" {
								status = 403
							}
							w.WriteHeader(status)
							if status != 204 {
								_, _ = io.WriteString(w, `{"code":"hostile-canary test-token","message":"hostile-canary test-token"}`)
							}
						},
					})
					root, stdout, stderr := newSvcRoot(t, fixture)
					root.SetArgs([]string{"--region", "hcm-3", "--debug", "--output", format, "iam", op, "--yes", "--cli-input-json", `{"UserID":"user-2","Method":"google"}`})
					err := root.Execute()
					success := outcome == "changed" || outcome == "noop" || outcome == "unverified" && !enable
					if (err == nil) != success {
						t.Fatalf("result: %v", err)
					}
					wantPosts := 0
					if outcome == "changed" || outcome == "uncertain" || outcome == "rejected" || strings.HasPrefix(outcome, "confirm-") || outcome == "unverified" && !enable {
						wantPosts = 1
					}
					if posts != wantPosts {
						t.Fatalf("posts=%d want=%d", posts, wantPosts)
					}
					all := stdout.String() + stderr.String()
					if err != nil {
						all += err.Error()
						if stdout.Len() != 0 {
							t.Fatal("partial output printed")
						}
					}
					if strings.Contains(all, "hostile-canary") || strings.Contains(all, "test-token") {
						t.Fatal("canary leaked")
					}
					if success {
						var expected bytes.Buffer
						if e := renderOutput(&expected, format, "", &iam.DisableUserMFAOutput{Method: "google", Enabled: vngcloud.Ptr(enable), Changed: posts == 1}, true); e != nil {
							t.Fatal(e)
						}
						if stdout.String() != expected.String() {
							t.Fatalf("output=%q want=%q", stdout.String(), expected.String())
						}
					}
					switch outcome {
					case "self":
						if !errors.Is(err, iam.ErrSelfChange) || classify(err).Code != "SelfChange" {
							t.Fatal(err)
						}
					case "protected":
						if !errors.Is(err, iam.ErrPrivilegedChange) || classify(err).Code != "PrivilegedChange" {
							t.Fatal(err)
						}
					case "origin":
						if !errors.Is(err, iam.ErrOriginNotIAM) || classify(err).Code != "RequestFailed" {
							t.Fatal(err)
						}
					case "unknown":
						if classify(err).Code != "RequestFailed" || exitCode(err) != 1 {
							t.Fatal(err)
						}
					case "uncertain":
						if !errors.Is(err, iam.ErrUserWriteUncertain) || exitCode(err) != 1 {
							t.Fatal(err)
						}
					case "confirm-401", "confirm-404":
						if !errors.Is(err, iam.ErrNotSettled) || exitCode(err) != 1 || classify(err).Code != "NotSettled" || targets < 2 {
							t.Fatal(err)
						}
					}
				})
			}
		}
	}
}

func TestIAMUserMFAGoldenOutput(t *testing.T) {
	for _, enable := range []bool{false, true} {
		op := "disable"
		if enable {
			op = "enable"
		}
		for _, format := range []string{"json", "table", "text"} {
			checkGolden(t, "iam-"+op+"-user-mfa-"+format+".golden", format, "", &iam.DisableUserMFAOutput{Method: "google", Enabled: vngcloud.Ptr(enable), Changed: true})
		}
	}
}

func TestIAMUserMFAOutcomeIdentity(t *testing.T) {
	for _, outcome := range []error{iam.ErrNotSettled, iam.ErrUserWriteUncertain} {
		for _, cause := range []error{&vngcloud.APIError{StatusCode: 401}, &vngcloud.APIError{StatusCode: 404}, context.Canceled} {
			err := errors.Join(outcome, cause)
			want := "RequestFailed"
			if errors.Is(outcome, iam.ErrNotSettled) {
				want = "NotSettled"
			}
			if exitCode(err) != 1 || classify(err).Code != want || !errors.Is(err, outcome) {
				t.Fatalf("lost outcome: %v", err)
			}
		}
	}
}

func TestIAMUserMFAReadNullableOutput(t *testing.T) {
	for _, tc := range []struct{ body, want string }{
		{`{"id":"user-2"}`, "null\n"},
		{`{"id":"user-2","twoFactorAuth":{"googleAuthenticator":{}}}`, "null\n"},
		{cliMFAUser(false, false), "false\n"},
	} {
		fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
			"/accounts-api/v1/iam-users/user-2": jsonHandler(200, tc.body),
		})
		root, stdout, _ := newSvcRoot(t, fixture)
		root.SetArgs([]string{"--region", "hcm-3", "--query", "User.MFA.GoogleAuthenticator.Enabled", "iam", "get-user", "--user-id", "user-2"})
		if err := root.Execute(); err != nil {
			t.Fatal(err)
		}
		if stdout.String() != tc.want {
			t.Fatalf("nullable output=%q want=%q", stdout.String(), tc.want)
		}
	}
}
