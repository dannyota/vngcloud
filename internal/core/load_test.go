package core

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

var allVNGCloudEnvVars = []string{
	"VNGCLOUD_PROFILE",
	"VNGCLOUD_REGION",
	"VNGCLOUD_PROJECT_ID",
	"VNGCLOUD_ROOT_EMAIL",
	"VNGCLOUD_USERNAME",
	"VNGCLOUD_PASSWORD",
	"VNGCLOUD_TOTP_SECRET",
	"VNGCLOUD_ACCESS_TOKEN",
	"VNGCLOUD_CONFIG_FILE",
	"VNGCLOUD_SHARED_CREDENTIALS_FILE",
}

// setupHome clears every VNGCLOUD_* environment variable (an empty value
// counts as unset, same as truly unset) and points HOME and USERPROFILE at
// a fresh temp directory, so LoadConfig never sees the real home directory
// or a value left over from the host environment. It returns that directory.
func setupHome(t *testing.T) string {
	t.Helper()
	for _, key := range allVNGCloudEnvVars {
		t.Setenv(key, "")
	}
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
	return dir
}

func writeFile(t *testing.T, path, content string, perm os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), perm); err != nil {
		t.Fatal(err)
	}
}

func configPath(home string) string      { return filepath.Join(home, ".vngcloud", "config") }
func credentialsPath(home string) string { return filepath.Join(home, ".vngcloud", "credentials") }

// capturedLogin records the credentials a fake login flow actually
// received, so a test can confirm which credential source LoadConfig chose
// without reaching into a field on another package's unexported type.
type capturedLogin struct {
	mu        sync.Mutex
	rootEmail string
	username  string
	password  string
}

func (c *capturedLogin) snapshot() (rootEmail, username, password string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.rootEmail, c.username, c.password
}

