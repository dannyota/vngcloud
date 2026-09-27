package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"danny.vn/vngcloud"
)

// sshKeyJSON builds a {"data": {...}} SSH key envelope, the shape GetSSHKey,
// ImportSSHKey, and CreateSSHKey all decode, always under id "key-1", name
// "my-key", and public key "ssh-ed25519 AAAA", the only ones every test in
// this file uses; privateKey is included only when non-empty, matching
// CreateSSHKey's own response.
func sshKeyJSON(privateKey string) string {
	body := map[string]any{
		"id":        "key-1",
		"name":      "my-key",
		"pubKey":    "ssh-ed25519 AAAA",
		"status":    "ACTIVE",
		"createdAt": "2026-01-01T00:00:00Z",
	}
	if privateKey != "" {
		body["privateKey"] = privateKey
	}
	b, err := json.Marshal(map[string]any{"data": body})
	if err != nil {
		panic(err)
	}
	return string(b)
}

// TestComputeGetSSHKeyPrintsNoPrivateKeyField checks that get-ssh-key's own
// Output, {"SSHKey": {...}}, never carries a PrivateKey field: SSHKey drops
// it entirely, per the SDK design, so there is nothing for the CLI to
// redact.
func TestComputeGetSSHKeyPrintsNoPrivateKeyField(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/sshKeys/key-1": jsonHandler(http.StatusOK, sshKeyJSON("")),
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "--project-id", "proj-1", "compute", "get-ssh-key", "--ssh-key-id", "key-1"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("get-ssh-key: %v (stderr=%s)", err, stderr.String())
	}
	if got, ok := fixture.methodFor("/v2/proj-1/sshKeys/key-1"); !ok || got != http.MethodGet {
		t.Fatalf("method = %q, ok=%v, want GET", got, ok)
	}
	got := stdout.String()
	if !strings.Contains(got, `"Name": "my-key"`) {
		t.Fatalf("stdout = %s, want the SSH key printed", got)
	}
	if strings.Contains(got, "PrivateKey") {
		t.Fatalf("stdout = %s, want no PrivateKey field at all", got)
	}
}

// TestComputeImportSSHKeySendsTrimmedPublicKey drives a real import-ssh-key
// call and checks the POST body the SDK built.
func TestComputeImportSSHKeySendsTrimmedPublicKey(t *testing.T) {
	var body []byte
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/sshKeys/import": func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				t.Fatalf("method = %s, want POST", r.Method)
			}
			defer func() { _ = r.Body.Close() }()
			body, _ = io.ReadAll(r.Body)
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(sshKeyJSON("")))
		},
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1", "compute", "import-ssh-key",
		"--name", "my-key", "--public-key", "ssh-ed25519 AAAA",
	})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("import-ssh-key: %v (stderr=%s)", err, stderr.String())
	}
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("body is not valid JSON: %v (%s)", err, body)
	}
	if decoded["name"] != "my-key" || decoded["pubKey"] != "ssh-ed25519 AAAA" {
		t.Fatalf("body = %s, want name=my-key pubKey=\"ssh-ed25519 AAAA\"", body)
	}
	if !strings.Contains(stdout.String(), `"Name": "my-key"`) {
		t.Fatalf("stdout = %s, want the imported SSH key printed", stdout.String())
	}
}

// TestComputeDeleteSSHKeyRequiresYes checks the CLI design's --yes rule:
// delete-ssh-key is Write and Destructive, so it fails with exit code 2 and
// sends no request unless --yes is given.
func TestComputeDeleteSSHKeyRequiresYes(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/sshKeys/key-1": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "--project-id", "proj-1", "compute", "delete-ssh-key", "--ssh-key-id", "key-1"})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected an error without --yes")
	}
	if got := exitCode(err); got != 2 {
		t.Fatalf("exitCode = %d, want 2 (stderr=%s)", got, stderr.String())
	}
	if n := fixture.requestCount(); n != 0 {
		t.Fatalf("requestCount = %d, want 0", n)
	}
}

// TestComputeDeleteSSHKeyWithYes checks that --yes sends the DELETE.
func TestComputeDeleteSSHKeyWithYes(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/sshKeys/key-1": func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodDelete {
				t.Fatalf("method = %s, want DELETE", r.Method)
			}
			w.WriteHeader(http.StatusNoContent)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1", "--yes",
		"compute", "delete-ssh-key", "--ssh-key-id", "key-1",
	})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("delete-ssh-key: %v (stderr=%s)", err, stderr.String())
	}
	if n := fixture.requestCount(); n != 1 {
		t.Fatalf("requestCount = %d, want 1", n)
	}
}

