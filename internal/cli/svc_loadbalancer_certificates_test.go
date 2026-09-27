package cli

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"io"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"danny.vn/vngcloud"
)

// certificateFixture is a throwaway self-signed certificate and its RSA
// private key, generated once for this test binary rather than committed to
// testdata: a committed key, even a fake one, risks tripping a secret
// scanner and needs no allowlist this way. Nothing here is a real
// credential; the key never leaves this process. It mirrors
// loadbalancer/certificates_write_test.go's own fixture, generated
// separately since that package's unexported helper is not visible here.
var certificateFixture = generateCertificateFixture()

type certFixture struct {
	certPEM string
	keyPEM  string
}

func generateCertificateFixture() certFixture {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "vngcloud-cli-test.invalid"},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		panic(err)
	}
	cert := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	pkey := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	return certFixture{certPEM: string(cert), keyPEM: string(pkey)}
}

// writeCertFile writes content to name under t.TempDir and returns its path.
func writeCertFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile(%s): %v", name, err)
	}
	return path
}

// importCertificateResponseBody is the {"data": {...}} envelope the fixture
// server answers a successful import-certificate POST with.
const importCertificateResponseBody = `{"data":{"uuid":"cert-1","name":"example-com","certificateType":"TLS/SSL","inUse":false}}`

// TestLoadBalancerImportCertificateNoLiteralFlagsExist checks the CLI
// design's rule that every certificate and key field comes from a file
// flag: no --certificate, --certificate-chain, --private-key, or
// --passphrase flag is ever registered on import-certificate.
func TestLoadBalancerImportCertificateNoLiteralFlagsExist(t *testing.T) {
	cmd := newLoadBalancerCmd(&env{flags: &globalFlags{}})
	sub, _, err := cmd.Find([]string{"import-certificate"})
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	for _, name := range []string{"certificate", "certificate-chain", "private-key", "passphrase"} {
		if f := sub.Flags().Lookup(name); f != nil {
			t.Fatalf("import-certificate registered its own --%s flag: %+v", name, f)
		}
	}
	for _, name := range []string{"certificate-file", "certificate-chain-file", "private-key-file", "passphrase-file"} {
		if f := sub.Flags().Lookup(name); f == nil {
			t.Fatalf("import-certificate is missing --%s", name)
		}
	}
}

// TestLoadBalancerImportCertificateSendsFileContents drives a real
// import-certificate call with every file flag set and checks the exact POST
// body the SDK built, plus the printed Output.
func TestLoadBalancerImportCertificateSendsFileContents(t *testing.T) {
	certPath := writeCertFile(t, "cert.pem", certificateFixture.certPEM)
	chainPath := writeCertFile(t, "chain.pem", certificateFixture.certPEM+certificateFixture.certPEM)
	keyPath := writeCertFile(t, "key.pem", certificateFixture.keyPEM)
	passPath := writeCertFile(t, "pass.txt", "correct-horse-battery-staple\n")

	var body []byte
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/cas": func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				t.Fatalf("method = %s, want POST", r.Method)
			}
			defer func() { _ = r.Body.Close() }()
			body, _ = io.ReadAll(r.Body)
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(importCertificateResponseBody))
		},
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1",
		"loadbalancer", "import-certificate",
		"--name", "example-com", "--type", "TLS/SSL",
		"--certificate-file", certPath, "--certificate-chain-file", chainPath,
		"--private-key-file", keyPath, "--passphrase-file", passPath,
	})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("import-certificate: %v (stderr=%s)", err, stderr.String())
	}

	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("body is not valid JSON: %v (%s)", err, body)
	}
	if decoded["name"] != "example-com" || decoded["type"] != "TLS/SSL" {
		t.Fatalf("body = %s, want name=example-com type=TLS/SSL", body)
	}
	if decoded["certificate"] != certificateFixture.certPEM {
		t.Fatalf("body certificate does not hold the file's exact content")
	}
	if decoded["certificateChain"] != certificateFixture.certPEM+certificateFixture.certPEM {
		t.Fatalf("body certificateChain does not hold the file's exact content")
	}
	if decoded["privateKey"] != certificateFixture.keyPEM {
		t.Fatalf("body privateKey does not hold the key file's exact content in the clear")
	}
	// The passphrase file ends with "\n"; the CLI must drop exactly that one
	// trailing newline, the same rule configure set <key> - applies to stdin.
	if decoded["passphrase"] != "correct-horse-battery-staple" {
		t.Fatalf("body passphrase = %v, want the trailing newline dropped", decoded["passphrase"])
	}
	if !strings.Contains(stdout.String(), `"UUID": "cert-1"`) {
		t.Fatalf("stdout = %s, want the imported certificate printed", stdout.String())
	}
	if strings.Contains(stdout.String(), certificateFixture.keyPEM) {
		t.Fatalf("stdout leaks the private key: %s", stdout.String())
	}
}

