package cli

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"danny.vn/vngcloud"
)

// runConfigure builds a fresh command tree (as one real process invocation
// would) with stdin set to stdinContent, runs args, and returns stdout and
// the result. Every configure test drives commands this way, since
// configure's own state lives on disk, not in the process, so a fresh root
// per call is exactly how the real CLI behaves across separate invocations.
func runConfigure(t *testing.T, stdinContent string, args []string) (stdout string, err error) {
	t.Helper()
	var out, errOut strings.Builder
	root := newRootCmd(strings.NewReader(stdinContent), &out, &errOut)
	root.SetArgs(args)
	err = root.ExecuteContext(context.Background())
	if err != nil {
		t.Logf("args=%v stderr=%s", args, errOut.String())
	}
	return out.String(), err
}

func configPath(home string) string      { return filepath.Join(home, ".vngcloud", "config") }
func credentialsPath(home string) string { return filepath.Join(home, ".vngcloud", "credentials") }

func TestConfigureSetThenLoadConfigReadsItBack(t *testing.T) {
	home := withCleanEnv(t)
	if _, err := runConfigure(t, "", []string{"configure", "set", "region", "hcm-3"}); err != nil {
		t.Fatalf("set region: %v", err)
	}
	if _, err := runConfigure(t, "", []string{"configure", "set", "username", "u"}); err != nil {
		t.Fatalf("set username: %v", err)
	}
	if _, err := runConfigure(t, "", []string{"configure", "set", "root_email", "e@example.com"}); err != nil {
		t.Fatalf("set root_email: %v", err)
	}
	if _, err := runConfigure(t, "s3cr3t\n", []string{"configure", "set", "password", "-"}); err != nil {
		t.Fatalf("set password: %v", err)
	}

	_ = home
	cfg, err := vngcloud.LoadConfig(context.Background())
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Region() != "hcm-3" {
		t.Fatalf("Region() = %q, want hcm-3", cfg.Region())
	}
}

func TestConfigureSetDuplicateKeysEditsTheLastOne(t *testing.T) {
	content := "[default]\nregion = a\nregion = b\n"
	got := setINIValue([]byte(content), "default", "region", "c")
	want := "[default]\nregion = a\nregion = c\n"
	if string(got) != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if v := readINIValue(got, "default", "region"); v != "c" {
		t.Fatalf("readINIValue = %q, want c", v)
	}
}