// TestComputeWritesReadOnlyRefusedWithZeroRequests checks the CLI design's
// read-only rule for import-ssh-key, create-ssh-key, and delete-ssh-key:
// all three are Write operations, so a read-only profile refuses each with
// exit 2 before any request.
func TestComputeWritesReadOnlyRefusedWithZeroRequests(t *testing.T) {
	tests := []struct {
		op   string
		args []string
	}{
		{"import-ssh-key", []string{"import-ssh-key", "--name", "k", "--public-key", "ssh-ed25519 AAAA"}},
		{"create-ssh-key", []string{"create-ssh-key", "--name", "k", "--secret-file", "/does-not-matter"}},
		{"delete-ssh-key", []string{"delete-ssh-key", "--ssh-key-id", "key-1", "--yes"}},
	}
	for _, tc := range tests {
		t.Run(tc.op, func(t *testing.T) {
			home := withCleanEnv(t)
			writeConfigFile(t, home, "[profile agent]\nregion = hcm-3\nproject_id = proj-1\nread_only = true\n")
			writeCredentialsFile(t, home, "[agent]\nusername = u\npassword = p\nroot_email = e@example.com\n")

			fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
				"/v2/proj-1/sshKeys": func(_ http.ResponseWriter, r *http.Request) {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				},
				"/v2/proj-1/sshKeys/import": func(_ http.ResponseWriter, r *http.Request) {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				},
				"/v2/proj-1/sshKeys/key-1": func(_ http.ResponseWriter, r *http.Request) {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				},
			})
			opts := newFakeServer(t, fixture.mux)
			withTestOptions(t, append(opts, vngcloud.WithStaticToken("test-token"))...)

			stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
			root := newRootCmd(strings.NewReader(""), stdout, stderr)
			root.SetArgs(append([]string{"--profile", "agent", "compute"}, tc.args...))
			err := root.ExecuteContext(context.Background())
			if err == nil {
				t.Fatalf("expected a read-only refusal")
			}
			if got := classify(err).Code; got != "ReadOnly" {
				t.Fatalf("Code = %q, want ReadOnly (stderr=%s)", got, stderr.String())
			}
			if got := exitCode(err); got != 2 {
				t.Fatalf("exitCode = %d, want 2", got)
			}
			if n := fixture.requestCount(); n != 0 {
				t.Fatalf("requestCount = %d, want 0", n)
			}
		})
	}
}

// TestComputeCreateSSHKeyRequiresSecretFile checks that create-ssh-key
// refuses to run, before any request, when --secret-file is missing.
func TestComputeCreateSSHKeyRequiresSecretFile(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/sshKeys": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "--project-id", "proj-1", "compute", "create-ssh-key", "--name", "my-key"})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected an error without --secret-file")
	}
	if got := exitCode(err); got != 2 {
		t.Fatalf("exitCode = %d, want 2 (stderr=%s)", got, stderr.String())
	}
	if n := fixture.requestCount(); n != 0 {
		t.Fatalf("requestCount = %d, want 0", n)
	}
}

// TestComputeCreateSSHKeyRefusesExistingSecretFile checks that create-ssh-key
// refuses an already-existing --secret-file path before any request.
func TestComputeCreateSSHKeyRefusesExistingSecretFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "key.pem")
	if err := os.WriteFile(path, []byte("existing"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/sshKeys": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1", "compute", "create-ssh-key",
		"--name", "my-key", "--secret-file", path,
	})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected an error for an existing --secret-file")
	}
	if got := exitCode(err); got != 2 {
		t.Fatalf("exitCode = %d, want 2 (stderr=%s)", got, stderr.String())
	}
	if n := fixture.requestCount(); n != 0 {
		t.Fatalf("requestCount = %d, want 0", n)
	}
	data, readErr := os.ReadFile(path)
	if readErr != nil || string(data) != "existing" {
		t.Fatalf("the existing file was modified: err=%v data=%q", readErr, data)
	}
}

// TestComputeCreateSSHKeyRefusesSymlinkSecretFile checks that create-ssh-key
// refuses a --secret-file path that is a symlink, before any request, even
// though the link's own target does not exist.
func TestComputeCreateSSHKeyRefusesSymlinkSecretFile(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "missing-target")
	link := filepath.Join(dir, "key.pem")
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("Symlink: %v", err)
	}
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/sshKeys": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1", "compute", "create-ssh-key",
		"--name", "my-key", "--secret-file", link,
	})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected an error for a symlink --secret-file")
	}
	if got := exitCode(err); got != 2 {
		t.Fatalf("exitCode = %d, want 2 (stderr=%s)", got, stderr.String())
	}
	if n := fixture.requestCount(); n != 0 {
		t.Fatalf("requestCount = %d, want 0", n)
	}
}