// TestLoadBalancerImportCertificateMissingCertificateFileNoRequest checks
// that import-certificate refuses to run, before any request, when
// --certificate-file is missing and --cli-input-json sets no Certificate
// either.
func TestLoadBalancerImportCertificateMissingCertificateFileNoRequest(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/cas": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1",
		"loadbalancer", "import-certificate", "--name", "example-com", "--type", "TLS/SSL",
	})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected an error without --certificate-file")
	}
	if got := exitCode(err); got != 2 {
		t.Fatalf("exitCode = %d, want 2 (stderr=%s)", got, stderr.String())
	}
	if n := fixture.requestCount(); n != 0 {
		t.Fatalf("requestCount = %d, want 0", n)
	}
}

// TestLoadBalancerImportCertificateEmptyFileRefused checks that an empty
// --certificate-file is refused before any request.
func TestLoadBalancerImportCertificateEmptyFileRefused(t *testing.T) {
	certPath := writeCertFile(t, "cert.pem", "")
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/cas": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1",
		"loadbalancer", "import-certificate", "--name", "example-com", "--type", "TLS/SSL",
		"--certificate-file", certPath,
	})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected an error for an empty --certificate-file")
	}
	if got := exitCode(err); got != 2 {
		t.Fatalf("exitCode = %d, want 2 (stderr=%s)", got, stderr.String())
	}
	if n := fixture.requestCount(); n != 0 {
		t.Fatalf("requestCount = %d, want 0", n)
	}
}

// TestLoadBalancerImportCertificateOversizedFileRefused checks that a
// --certificate-file over maxInputFileSize is refused before any request,
// and that the error never echoes the file's content.
func TestLoadBalancerImportCertificateOversizedFileRefused(t *testing.T) {
	content := strings.Repeat("a", maxInputFileSize+1)
	certPath := writeCertFile(t, "cert.pem", content)
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/cas": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1",
		"loadbalancer", "import-certificate", "--name", "example-com", "--type", "TLS/SSL",
		"--certificate-file", certPath,
	})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected an error for an oversized --certificate-file")
	}
	if got := exitCode(err); got != 2 {
		t.Fatalf("exitCode = %d, want 2 (stderr=%s)", got, stderr.String())
	}
	if n := fixture.requestCount(); n != 0 {
		t.Fatalf("requestCount = %d, want 0", n)
	}
	if strings.Contains(err.Error(), content) {
		t.Fatalf("error echoes the oversized file's content: %v", err)
	}
}

// TestLoadBalancerImportCertificateMissingFileRefused checks that a
// --certificate-file naming a path that does not exist is refused before any
// request, and that the error never echoes the (nonexistent) content.
func TestLoadBalancerImportCertificateMissingFileRefused(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/cas": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1",
		"loadbalancer", "import-certificate", "--name", "example-com", "--type", "TLS/SSL",
		"--certificate-file", filepath.Join(t.TempDir(), "does-not-exist.pem"),
	})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected an error for a missing --certificate-file")
	}
	if got := exitCode(err); got != 2 {
		t.Fatalf("exitCode = %d, want 2 (stderr=%s)", got, stderr.String())
	}
	if n := fixture.requestCount(); n != 0 {
		t.Fatalf("requestCount = %d, want 0", n)
	}
}

// cliInputJSONSecretRefusal is the exact error applyCLIInputJSON (input.go)
// returns for a --cli-input-json value that names field, a vngcloud.Secret
// field.
func cliInputJSONSecretRefusal(field string) string {
	return `--cli-input-json: "` + field + `" holds a secret value and cannot be set through --cli-input-json`
}