func TestSetINIValueInsertsNewKeyBeforeTrailingBlankLines(t *testing.T) {
	content := "[default]\nregion = a\n\n[other]\nx = 1\n"
	got := setINIValue([]byte(content), "default", "read_only", "true")
	want := "[default]\nregion = a\nread_only = true\n\n[other]\nx = 1\n"
	if string(got) != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestSetINIValueInsertsNewKeyRightAfterHeaderWhenSectionHasNoKeys(t *testing.T) {
	content := "[default]\n\n[other]\nx = 1\n"
	got := setINIValue([]byte(content), "default", "region", "hcm-3")
	want := "[default]\nregion = hcm-3\n\n[other]\nx = 1\n"
	if string(got) != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestSetINIValueAlwaysEndsWithANewline(t *testing.T) {
	tests := []struct {
		name    string
		content string
		section string
		key     string
		value   string
	}{
		{"edit existing key, no trailing newline", "[default]\nregion = a", "default", "region", "b"},
		{"new key in existing section, no trailing newline", "[default]\nregion = a", "default", "read_only", "true"},
		{"new section in a fresh file", "", "default", "region", "hcm-3"},
		{"new section appended after existing content", "[other]\nx = 1\n", "default", "region", "hcm-3"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := setINIValue([]byte(tt.content), tt.section, tt.key, tt.value)
			if len(got) == 0 || got[len(got)-1] != '\n' {
				t.Fatalf("result does not end with a newline: %q", got)
			}
		})
	}
}

func TestConfigureSetDuplicateSectionsEditsTheLastOccurrence(t *testing.T) {
	content := "[profile x]\nregion = a\n\n[profile y]\nregion = z\n\n[profile x]\nregion = b\n"
	got := setINIValue([]byte(content), "profile x", "region", "c")
	if v := readINIValue(got, "profile x", "region"); v != "c" {
		t.Fatalf("readINIValue(profile x) = %q, want c", v)
	}
	if v := readINIValue(got, "profile y", "region"); v != "z" {
		t.Fatalf("readINIValue(profile y) = %q, want z (untouched)", v)
	}
	// The first occurrence's own line must be left alone; only the last
	// occurrence's line changes.
	if !strings.Contains(string(got), "region = a") {
		t.Fatalf("first occurrence's line was modified: %s", got)
	}
}

func TestConfigureSetReadOnlyCasingEditsTheEffectiveLine(t *testing.T) {
	content := "[default]\nRead_Only = true\n"
	got := setINIValue([]byte(content), "default", "read_only", "false")
	if strings.Count(string(got), "read_only") != 0 {
		t.Fatalf("expected the original casing to be kept, got %s", got)
	}
	if !strings.Contains(string(got), "Read_Only = false") {
		t.Fatalf("expected the existing line to be edited in place, got %s", got)
	}
	if v := readINIValue(got, "default", "read_only"); v != "false" {
		t.Fatalf("readINIValue = %q, want false", v)
	}
}

func TestConfigureSymlinkStaysASymlink(t *testing.T) {
	home := withCleanEnv(t)
	dir := filepath.Join(home, ".vngcloud")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	real := filepath.Join(dir, "real-config")
	if err := os.WriteFile(real, []byte("[default]\nregion = old\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	link := configPath(home)
	if err := os.Symlink(real, link); err != nil {
		t.Fatalf("Symlink: %v", err)
	}

	if _, err := runConfigure(t, "", []string{"configure", "set", "region", "new"}); err != nil {
		t.Fatalf("set region: %v", err)
	}

	info, err := os.Lstat(link)
	if err != nil {
		t.Fatalf("Lstat: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("%s is no longer a symlink", link)
	}
	target, err := os.Readlink(link)
	if err != nil {
		t.Fatalf("Readlink: %v", err)
	}
	if target != real {
		t.Fatalf("symlink target = %q, want %q", target, real)
	}
	data, err := os.ReadFile(real)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(data), "region = new") {
		t.Fatalf("real file was not updated: %s", data)
	}
}

func TestConfigureDanglingSymlinkRefused(t *testing.T) {
	home := withCleanEnv(t)
	dir := filepath.Join(home, ".vngcloud")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	link := configPath(home)
	if err := os.Symlink(filepath.Join(dir, "does-not-exist"), link); err != nil {
		t.Fatalf("Symlink: %v", err)
	}

	_, err := runConfigure(t, "", []string{"configure", "set", "region", "x"})
	if err == nil {
		t.Fatalf("expected an error for a dangling symlink")
	}
	if exitCode(err) != 2 {
		t.Fatalf("exitCode = %d, want 2", exitCode(err))
	}
	info, lerr := os.Lstat(link)
	if lerr != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("the dangling symlink should be left alone: %v, %v", info, lerr)
	}
}

func TestConfigureFileModes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits are not checked on windows")
	}
	home := withCleanEnv(t)
	if _, err := runConfigure(t, "", []string{"configure", "set", "region", "hcm-3"}); err != nil {
		t.Fatalf("set region: %v", err)
	}
	if _, err := runConfigure(t, "secret\n", []string{"configure", "set", "password", "-"}); err != nil {
		t.Fatalf("set password: %v", err)
	}

	dirInfo, err := os.Stat(filepath.Join(home, ".vngcloud"))
	if err != nil {
		t.Fatalf("Stat dir: %v", err)
	}
	if perm := dirInfo.Mode().Perm(); perm != 0o700 {
		t.Fatalf("dir mode = %o, want 0700", perm)
	}
	for _, path := range []string{configPath(home), credentialsPath(home)} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("Stat %s: %v", path, err)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Fatalf("%s mode = %o, want 0600", path, perm)
		}
	}
}

const configureSecretMarker = "marker-literal-secret"