// capturingLoginServer stands up signin and token httptest servers that
// complete the login flow and record the rootEmail query parameter and the
// username/password form fields the flow sent.
func capturingLoginServer(t *testing.T) (signinURL, dashboardURL string, got *capturedLogin) {
	t.Helper()
	got = &capturedLogin{}
	var tokenURLRef string
	signin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			got.mu.Lock()
			got.rootEmail = r.URL.Query().Get("rootEmail")
			got.mu.Unlock()
			_, _ = w.Write([]byte(`<html><input name="_csrf" value="csrf"></html>`))
		case http.MethodPost:
			if err := r.ParseForm(); err == nil {
				got.mu.Lock()
				got.username = r.PostFormValue("username")
				got.password = r.PostFormValue("password")
				got.mu.Unlock()
			}
			http.Redirect(w, r, tokenURLRef+"/callback?code=auth-code", http.StatusFound)
		}
	}))
	t.Cleanup(signin.Close)

	token := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"accessToken":"tok-1","expiresIn":3600}`))
	}))
	t.Cleanup(token.Close)
	tokenURLRef = token.URL

	return signin.URL, token.URL + "/", got
}

// ---- Region precedence ----

func TestLoadConfigRegionOptionWins(t *testing.T) {
	home := setupHome(t)
	writeFile(t, configPath(home), "[default]\nregion = file-region\n", 0o600)
	t.Setenv("VNGCLOUD_REGION", "env-region")
	cfg, err := LoadConfig(context.Background(), WithRegion("option-region"), WithStaticToken("tok"))
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if cfg.Region() != "option-region" {
		t.Fatalf("Region() = %q, want option-region", cfg.Region())
	}
}

func TestLoadConfigRegionEnvWinsOverProfile(t *testing.T) {
	home := setupHome(t)
	writeFile(t, configPath(home), "[default]\nregion = file-region\n", 0o600)
	t.Setenv("VNGCLOUD_REGION", "env-region")
	cfg, err := LoadConfig(context.Background(), WithStaticToken("tok"))
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if cfg.Region() != "env-region" {
		t.Fatalf("Region() = %q, want env-region", cfg.Region())
	}
}

func TestLoadConfigRegionFromProfile(t *testing.T) {
	home := setupHome(t)
	writeFile(t, configPath(home), "[default]\nregion = file-region\n", 0o600)
	cfg, err := LoadConfig(context.Background(), WithStaticToken("tok"))
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if cfg.Region() != "file-region" {
		t.Fatalf("Region() = %q, want file-region", cfg.Region())
	}
}

func TestLoadConfigRegionMissingIsInvalidConfigNotCredentials(t *testing.T) {
	setupHome(t)
	_, err := LoadConfig(context.Background(), WithStaticToken("tok"))
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("err = %v, want ErrInvalidConfig", err)
	}
	if errors.Is(err, ErrNoCredentials) {
		t.Fatal("a missing region must not be reported as a credentials error")
	}
}

// ---- Project ID precedence ----

func TestLoadConfigProjectIDOptionWins(t *testing.T) {
	home := setupHome(t)
	writeFile(t, configPath(home), "[default]\nregion = r\nproject_id = file-project\n", 0o600)
	t.Setenv("VNGCLOUD_PROJECT_ID", "env-project")
	cfg, err := LoadConfig(context.Background(), WithProjectID("option-project"), WithStaticToken("tok"))
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if got := ClientOf(cfg).ProjectID(); got != "option-project" {
		t.Fatalf("ProjectID() = %q, want option-project", got)
	}
}

func TestLoadConfigProjectIDEnvWinsOverProfile(t *testing.T) {
	home := setupHome(t)
	writeFile(t, configPath(home), "[default]\nregion = r\nproject_id = file-project\n", 0o600)
	t.Setenv("VNGCLOUD_PROJECT_ID", "env-project")
	cfg, err := LoadConfig(context.Background(), WithStaticToken("tok"))
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if got := ClientOf(cfg).ProjectID(); got != "env-project" {
		t.Fatalf("ProjectID() = %q, want env-project", got)
	}
}

func TestLoadConfigProjectIDFromProfile(t *testing.T) {
	home := setupHome(t)
	writeFile(t, configPath(home), "[default]\nregion = r\nproject_id = file-project\n", 0o600)
	cfg, err := LoadConfig(context.Background(), WithStaticToken("tok"))
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if got := ClientOf(cfg).ProjectID(); got != "file-project" {
		t.Fatalf("ProjectID() = %q, want file-project", got)
	}
}

func TestLoadConfigProjectIDMissingIsFine(t *testing.T) {
	home := setupHome(t)
	writeFile(t, configPath(home), "[default]\nregion = r\n", 0o600)
	cfg, err := LoadConfig(context.Background(), WithStaticToken("tok"))
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if got := ClientOf(cfg).ProjectID(); got != "" {
		t.Fatalf("ProjectID() = %q, want empty", got)
	}
}

// ---- Credential set resolution ----

func TestLoadConfigCredentialSetsNeverMix(t *testing.T) {
	home := setupHome(t)
	writeFile(t, configPath(home), "[default]\nregion = r\n", 0o600)
	writeFile(t, credentialsPath(home),
		"[default]\nusername = file-user\npassword = file-pass\n", 0o600)
	// The environment sets only root_email; LoadConfig must not fill the
	// missing username/password from the profile file.
	t.Setenv("VNGCLOUD_ROOT_EMAIL", "env-root@example.test")

	_, err := LoadConfig(context.Background())
	if !errors.Is(err, ErrNoCredentials) {
		t.Fatalf("err = %v, want ErrNoCredentials (a partial set from one source must not be completed from another)", err)
	}
}

// TestLoadConfigPartialCredentialSetFromEnvNamesMissingKeys covers an env
// source that sets some IAM User keys but not all: LoadConfig must report it
// as ErrNoCredentials naming the environment and the missing keys, and never
// the value of a key that was set.
func TestLoadConfigPartialCredentialSetFromEnvNamesMissingKeys(t *testing.T) {
	home := setupHome(t)
	writeFile(t, configPath(home), "[default]\nregion = r\n", 0o600)
	t.Setenv("VNGCLOUD_USERNAME", "env-user")
	t.Setenv("VNGCLOUD_PASSWORD", "env-pass-secret")

	_, err := LoadConfig(context.Background())
	if !errors.Is(err, ErrNoCredentials) {
		t.Fatalf("err = %v, want ErrNoCredentials", err)
	}
	if !strings.Contains(err.Error(), "environment") {
		t.Fatalf("err = %v, want it to name the environment as the source", err)
	}
	if !strings.Contains(err.Error(), envRootEmail) {
		t.Fatalf("err = %v, want it to name the missing %s", err, envRootEmail)
	}
	if strings.Contains(err.Error(), "env-pass-secret") {
		t.Fatalf("err leaked a credential value: %v", err)
	}
}

// TestLoadConfigPartialCredentialSetFromProfileNamesMissingKeys is the same
// case from a profile's credentials section instead of the environment.
func TestLoadConfigPartialCredentialSetFromProfileNamesMissingKeys(t *testing.T) {
	home := setupHome(t)
	writeFile(t, configPath(home), "[default]\nregion = r\n", 0o600)
	writeFile(t, credentialsPath(home), "[default]\nusername = file-user\npassword = file-pass-secret\n", 0o600)

	_, err := LoadConfig(context.Background())
	if !errors.Is(err, ErrNoCredentials) {
		t.Fatalf("err = %v, want ErrNoCredentials", err)
	}
	if !strings.Contains(err.Error(), `"default"`) {
		t.Fatalf("err = %v, want it to name the profile", err)
	}
	if !strings.Contains(err.Error(), "root_email") {
		t.Fatalf("err = %v, want it to name the missing root_email key", err)
	}
	if strings.Contains(err.Error(), "file-pass-secret") {
		t.Fatalf("err leaked a credential value: %v", err)
	}
}

func TestLoadConfigEmptyEnvValueFallsThrough(t *testing.T) {
	home := setupHome(t)
	writeFile(t, configPath(home), "[default]\nregion = file-region\n", 0o600)
	t.Setenv("VNGCLOUD_REGION", "") // explicit empty, same as unset
	cfg, err := LoadConfig(context.Background(), WithStaticToken("tok"))
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if cfg.Region() != "file-region" {
		t.Fatalf("Region() = %q, want file-region", cfg.Region())
	}
}

func TestLoadConfigExplicitProfileIgnoresEnvCredentialsAndProjectID(t *testing.T) {
	home := setupHome(t)
	signinURL, dashboardURL, got := capturingLoginServer(t)

	writeFile(t, configPath(home), "[profile dev]\nregion = r\nproject_id = file-project\n", 0o600)
	writeFile(t, credentialsPath(home),
		"[dev]\nroot_email = file-root@example.test\nusername = file-user\npassword = file-pass\n", 0o600)

	t.Setenv("VNGCLOUD_ROOT_EMAIL", "env-root@example.test")
	t.Setenv("VNGCLOUD_USERNAME", "env-user")
	t.Setenv("VNGCLOUD_PASSWORD", "env-pass")
	t.Setenv("VNGCLOUD_PROJECT_ID", "env-project")

	cfg, err := LoadConfig(context.Background(), WithProfile("dev"),
		WithEndpointOverrides(EndpointOverrides{Signin: signinURL, Dashboard: dashboardURL}))
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if got := ClientOf(cfg).ProjectID(); got != "file-project" {
		t.Fatalf("ProjectID() = %q, want file-project (env must be skipped for an explicit profile)", got)
	}

	if err := cfg.Authenticate(context.Background()); err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	rootEmail, username, password := got.snapshot()
	if rootEmail != "file-root@example.test" || username != "file-user" || password != "file-pass" {
		t.Fatalf("login used rootEmail=%q username=%q password=%q, want the file's credentials, not the environment's",
			rootEmail, username, password)
	}
}

func TestLoadConfigVNGCloudProfileEnvVarIsNotExplicit(t *testing.T) {
	home := setupHome(t)
	writeFile(t, configPath(home), "[profile dev]\nregion = r\nproject_id = file-project\n", 0o600)
	writeFile(t, credentialsPath(home), "[dev]\nroot_email = a\nusername = b\npassword = c\n", 0o600)
	t.Setenv("VNGCLOUD_PROFILE", "dev")
	t.Setenv("VNGCLOUD_PROJECT_ID", "env-project")

	cfg, err := LoadConfig(context.Background())
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	// VNGCLOUD_PROFILE naming "dev" is not explicit, so env project ID still
	// wins over the profile file's project_id.
	if got := ClientOf(cfg).ProjectID(); got != "env-project" {
		t.Fatalf("ProjectID() = %q, want env-project (VNGCLOUD_PROFILE must not count as an explicit profile)", got)
	}
}

func TestLoadConfigExplicitProfileWithoutCredentialsIsErrNoCredentials(t *testing.T) {
	home := setupHome(t)
	writeFile(t, configPath(home), "[profile dev]\nregion = r\n", 0o600)
	t.Setenv("VNGCLOUD_ROOT_EMAIL", "env-root@example.test")
	t.Setenv("VNGCLOUD_USERNAME", "env-user")
	t.Setenv("VNGCLOUD_PASSWORD", "env-pass")

	_, err := LoadConfig(context.Background(), WithProfile("dev"))
	if !errors.Is(err, ErrNoCredentials) {
		t.Fatalf("err = %v, want ErrNoCredentials", err)
	}
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatal("ErrNoCredentials must also match ErrInvalidConfig")
	}
	if !strings.Contains(err.Error(), "dev") {
		t.Fatalf("err = %v, want it to name the profile", err)
	}
}

// ---- Section name conventions ----

func TestLoadConfigProfileSectionNames(t *testing.T) {
	home := setupHome(t)
	writeFile(t, configPath(home), "[profile dev]\nregion = dev-region\n", 0o600)
	writeFile(t, credentialsPath(home), "[dev]\nroot_email = a\nusername = b\npassword = c\n", 0o600)

	cfg, err := LoadConfig(context.Background(), WithProfile("dev"))
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if cfg.Region() != "dev-region" {
		t.Fatalf("Region() = %q, want dev-region", cfg.Region())
	}
}

func TestLoadConfigDefaultSectionNames(t *testing.T) {
	home := setupHome(t)
	writeFile(t, configPath(home), "[default]\nregion = default-region\n", 0o600)
	writeFile(t, credentialsPath(home), "[default]\nroot_email = a\nusername = b\npassword = c\n", 0o600)

	cfg, err := LoadConfig(context.Background())
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if cfg.Region() != "default-region" {
		t.Fatalf("Region() = %q, want default-region", cfg.Region())
	}
}

func TestLoadConfigNamedProfileNotFoundAnywhere(t *testing.T) {
	home := setupHome(t)
	writeFile(t, configPath(home), "[default]\nregion = r\n", 0o600)
	_, err := LoadConfig(context.Background(), WithProfile("ghost"))
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("err = %v, want ErrInvalidConfig", err)
	}
	if !strings.Contains(err.Error(), "ghost") {
		t.Fatalf("err = %v, want it to name the missing profile", err)
	}
}

// ---- File presence and safety ----

func TestLoadConfigMissingDefaultFilesAreFine(t *testing.T) {
	setupHome(t)
	_, err := LoadConfig(context.Background(), WithRegion("r"), WithStaticToken("tok"))
	if err != nil {
		t.Fatalf("LoadConfig() error = %v, want nil when default files are simply absent", err)
	}
}

func TestLoadConfigMissingExplicitConfigPathErrors(t *testing.T) {
	home := setupHome(t)
	missing := filepath.Join(home, "nope", "config")
	_, err := LoadConfig(context.Background(), WithConfigFile(missing), WithStaticToken("tok"))
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("err = %v, want ErrInvalidConfig", err)
	}
}

func TestLoadConfigMissingExplicitCredentialsPathErrors(t *testing.T) {
	home := setupHome(t)
	missing := filepath.Join(home, "nope", "credentials")
	_, err := LoadConfig(context.Background(), WithRegion("r"), WithSharedCredentialsFile(missing))
	if !errors.Is(err, ErrCredentialsFile) {
		t.Fatalf("err = %v, want ErrCredentialsFile", err)
	}
}

func TestLoadConfigDirectoryPathErrors(t *testing.T) {
	home := setupHome(t)
	dir := filepath.Join(home, "adir")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	_, err := LoadConfig(context.Background(), WithConfigFile(dir), WithStaticToken("tok"), WithRegion("r"))
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("err = %v, want ErrInvalidConfig for a directory config path", err)
	}
}

func TestLoadConfigCredentialsFileMode0644And0640AreRefused(t *testing.T) {
	for _, perm := range []os.FileMode{0o644, 0o640} {
		t.Run(perm.String(), func(t *testing.T) {
			home := setupHome(t)
			writeFile(t, configPath(home), "[default]\nregion = r\n", 0o600)
			writeFile(t, credentialsPath(home), "[default]\nroot_email = secret-XYZ\nusername = u\npassword = secret-XYZ\n", perm)

			_, err := LoadConfig(context.Background())
			if !errors.Is(err, ErrCredentialsFile) {
				t.Fatalf("err = %v, want ErrCredentialsFile", err)
			}
			if !strings.Contains(err.Error(), "600") {
				t.Fatalf("err = %v, want it to suggest chmod 600", err)
			}
			if strings.Contains(err.Error(), "secret-XYZ") {
				t.Fatalf("err leaked a credential value: %v", err)
			}
		})
	}
}

func TestLoadConfigCredentialsFileMode0600Passes(t *testing.T) {
	home := setupHome(t)
	writeFile(t, configPath(home), "[default]\nregion = r\n", 0o600)
	writeFile(t, credentialsPath(home), "[default]\nroot_email = a\nusername = b\npassword = c\n", 0o600)

	if _, err := LoadConfig(context.Background()); err != nil {
		t.Fatalf("LoadConfig() error = %v, want nil for a 0600 credentials file", err)
	}
}

func TestLoadConfigMalformedFileErrorsWithNameAndLineNoValue(t *testing.T) {
	home := setupHome(t)
	writeFile(t, configPath(home), "[default]\nregion = r\n", 0o600)
	credPath := credentialsPath(home)
	writeFile(t, credPath, "[default]\nroot_email = a\nbroken-line-secret-XYZ\n", 0o600)

	_, err := LoadConfig(context.Background())
	if err == nil {
		t.Fatal("expected an error for a malformed credentials file")
	}
	if !strings.Contains(err.Error(), credPath) {
		t.Fatalf("err = %v, want it to name the file", err)
	}
	if !strings.Contains(err.Error(), ":3:") {
		t.Fatalf("err = %v, want it to name line 3", err)
	}
	if strings.Contains(err.Error(), "secret-XYZ") {
		t.Fatalf("err leaked file content: %v", err)
	}
}

func TestLoadConfigNoCredentialsAnywhereIsErrNoCredentials(t *testing.T) {
	home := setupHome(t)
	writeFile(t, configPath(home), "[default]\nregion = r\n", 0o600)
	_, err := LoadConfig(context.Background())
	if !errors.Is(err, ErrNoCredentials) {
		t.Fatalf("err = %v, want ErrNoCredentials", err)
	}
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatal("ErrNoCredentials must also match ErrInvalidConfig")
	}
}

// ---- Credential source selection (env and profile) ----

func TestLoadConfigCredentialsFromEnvAccessToken(t *testing.T) {
	home := setupHome(t)
	writeFile(t, configPath(home), "[default]\nregion = r\n", 0o600)
	t.Setenv("VNGCLOUD_ACCESS_TOKEN", "env-token")
	// Also set IAM values, to prove the access token wins within this source.
	t.Setenv("VNGCLOUD_ROOT_EMAIL", "env-root@example.test")
	t.Setenv("VNGCLOUD_USERNAME", "env-user")
	t.Setenv("VNGCLOUD_PASSWORD", "env-pass")

	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer env-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer api.Close()

	cfg, err := LoadConfig(context.Background(), WithEndpointOverrides(EndpointOverrides{VServer: api.URL}))
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	status, err := doRequest(t, ClientOf(cfg), api.URL)
	if err != nil {
		t.Fatalf("DoJSONStatus() error = %v", err)
	}
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200 (access token from env must be used)", status)
	}
}

func TestLoadConfigCredentialsFromProfileFile(t *testing.T) {
	home := setupHome(t)
	signinURL, dashboardURL, got := capturingLoginServer(t)
	writeFile(t, configPath(home), "[default]\nregion = r\n", 0o600)
	writeFile(t, credentialsPath(home), "[default]\nroot_email = file-root@example.test\nusername = file-user\npassword = file-pass\n", 0o600)

	cfg, err := LoadConfig(context.Background(),
		WithEndpointOverrides(EndpointOverrides{Signin: signinURL, Dashboard: dashboardURL}))
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if err := cfg.Authenticate(context.Background()); err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	rootEmail, username, password := got.snapshot()
	if rootEmail != "file-root@example.test" || username != "file-user" || password != "file-pass" {
		t.Fatalf("login used rootEmail=%q username=%q password=%q, want the profile file's credentials",
			rootEmail, username, password)
	}
}

func TestLoadConfigTOTPSecretFromProfileFile(t *testing.T) {
	home := setupHome(t)
	writeFile(t, configPath(home), "[default]\nregion = r\n", 0o600)
	writeFile(t, credentialsPath(home),
		"[default]\nroot_email = a\nusername = b\npassword = c\ntotp_secret = JBSWY3DPEHPK3PXP\n", 0o600)

	// No network call is needed: just confirm LoadConfig builds successfully
	// and (indirectly, through validate() not erroring) accepted the TOTP
	// secret as part of a complete IAM User credential set.
	if _, err := LoadConfig(context.Background()); err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
}