// testCLIInputJSONRefusesSecretField drives import-certificate with a
// --cli-input-json value that sets field to a PEM value holding literal
// newlines, both inline and through file://, and checks that the command
// fails on the design's Secret-field refusal rather than on invalid JSON. A
// raw PEM value embedded directly inside a JSON string literal is not valid
// JSON, since its newlines are unescaped control characters; building the
// body with json.Marshal instead escapes them, so the decode succeeds and
// the command actually reaches applyCLIInputJSON's secret check.
func testCLIInputJSONRefusesSecretField(t *testing.T, field string) {
	t.Helper()
	certPath := writeCertFile(t, "cert.pem", certificateFixture.certPEM)
	body, err := json.Marshal(map[string]string{field: certificateFixture.keyPEM})
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}

	t.Run("inline", func(t *testing.T) {
		fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
			"/v2/proj-1/cas": func(_ http.ResponseWriter, r *http.Request) {
				t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			},
		})
		root, _, stderr := newSvcRoot(t, fixture)
		root.SetArgs([]string{
			"--region", "hcm-3", "--project-id", "proj-1",
			"loadbalancer", "import-certificate", "--name", "example-com", "--type", "TLS/SSL",
			"--certificate-file", certPath,
			"--cli-input-json", string(body),
		})
		err := root.ExecuteContext(context.Background())
		if err == nil {
			t.Fatalf("expected an error for an inline --cli-input-json %s", field)
		}
		if got, want := err.Error(), cliInputJSONSecretRefusal(field); got != want {
			t.Fatalf("error = %q, want %q (stderr=%s)", got, want, stderr.String())
		}
		if got := exitCode(err); got != 2 {
			t.Fatalf("exitCode = %d, want 2 (stderr=%s)", got, stderr.String())
		}
		if n := fixture.requestCount(); n != 0 {
			t.Fatalf("requestCount = %d, want 0", n)
		}
	})

	t.Run("file", func(t *testing.T) {
		jsonPath := writeCertFile(t, "input.json", string(body))
		fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
			"/v2/proj-1/cas": func(_ http.ResponseWriter, r *http.Request) {
				t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			},
		})
		root, _, stderr := newSvcRoot(t, fixture)
		root.SetArgs([]string{
			"--region", "hcm-3", "--project-id", "proj-1",
			"loadbalancer", "import-certificate", "--name", "example-com", "--type", "TLS/SSL",
			"--certificate-file", certPath,
			"--cli-input-json", "file://" + jsonPath,
		})
		err := root.ExecuteContext(context.Background())
		if err == nil {
			t.Fatalf("expected an error for a file:// --cli-input-json %s", field)
		}
		if got, want := err.Error(), cliInputJSONSecretRefusal(field); got != want {
			t.Fatalf("error = %q, want %q (stderr=%s)", got, want, stderr.String())
		}
		if got := exitCode(err); got != 2 {
			t.Fatalf("exitCode = %d, want 2 (stderr=%s)", got, stderr.String())
		}
		if n := fixture.requestCount(); n != 0 {
			t.Fatalf("requestCount = %d, want 0", n)
		}
	})
}

// TestLoadBalancerImportCertificateCLIInputJSONRefusesPrivateKey checks that
// --cli-input-json can never set PrivateKey, inline or file://, per the CLI
// design.
func TestLoadBalancerImportCertificateCLIInputJSONRefusesPrivateKey(t *testing.T) {
	testCLIInputJSONRefusesSecretField(t, "PrivateKey")
}

// TestLoadBalancerImportCertificateCLIInputJSONRefusesPassphrase checks that
// --cli-input-json can never set Passphrase, inline or file://, the same
// rule as PrivateKey.
func TestLoadBalancerImportCertificateCLIInputJSONRefusesPassphrase(t *testing.T) {
	testCLIInputJSONRefusesSecretField(t, "Passphrase")
}

// TestLoadBalancerImportCertificateReadOnlyRefusedWithZeroRequests checks
// the CLI design's read-only rule: import-certificate is a Write, so a
// read-only profile refuses it with exit 2 before any request, even though
// its own file-reading guard already ran and succeeded.
func TestLoadBalancerImportCertificateReadOnlyRefusedWithZeroRequests(t *testing.T) {
	certPath := writeCertFile(t, "cert.pem", certificateFixture.certPEM)

	home := withCleanEnv(t)
	writeConfigFile(t, home, "[profile agent]\nregion = hcm-3\nproject_id = proj-1\nread_only = true\n")
	writeCredentialsFile(t, home, "[agent]\nusername = u\npassword = p\nroot_email = e@example.com\n")

	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/cas": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	opts := newFakeServer(t, fixture.mux)
	withTestOptions(t, append(opts, vngcloud.WithStaticToken("test-token"))...)

	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	root := newRootCmd(strings.NewReader(""), stdout, stderr)
	root.SetArgs([]string{
		"--profile", "agent",
		"loadbalancer", "import-certificate", "--name", "example-com", "--type", "TLS/SSL",
		"--certificate-file", certPath,
	})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatalf("expected a read-only refusal")
	}
	if got := classify(err).Code; got != "ReadOnly" {
		t.Fatalf("Code = %q, want ReadOnly (stderr=%s)", got, stderr.String())
	}
	if n := fixture.requestCount(); n != 0 {
		t.Fatalf("requestCount = %d, want 0", n)
	}
}