func TestConfigureLiteralPasswordRefusedAndFileUnchanged(t *testing.T) {
	home := withCleanEnv(t)
	_, err := runConfigure(t, "", []string{"configure", "set", "password", configureSecretMarker})
	if err == nil {
		t.Fatalf("expected an error for a literal password")
	}
	if exitCode(err) != 2 {
		t.Fatalf("exitCode = %d, want 2", exitCode(err))
	}
	if strings.Contains(err.Error(), configureSecretMarker) {
		t.Fatalf("error echoed the secret value: %v", err)
	}
	if _, statErr := os.Stat(credentialsPath(home)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("credentials file should not have been created: %v", statErr)
	}
}

func TestConfigureLiteralTOTPSecretRefused(t *testing.T) {
	withCleanEnv(t)
	_, err := runConfigure(t, "", []string{"configure", "set", "totp_secret", "ABCDEF"})
	if err == nil {
		t.Fatalf("expected an error for a literal totp_secret")
	}
	if exitCode(err) != 2 {
		t.Fatalf("exitCode = %d, want 2", exitCode(err))
	}
}

func TestConfigureSetDashFromAPipe(t *testing.T) {
	home := withCleanEnv(t)
	if _, err := runConfigure(t, configureSecretMarker+"\n", []string{"configure", "set", "password", "-"}); err != nil {
		t.Fatalf("set password: %v", err)
	}
	data, err := os.ReadFile(credentialsPath(home))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(data), configureSecretMarker) {
		t.Fatalf("expected the credentials file to contain the piped value")
	}
}

func TestConfigureSetDashFromATerminalRefused(t *testing.T) {
	withCleanEnv(t)
	prev := isTerminalOverride
	isTerminalOverride = func(io.Reader) bool { return true }
	t.Cleanup(func() { isTerminalOverride = prev })

	_, err := runConfigure(t, "", []string{"configure", "set", "password", "-"})
	if err == nil {
		t.Fatalf("expected an error when stdin is a terminal")
	}
	if exitCode(err) != 2 {
		t.Fatalf("exitCode = %d, want 2", exitCode(err))
	}
}

func TestConfigureInteractiveNoTerminalExitsWithZeroWrites(t *testing.T) {
	home := withCleanEnv(t)
	_, err := runConfigure(t, "", []string{"configure"})
	if err == nil {
		t.Fatalf("expected an error without a terminal")
	}
	if exitCode(err) != 2 {
		t.Fatalf("exitCode = %d, want 2", exitCode(err))
	}
	if _, statErr := os.Stat(filepath.Join(home, ".vngcloud")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf(".vngcloud should not have been created: %v", statErr)
	}
}

