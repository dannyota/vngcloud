package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/core"
)

func TestServiceAccountConfigureRoundTrip(t *testing.T) {
	home := withCleanEnv(t)
	for _, item := range [][3]string{{"region", "hcm-3", ""}, {"client_id", "synthetic-client", ""}, {"client_secret", "-", "synthetic-secret\r\n"}} {
		if _, err := runConfigure(t, item[2], []string{"--profile", "machine", "configure", "set", item[0], item[1]}); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(credentialsPath(home))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "[machine]\n") || !strings.Contains(string(data), "client_secret = synthetic-secret\n") {
		t.Fatal("wrong credentials file")
	}
	info, err := os.Stat(credentialsPath(home))
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatal("unsafe credentials mode")
	}
	var sends int
	fixture, err := os.ReadFile("../../testdata/auth/service_account_token.json")
	if err != nil {
		t.Fatal(err)
	}
	rt := saCLITransport(func(r *http.Request) (*http.Response, error) {
		sends++
		id, secret, ok := r.BasicAuth()
		if !ok || id != "synthetic-client" || secret != "synthetic-secret" {
			t.Error("wrong credentials")
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(fixture))}, nil
	})
	// Loading must remain offline even with exchange dependencies installed.
	captures := 0
	var logs bytes.Buffer
	withTestOptions(t, core.WithServiceAccountTestTransport(rt, func() time.Time { return time.Unix(1700000000, 0) }), vngcloud.WithResponseCapture(func(vngcloud.ResponseCapture) { captures++ }))
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	cfg, err := loadConfig(context.Background(), &env{flags: &globalFlags{profile: "machine"}}, logger)
	if err != nil {
		t.Fatal(err)
	}
	if sends != 0 || core.ClientOf(cfg).UsesIAMUserLogin() {
		t.Fatal("loading authenticated or selected IAM")
	}
	if err := cfg.Authenticate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if sends != 1 {
		t.Fatal("wrong selected auth")
	}
	if captures != 0 {
		t.Fatal("exchange captured")
	}
	for _, marker := range []string{"synthetic-client", "synthetic-secret", "Basic ", "<secret>"} {
		if strings.Contains(logs.String(), marker) {
			t.Fatal("exchange debug leaked credentials")
		}
	}
	for _, args := range [][]string{{"configure", "get", "client_secret"}, {"configure", "list"}} {
		out, err := runConfigure(t, "", append([]string{"--profile", "machine"}, args...))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(out, "synthetic-secret") || !strings.Contains(out, "****") {
			t.Fatal("secret not masked")
		}
	}
	_, err = runConfigure(t, "", []string{"--profile", "machine", "configure", "set", "read_only", "1"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = runConfigure(t, "replacement-secret", []string{"--profile", "machine", "configure", "set", "client_secret", "-"})
	if err == nil {
		t.Fatal("rotation bypassed profile latch")
	}
	if classify(err).Code != "ReadOnly" || exitCode(err) != 2 {
		t.Fatalf("rotation bypassed latch: %v", err)
	}
}

type saCLITransport func(*http.Request) (*http.Response, error)

func (f saCLITransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestServiceAccountConfigureSecretInput(t *testing.T) {
	for _, tc := range []struct {
		name, input, value string
		want               bool
	}{
		{"argv", "", "synthetic-secret", false}, {"empty", "", "-", false},
		{"bound", strings.Repeat("x", 4096), "-", true}, {"over bound", strings.Repeat("x", 4097), "-", false},
		{"LF", "synthetic-secret\n", "-", true}, {"CRLF", "synthetic-secret\r\n", "-", true},
		{"two newlines", "synthetic-secret\n\n", "-", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := withCleanEnv(t)
			_, err := runConfigure(t, tc.input, []string{"configure", "set", "client_secret", tc.value})
			if (err == nil) != tc.want {
				t.Fatalf("unexpected input result: %v", err)
			}
			if err != nil {
				if exitCode(err) != 2 || strings.Contains(err.Error(), "synthetic-secret") {
					t.Fatal("unsafe usage error")
				}
				if _, statErr := os.Stat(credentialsPath(home)); !errors.Is(statErr, os.ErrNotExist) {
					t.Fatal("rejected input wrote file")
				}
			}
		})
	}
	withCleanEnv(t)
	prev := isTerminalOverride
	isTerminalOverride = func(any) bool { return true }
	t.Cleanup(func() { isTerminalOverride = prev })
	if _, err := runConfigure(t, "synthetic-secret", []string{"configure", "set", "client_secret", "-"}); err == nil {
		t.Fatal("terminal secret accepted")
	}
}

func TestServiceAccountErrorClasses(t *testing.T) {
	for _, tc := range []struct {
		err  error
		code string
		exit int
	}{
		{&vngcloud.ServiceAccountLoginError{Status: 401, Reason: "token endpoint rejected request", Err: vngcloud.ErrAuth}, "ServiceAccountLoginFailed", 3},
		{&vngcloud.ServiceAccountLoginError{Reason: "token request failed", Err: context.Canceled}, "ServiceAccountLoginFailed", 1},
		{&vngcloud.ServiceAccountLoginError{Reason: "token request failed", Err: context.DeadlineExceeded}, "ServiceAccountLoginFailed", 1},
		{vngcloud.ErrTokenCache, "TokenCacheFailed", 1}, {context.Canceled, "RequestFailed", 1}, {context.DeadlineExceeded, "RequestFailed", 1},
		{vngcloud.ErrNoCredentials, "NoCredentials", 3}, {vngcloud.ErrCredentialsFile, "NoCredentials", 3}, {vngcloud.ErrInvalidConfig, "InvalidConfig", 2},
		{&vngcloud.APIError{StatusCode: 401, Code: "Unauthorized", Err: vngcloud.ErrAuth}, "Unauthorized", 3},
		{&vngcloud.APIError{StatusCode: 403, Code: "Forbidden", Err: vngcloud.ErrPermission}, "Forbidden", 1},
	} {
		t.Run(tc.code+strconv.Itoa(tc.exit), func(t *testing.T) {
			err := fmt.Errorf("wrapped: %w", tc.err)
			if got := classify(err).Code; got != tc.code {
				t.Fatalf("code=%s want %s", got, tc.code)
			}
			if got := exitCode(err); got != tc.exit {
				t.Fatalf("exit=%d want %d", got, tc.exit)
			}
			var buf bytes.Buffer
			printError(&buf, err)
			if !strings.Contains(buf.String(), tc.code) {
				t.Fatal("stderr lost error class")
			}
		})
	}
}

func TestServiceAccountCachePreflightSelection(t *testing.T) {
	for _, kind := range []string{"service options", "service env", "service profile", "IAM options", "static options", "provider options", "static env wins", "explicit profile skips env"} {
		t.Run(kind, func(t *testing.T) {
			home := withCleanEnv(t)
			cache := filepath.Join(home, ".vngcloud", "cache")
			if err := os.MkdirAll(filepath.Dir(cache), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(cache, []byte("unsafe-marker"), 0o600); err != nil {
				t.Fatal(err)
			}
			writeConfigFile(t, home, "[profile dev]\nregion = hcm-3\n")
			writeCredentialsFile(t, home, "[dev]\nclient_id = file-id\nclient_secret = file-secret\n")
			flags := &globalFlags{region: "hcm-3"}
			var opts []vngcloud.LoadOption
			switch kind {
			case "service options":
				opts = append(opts, vngcloud.WithServiceAccount(&vngcloud.ServiceAccountAuth{ClientID: "id", ClientSecret: "secret"}))
			case "service profile":
				flags.profile = "dev"
			case "IAM options":
				opts = append(opts, vngcloud.WithIAMUser(&vngcloud.IAMUserAuth{RootEmail: "root", Username: "user", Password: "password"}))
			case "static options":
				opts = append(opts, vngcloud.WithStaticToken("token"))
			case "provider options":
				opts = append(opts, vngcloud.WithCredentialsProvider(saCLIProvider{}))
			default:
				t.Setenv("VNGCLOUD_CLIENT_ID", "env-id")
				t.Setenv("VNGCLOUD_CLIENT_SECRET", "env-secret")
			}
			if kind == "static env wins" {
				t.Setenv("VNGCLOUD_ACCESS_TOKEN", "token")
			}
			if kind == "explicit profile skips env" {
				flags.profile = "dev"
				writeCredentialsFile(t, home, "[dev]\nroot_email = root\nusername = user\npassword = password\n")
			}
			withTestOptions(t, opts...)
			_, err := loadConfig(context.Background(), &env{flags: flags}, nil)
			want := vngcloud.ErrInvalidConfig
			if strings.HasPrefix(kind, "service ") {
				want = vngcloud.ErrTokenCache
			}
			if !errors.Is(err, want) {
				t.Fatalf("err=%v want %v", err, want)
			}
			if errors.Is(err, vngcloud.ErrTokenCache) && err != vngcloud.ErrTokenCache { //nolint:errorlint // Cache failures must return the bare sentinel.
				t.Fatal("cache failure wrapped")
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			_, err = loadConfig(ctx, &env{flags: flags}, nil)
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("context lost: %v", err)
			}
		})
	}
}

func TestServiceAccountConfigurePromptOrder(t *testing.T) {
	want := []string{"region", "project_id", "output", "read_only", "root_email", "username", "password", "totp_secret"}
	if !reflect.DeepEqual(configureKeyOrder, want) {
		t.Fatal("interactive prompts changed")
	}
}

type saCLIProvider struct{}

func (saCLIProvider) Token(context.Context) (vngcloud.Token, error) {
	return vngcloud.Token{}, errors.New("provider must not be called while loading")
}
func (saCLIProvider) Invalidate(string) {}

func TestServiceAccountConfigureInlineJSONRefused(t *testing.T) {
	home := withCleanEnv(t)
	_, err := runConfigure(t, "", []string{"--cli-input-json", `{"client_secret":"synthetic-inline-marker"}`, "configure", "set", "client_secret", "-"})
	if err == nil || exitCode(err) != 2 || strings.Contains(err.Error(), "synthetic-inline-marker") {
		t.Fatal("inline JSON supplied a secret")
	}
	if _, err := os.Stat(credentialsPath(home)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("inline JSON wrote credentials")
	}
}

func TestServiceAccountLoadErrorIdentity(t *testing.T) {
	for _, kind := range []string{"partial options", "shape options", "config key", "unsafe credentials", "no home"} {
		t.Run(kind, func(t *testing.T) {
			home := withCleanEnv(t)
			opts := []vngcloud.LoadOption{vngcloud.WithRegion("hcm-3")}
			wantCode, wantExit := "NoCredentials", 3
			switch kind {
			case "partial options":
				opts = append(opts, vngcloud.WithServiceAccount(&vngcloud.ServiceAccountAuth{ClientID: "synthetic-client"}))
			case "shape options":
				opts = append(opts, vngcloud.WithServiceAccount(&vngcloud.ServiceAccountAuth{ClientID: "bad:id", ClientSecret: "synthetic-secret"}))
				wantCode, wantExit = "InvalidConfig", 2
			case "config key":
				writeConfigFile(t, home, "[default]\nclient_secret = synthetic-secret\n")
				opts = append(opts, vngcloud.WithStaticToken("token"))
				wantCode, wantExit = "InvalidConfig", 2
			case "unsafe credentials":
				if runtime.GOOS == "windows" {
					t.Skip("Windows retains its mode-bit exception")
				}
				writeCredentialsFile(t, home, "[default]\nclient_id = id\nclient_secret = synthetic-secret\n")
				if err := os.Chmod(credentialsPath(home), 0o644); err != nil { //nolint:gosec // Deliberately unsafe synthetic fixture tests credentials-file refusal.
					t.Fatal(err)
				}
			case "no home":
				t.Setenv("HOME", "")
				t.Setenv("USERPROFILE", "")
				opts = append(opts, vngcloud.WithServiceAccount(&vngcloud.ServiceAccountAuth{ClientID: "id", ClientSecret: "secret"}))
			}
			withTestOptions(t, opts...)
			_, err := loadConfig(context.Background(), &env{flags: &globalFlags{}}, nil)
			if kind == "no home" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || classify(err).Code != wantCode || exitCode(err) != wantExit {
				t.Fatalf("wrong loader identity: %v", err)
			}
			if strings.Contains(err.Error(), "synthetic-secret") {
				t.Fatal("loader error leaked secret")
			}
		})
	}
}