// TestLoadBalancerImportCertificateNeverLeaksSecrets checks the CLI
// design's core security rule: the private key and passphrase never appear
// in stdout, stderr, or --debug output, on both a successful import and one
// the server rejects while quoting the key back.
func TestLoadBalancerImportCertificateNeverLeaksSecrets(t *testing.T) {
	const passphrase = "correct-horse-battery-staple"
	certPath := writeCertFile(t, "cert.pem", certificateFixture.certPEM)
	keyPath := writeCertFile(t, "key.pem", certificateFixture.keyPEM)
	passPath := writeCertFile(t, "pass.txt", passphrase)

	rejectBody, err := json.Marshal(map[string]string{"message": "invalid privateKey: " + certificateFixture.keyPEM})
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}

	tests := []struct {
		name   string
		status int
		body   string
	}{
		{"success", http.StatusCreated, importCertificateResponseBody},
		{"failure quoting the key", http.StatusBadRequest, string(rejectBody)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
				"/v2/proj-1/cas": jsonHandler(tc.status, tc.body),
			})
			root, stdout, stderr := newSvcRoot(t, fixture)
			root.SetArgs([]string{
				"--region", "hcm-3", "--project-id", "proj-1", "--debug",
				"loadbalancer", "import-certificate",
				"--name", "example-com", "--type", "TLS/SSL",
				"--certificate-file", certPath, "--private-key-file", keyPath, "--passphrase-file", passPath,
			})
			_ = root.ExecuteContext(context.Background())
			out := stdout.String() + stderr.String()
			if strings.Contains(out, certificateFixture.keyPEM) {
				t.Fatalf("output leaks the private key: %s", out)
			}
			if strings.Contains(out, passphrase) {
				t.Fatalf("output leaks the passphrase: %s", out)
			}
		})
	}
}

// TestLoadBalancerDeleteCertificateRequiresYes checks the CLI design's --yes
// rule: delete-certificate is Write and Destructive, so it fails with exit
// code 2 and sends no request unless --yes is given.
func TestLoadBalancerDeleteCertificateRequiresYes(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/cas/cert-1": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1",
		"loadbalancer", "delete-certificate", "--certificate-id", "cert-1",
	})
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

// TestLoadBalancerDeleteCertificateWithYes checks that --yes reads the
// certificate first, then sends the DELETE, and prints the (empty) Output.
func TestLoadBalancerDeleteCertificateWithYes(t *testing.T) {
	var getCalls, delCalls int
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/cas/cert-1": func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodGet:
				getCalls++
				_, _ = w.Write([]byte(`{"uuid":"cert-1","inUse":false}`))
			case http.MethodDelete:
				delCalls++
				w.WriteHeader(http.StatusNoContent)
			default:
				t.Fatalf("unexpected method: %s", r.Method)
			}
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1", "--yes",
		"loadbalancer", "delete-certificate", "--certificate-id", "cert-1",
	})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("delete-certificate: %v (stderr=%s)", err, stderr.String())
	}
	if getCalls != 1 || delCalls != 1 {
		t.Fatalf("getCalls=%d delCalls=%d, want 1 and 1", getCalls, delCalls)
	}
}

// TestLoadBalancerDeleteCertificateInUseSendsNoDelete checks that a
// pre-delete read showing InUse true refuses the command, with error code
// ResourceInUse, and sends no DELETE.
func TestLoadBalancerDeleteCertificateInUseSendsNoDelete(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/cas/cert-1": func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodDelete {
				t.Fatal("no DELETE expected when the pre-read shows InUse")
			}
			_, _ = w.Write([]byte(`{"uuid":"cert-1","inUse":true}`))
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1", "--yes",
		"loadbalancer", "delete-certificate", "--certificate-id", "cert-1",
	})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected an error for a certificate in use")
	}
	if got := classify(err).Code; got != "ResourceInUse" {
		t.Fatalf("Code = %q, want ResourceInUse (stderr=%s)", got, stderr.String())
	}
	if got := exitCode(err); got != 1 {
		t.Fatalf("exitCode = %d, want 1", got)
	}
}

// TestLoadBalancerDeleteCertificateReadOnlyRefusedWithZeroRequests checks
// the CLI design's read-only rule for delete-certificate.
func TestLoadBalancerDeleteCertificateReadOnlyRefusedWithZeroRequests(t *testing.T) {
	home := withCleanEnv(t)
	writeConfigFile(t, home, "[profile agent]\nregion = hcm-3\nproject_id = proj-1\nread_only = true\n")
	writeCredentialsFile(t, home, "[agent]\nusername = u\npassword = p\nroot_email = e@example.com\n")

	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/cas/cert-1": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	opts := newFakeServer(t, fixture.mux)
	withTestOptions(t, append(opts, vngcloud.WithStaticToken("test-token"))...)

	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	root := newRootCmd(strings.NewReader(""), stdout, stderr)
	root.SetArgs([]string{
		"--profile", "agent", "--yes",
		"loadbalancer", "delete-certificate", "--certificate-id", "cert-1",
	})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatalf("expected a read-only refusal")
	}
	if got := classify(err).Code; got != "ReadOnly" {
		t.Fatalf("Code = %q, want ReadOnly (stderr=%s)", got, stderr.String())
	}
	if n := fixture.requestCount(); n != 0 {
		t.Fatalf("requestCount = %d, want 0", n)
	}
}