func TestConfigureInteractiveWithTerminalPromptsAndMasksSecrets(t *testing.T) {
	withCleanEnv(t)
	prevTerm, prevSecret := isTerminalOverride, readSecretOverride
	isTerminalOverride = func(io.Reader) bool { return true }
	secretCalls := 0
	readSecretOverride = func(*env) (string, error) {
		secretCalls++
		if secretCalls == 1 { // password is the first secret prompt; totp_secret is the second
			return configureSecretMarker, nil
		}
		return "", nil
	}
	t.Cleanup(func() {
		isTerminalOverride = prevTerm
		readSecretOverride = prevSecret
	})

	answers := "hcm-3\n\n\n\n\n\n" // region, project_id, output, read_only, root_email, username (password/totp handled by the secret hook)
	var stdout strings.Builder
	root := newRootCmd(strings.NewReader(answers), &stdout, &strings.Builder{})
	root.SetArgs([]string{"configure"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("configure: %v (stdout=%s)", err, stdout.String())
	}
	if strings.Contains(stdout.String(), configureSecretMarker) {
		t.Fatalf("prompt output leaked the secret: %s", stdout.String())
	}
}

func TestConfigureReadOnlyRefusals(t *testing.T) {
	t.Run("configure under --read-only flag", func(t *testing.T) {
		withCleanEnv(t)
		_, err := runConfigure(t, "", []string{"--read-only", "configure"})
		if exitCode(err) != 2 || classify(err).Code != "ReadOnly" {
			t.Fatalf("err = %v, exitCode=%d, code=%s", err, exitCode(err), classify(err).Code)
		}
	})
	t.Run("configure set under VNGCLOUD_READ_ONLY", func(t *testing.T) {
		withCleanEnv(t)
		t.Setenv(envReadOnly, "1")
		_, err := runConfigure(t, "", []string{"configure", "set", "region", "x"})
		if exitCode(err) != 2 || classify(err).Code != "ReadOnly" {
			t.Fatalf("err = %v, exitCode=%d, code=%s", err, exitCode(err), classify(err).Code)
		}
	})
	t.Run("configure get and list still work", func(t *testing.T) {
		withCleanEnv(t)
		t.Setenv(envReadOnly, "1")
		if _, err := runConfigure(t, "", []string{"configure", "get", "region"}); err != nil {
			t.Fatalf("get: %v", err)
		}
		if _, err := runConfigure(t, "", []string{"configure", "list"}); err != nil {
			t.Fatalf("list: %v", err)
		}
	})
	t.Run("configure set read_only cannot turn it off", func(t *testing.T) {
		home := withCleanEnv(t)
		writeConfigFile(t, home, "[default]\nregion = hcm-3\nread_only = true\n")
		_, err := runConfigure(t, "", []string{"configure", "set", "read_only", "false"})
		if err == nil {
			t.Fatalf("expected a refusal")
		}
		if exitCode(err) != 2 {
			t.Fatalf("exitCode = %d, want 2", exitCode(err))
		}
		if v := readINIValue([]byte(mustReadFile(t, configPath(home))), "default", "read_only"); v != "true" {
			t.Fatalf("read_only = %q, want it unchanged (true)", v)
		}
	})
}

func mustReadFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile %s: %v", path, err)
	}
	return string(data)
}