// TestComputeCreateSSHKeyWritesSecretFileAndRedactsOutput drives a real
// create-ssh-key call, with --debug on, and checks: the file lands at mode
// 0600 holding exactly the private key the fixture returned, stdout's
// PrivateKey field reads "[redacted]", stdout carries a SecretFile field
// naming the path, and the private key text itself appears nowhere in
// stdout or stderr, --debug's own write started/finished lines included.
func TestComputeCreateSSHKeyWritesSecretFileAndRedactsOutput(t *testing.T) {
	// Split so secret scanners do not read the fake key as a real one.
	const privateKey = "-----BEGIN " + "PRIVATE KEY-----\nfake-key-material\n-----END " + "PRIVATE KEY-----\n"
	dir := t.TempDir()
	path := filepath.Join(dir, "key.pem")

	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/sshKeys": func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				t.Fatalf("method = %s, want POST", r.Method)
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(sshKeyJSON(privateKey)))
		},
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1", "--debug", "compute", "create-ssh-key",
		"--name", "my-key", "--secret-file", path,
	})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("create-ssh-key: %v (stderr=%s)", err, stderr.String())
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat(%s): %v", path, err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("mode = %o, want 0600", got)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != privateKey {
		t.Fatalf("secret file content = %q, want %q", data, privateKey)
	}

	out := stdout.String()
	if !strings.Contains(out, `"PrivateKey": "[redacted]"`) {
		t.Fatalf("stdout = %s, want PrivateKey redacted", out)
	}
	if !strings.Contains(out, `"SecretFile": `+`"`+path+`"`) {
		t.Fatalf("stdout = %s, want SecretFile %q", out, path)
	}
	if strings.Contains(out, "fake-key-material") || strings.Contains(stderr.String(), "fake-key-material") {
		t.Fatalf("the private key leaked into output: stdout=%s stderr=%s", out, stderr.String())
	}
	if !strings.Contains(stderr.String(), "write started") || !strings.Contains(stderr.String(), "write finished") {
		t.Fatalf("stderr = %s, want --debug's write started/write finished lines", stderr.String())
	}
}

// TestComputeCreateSSHKeyCleansUpOnUnwritableSecretFile checks the CLI
// design's cleanup rule: when --secret-file cannot be written (here, its
// parent directory does not exist), the CLI deletes the new key through the
// SDK and reports SecretFileFailed, and the private key never reaches
// stdout or stderr either.
func TestComputeCreateSSHKeyCleansUpOnUnwritableSecretFile(t *testing.T) {
	// Split so secret scanners do not read the fake key as a real one.
	const privateKey = "-----BEGIN " + "PRIVATE KEY-----\nfake-key-material\n-----END " + "PRIVATE KEY-----\n"
	path := filepath.Join(t.TempDir(), "missing-dir", "key.pem")

	deleted := false
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/sshKeys": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(sshKeyJSON(privateKey)))
		},
		"/v2/proj-1/sshKeys/key-1": func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodDelete {
				t.Fatalf("method = %s, want DELETE", r.Method)
			}
			deleted = true
			w.WriteHeader(http.StatusNoContent)
		},
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1", "compute", "create-ssh-key",
		"--name", "my-key", "--secret-file", path,
	})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected a SecretFileFailed error")
	}
	if got := classify(err).Code; got != "SecretFileFailed" {
		t.Fatalf("Code = %q, want SecretFileFailed (stderr=%s)", got, stderr.String())
	}
	if got := exitCode(err); got != 1 {
		t.Fatalf("exitCode = %d, want 1", got)
	}
	if !deleted {
		t.Fatal("the orphaned key was never deleted")
	}
	if strings.Contains(stdout.String(), "fake-key-material") || strings.Contains(stderr.String(), "fake-key-material") {
		t.Fatalf("the private key leaked into output: stdout=%s stderr=%s", stdout.String(), stderr.String())
	}
	if _, statErr := os.Stat(path); statErr == nil {
		t.Fatalf("a partial secret file was left behind at %s", path)
	}
}

// TestComputeCreateSSHKeyCleanupFailureNamesOnlyTheID checks the CLI
// design's rule for the double-failure case: when both the secret file
// write and the cleanup delete fail, the error names the key only by its
// ID, never by the write or delete error's own text, and the private key
// itself never reaches the message either.
func TestComputeCreateSSHKeyCleanupFailureNamesOnlyTheID(t *testing.T) {
	// Split so secret scanners do not read the fake key as a real one.
	const privateKey = "-----BEGIN " + "PRIVATE KEY-----\nfake-key-material\n-----END " + "PRIVATE KEY-----\n"
	path := filepath.Join(t.TempDir(), "missing-dir", "key.pem")

	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/sshKeys": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(sshKeyJSON(privateKey)))
		},
		"/v2/proj-1/sshKeys/key-1": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		},
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1", "compute", "create-ssh-key",
		"--name", "my-key", "--secret-file", path,
	})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected a SecretFileFailed error")
	}
	env := classify(err)
	if env.Code != "SecretFileFailed" {
		t.Fatalf("Code = %q, want SecretFileFailed (stderr=%s)", env.Code, stderr.String())
	}
	if !strings.Contains(env.Message, "key-1") {
		t.Fatalf("message = %q, want it to name the key ID key-1", env.Message)
	}
	if strings.Contains(env.Message, "fake-key-material") {
		t.Fatalf("message = %q, leaked the private key", env.Message)
	}
	if strings.Contains(stdout.String(), "fake-key-material") || strings.Contains(stderr.String(), "fake-key-material") {
		t.Fatalf("the private key leaked into output: stdout=%s stderr=%s", stdout.String(), stderr.String())
	}
}