func TestConfigureGetAndListMaskSecrets(t *testing.T) {
	withCleanEnv(t)
	if _, err := runConfigure(t, configureSecretMarker+"\n", []string{"configure", "set", "password", "-"}); err != nil {
		t.Fatalf("set password: %v", err)
	}

	out, err := runConfigure(t, "", []string{"configure", "get", "password"})
	if err != nil {
		t.Fatalf("get password: %v", err)
	}
	if strings.Contains(out, configureSecretMarker) {
		t.Fatalf("get password leaked the secret: %s", out)
	}
	if !strings.Contains(out, "****") {
		t.Fatalf("get password did not mask: %s", out)
	}

	out, err = runConfigure(t, "", []string{"configure", "list"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if strings.Contains(out, configureSecretMarker) {
		t.Fatalf("list leaked the secret: %s", out)
	}
}

func TestConfigureSetRefusesEmbeddedNewlineInValue(t *testing.T) {
	home := withCleanEnv(t)
	// A value containing a newline could otherwise inject a new key or
	// section into the INI file; setConfigureValue must refuse it before
	// writing anything.
	_, err := runConfigure(t, "injected\n[profile evil]\nusername = oops\n", []string{"configure", "set", "region", "-"})
	if err == nil {
		t.Fatalf("expected an error for a newline in the value")
	}
	if exitCode(err) != 2 {
		t.Fatalf("exitCode = %d, want 2", exitCode(err))
	}
	if _, statErr := os.Stat(configPath(home)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("config file should not have been created: %v", statErr)
	}
}

func TestValidateProfileName(t *testing.T) {
	bad := []string{"agent\n", "\nagent", "agent\r", "a\r\nb", "ag[ent", "ag]ent", " agent", "agent ", "\tagent"}
	for _, name := range bad {
		if err := validateProfileName(name); err == nil {
			t.Errorf("validateProfileName(%q) = nil, want an error", name)
		} else if exitCode(err) != 2 {
			t.Errorf("validateProfileName(%q) exitCode = %d, want 2", name, exitCode(err))
		}
	}
	good := []string{"default", "agent", "agent-1", "agent_2"}
	for _, name := range good {
		if err := validateProfileName(name); err != nil {
			t.Errorf("validateProfileName(%q) = %v, want nil", name, err)
		}
	}
}

// TestConfigureRejectsInjectingProfileNameBeforeAnyFileWrite is the exact
// attack the profile-name check exists for: without it, the "]" and
// embedded newlines in --profile would close the "[profile agent]" section
// header early and open a forged "[profile zz" section, letting
// "configure set region" silently flip the real agent profile's read_only
// key from true to false while appearing to write somewhere else entirely.
func TestConfigureRejectsInjectingProfileNameBeforeAnyFileWrite(t *testing.T) {
	home := withCleanEnv(t)
	original := "[profile agent]\nread_only = true\n"
	writeConfigFile(t, home, original)

	evilProfile := "agent]\nread_only = false\n[profile zz"
	_, err := runConfigure(t, "", []string{"--profile", evilProfile, "configure", "set", "region", "hcm-3"})
	if err == nil {
		t.Fatalf("expected an error for an injecting profile name")
	}
	if exitCode(err) != 2 {
		t.Fatalf("exitCode = %d, want 2", exitCode(err))
	}
	if got := mustReadFile(t, configPath(home)); got != original {
		t.Fatalf("config file changed:\n%s", got)
	}
}

func TestConfigureRejectsInjectingProfileNameFromEnv(t *testing.T) {
	home := withCleanEnv(t)
	original := "[profile agent]\nread_only = true\n"
	writeConfigFile(t, home, original)

	t.Setenv(envProfile, "agent]\nread_only = false\n[profile zz")
	_, err := runConfigure(t, "", []string{"configure", "set", "region", "hcm-3"})
	if err == nil {
		t.Fatalf("expected an error for an injecting VNGCLOUD_PROFILE value")
	}
	if exitCode(err) != 2 {
		t.Fatalf("exitCode = %d, want 2", exitCode(err))
	}
	if got := mustReadFile(t, configPath(home)); got != original {
		t.Fatalf("config file changed:\n%s", got)
	}
}

func TestSetAndReadINIValueStripLeadingBOM(t *testing.T) {
	bom := "\xEF\xBB\xBF"
	content := []byte(bom + "[default]\nregion = old\n")
	got := setINIValue(content, "default", "region", "new")
	if !strings.HasPrefix(string(got), bom) {
		t.Fatalf("BOM was dropped: %q", got)
	}
	if v := readINIValue(got, "default", "region"); v != "new" {
		t.Fatalf("readINIValue = %q, want new", v)
	}
	want := bom + "[default]\nregion = new\n"
	if string(got) != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// TestConfigureReadOnlyBypassViaBOMIsRejected is the exact bypass the BOM fix
// closes: a leading BOM previously hid every key in the file (including
// read_only) from readINIValue, so checkReadOnlyNotTurnedOff would find no
// current value and let this "false" write through even though the real,
// BOM-hidden value was true.
func TestConfigureReadOnlyBypassViaBOMIsRejected(t *testing.T) {
	home := withCleanEnv(t)
	original := "\xEF\xBB\xBF[default]\nread_only = true\n"
	writeConfigFile(t, home, original)

	_, err := runConfigure(t, "", []string{"configure", "set", "read_only", "false"})
	if err == nil {
		t.Fatalf("expected a refusal")
	}
	if exitCode(err) != 2 {
		t.Fatalf("exitCode = %d, want 2", exitCode(err))
	}
	if got := mustReadFile(t, configPath(home)); got != original {
		t.Fatalf("config file changed:\n%s", got)
	}
}

func TestConfigureEditKeepsExistingBOM(t *testing.T) {
	home := withCleanEnv(t)
	bom := "\xEF\xBB\xBF"
	writeConfigFile(t, home, bom+"[default]\nregion = old\n")

	if _, err := runConfigure(t, "", []string{"configure", "set", "region", "new"}); err != nil {
		t.Fatalf("set region: %v", err)
	}
	got := mustReadFile(t, configPath(home))
	if !strings.HasPrefix(got, bom) {
		t.Fatalf("BOM was dropped: %q", got)
	}
	if !strings.Contains(got, "region = new") {
		t.Fatalf("region was not updated: %q", got)
	}
	if strings.Contains(got, "region = old") {
		t.Fatalf("old line was not replaced: %q", got)
	}
}
