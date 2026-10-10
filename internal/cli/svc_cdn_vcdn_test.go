package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
)

// vcdnServer serves the sanitized vCDN fixtures under testdata/cdn at the
// paths the cdn package calls, and records the Authorization and Origin
// headers of every request.
type vcdnServer struct {
	*svcFixture
	mu      sync.Mutex
	auths   []string
	origins []string
}

func newVCDNServer(t *testing.T) *vcdnServer {
	t.Helper()
	s := &vcdnServer{}
	serve := func(file string) func(http.ResponseWriter, *http.Request) {
		body, err := os.ReadFile("../../testdata/cdn/" + file)
		if err != nil {
			t.Fatalf("ReadFile %s: %v", file, err)
		}
		return func(w http.ResponseWriter, r *http.Request) {
			s.mu.Lock()
			s.auths = append(s.auths, r.Header.Get("Authorization"))
			s.origins = append(s.origins, r.Header.Get("Origin"))
			s.mu.Unlock()
			jsonHandler(http.StatusOK, string(body))(w, r)
		}
	}
	s.svcFixture = newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v1/certificate/list":          serve("certificate-list.json"),
		"/v1/certificate/detail/cert-1": serve("certificate-detail.json"),
		"/v1/apikey/list":               serve("apikey-list.json"),
	})
	return s
}

// vcdnRoot builds a root command wired at s with the placeholder key set
// through the environment, unless key is empty. run executes one command
// line and returns what it wrote.
func vcdnRoot(t *testing.T, s *vcdnServer, key string) (stdout, stderr *strings.Builder, run func(args ...string) error) {
	t.Helper()
	root, outBuf, errBuf := newSvcRoot(t, s.svcFixture)
	if key != "" {
		t.Setenv("VNGCLOUD_VCDN_API_KEY", key)
	}
	stdout, stderr = &strings.Builder{}, &strings.Builder{}
	run = func(args ...string) error {
		root.SetArgs(append([]string{"--region", "hcm-3"}, args...))
		err := root.ExecuteContext(context.Background())
		stdout.WriteString(outBuf.String())
		stderr.WriteString(errBuf.String())
		return err
	}
	return stdout, stderr, run
}

func TestCDNVCDNReadsSendBearerAndPrintNoSecrets(t *testing.T) {
	tests := []struct {
		name   string
		args   []string
		want   []string
		absent []string
	}{
		{
			"list-certificates",
			[]string{"cdn", "list-certificates"},
			[]string{`"ID": "cert-1"`, `"ID": "cert-2"`, `"CDNUsing": 2`},
			[]string{"ca-root", "certificate\\u003e", "BEGIN CERTIFICATE"},
		},
		{
			"get-certificate",
			[]string{"cdn", "get-certificate", "--certificate-id", "cert-1"},
			[]string{`"ID": "cert-1"`, `"Status": 1`},
			nil,
		},
		{
			"list-api-keys",
			[]string{"cdn", "list-api-keys"},
			[]string{`"ID": 1`, `"Current": true`, `"AllowOriginHeader": "https://`},
			nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newVCDNServer(t)
			stdout, stderr, run := vcdnRoot(t, s, vcdnKeyPlaceholder)
			if err := run(append([]string{"--output", "json"}, tt.args...)...); err != nil {
				t.Fatalf("execute: %v (stderr=%s)", err, stderr)
			}
			if len(s.auths) != 1 || s.auths[0] != "Bearer "+vcdnKeyPlaceholder {
				t.Fatalf("Authorization headers = %q, want one Bearer %s", s.auths, vcdnKeyPlaceholder)
			}
			if s.origins[0] != "" {
				t.Fatalf("Origin = %q, want none", s.origins[0])
			}
			if !json.Valid([]byte(stdout.String())) {
				t.Fatalf("stdout is not valid JSON: %s", stdout)
			}
			for _, want := range tt.want {
				if !strings.Contains(stdout.String(), want) {
					t.Errorf("stdout is missing %q:\n%s", want, stdout)
				}
			}
			lower := strings.ToLower(stdout.String())
			for _, banned := range []string{
				"secret", "private-key", "privatekey", "token", "useremail", "account",
			} {
				if strings.Contains(lower, banned) {
					t.Errorf("stdout holds %q:\n%s", banned, stdout)
				}
			}
			for _, banned := range tt.absent {
				if strings.Contains(stdout.String(), banned) {
					t.Errorf("stdout holds %q:\n%s", banned, stdout)
				}
			}
			if strings.Contains(stderr.String(), vcdnKeyPlaceholder) {
				t.Errorf("stderr holds the key: %s", stderr)
			}
		})
	}
}

// get-certificate prints the certificate and CA chain, which are public, and
// never the private key the server also returns.
func TestCDNGetCertificatePrintsPEMButNoPrivateKey(t *testing.T) {
	s := newVCDNServer(t)
	stdout, stderr, run := vcdnRoot(t, s, vcdnKeyPlaceholder)
	if err := run("--output", "json", "cdn", "get-certificate", "--certificate-id", "cert-1"); err != nil {
		t.Fatalf("execute: %v (stderr=%s)", err, stderr)
	}
	var decoded struct{ Certificate map[string]any }
	if err := json.Unmarshal([]byte(stdout.String()), &decoded); err != nil {
		t.Fatalf("stdout: %v", err)
	}
	pem, _ := decoded.Certificate["Certificate"].(string)
	ca, _ := decoded.Certificate["CARoot"].(string)
	if !strings.Contains(pem, "<certificate>") || !strings.Contains(ca, "<ca-root>") {
		t.Fatalf("certificate = %v, want the PEM text", decoded.Certificate)
	}
	if strings.Contains(strings.ToLower(stdout.String()), "private") {
		t.Fatalf("stdout mentions a private key:\n%s", stdout)
	}
}

func TestCDNVCDNReadsUseTheCredentialsFileKey(t *testing.T) {
	s := newVCDNServer(t)
	stdout, stderr, run := vcdnRoot(t, s, "")
	if _, err := runConfigure(t, vcdnKeyPlaceholder+"\n", []string{"configure", "set", "vcdn_api_key", "-"}); err != nil {
		t.Fatalf("configure set: %v", err)
	}
	if err := run("--output", "json", "cdn", "list-api-keys"); err != nil {
		t.Fatalf("execute: %v (stderr=%s)", err, stderr)
	}
	if len(s.auths) != 1 || s.auths[0] != "Bearer "+vcdnKeyPlaceholder {
		t.Fatalf("Authorization headers = %q", s.auths)
	}
	if !strings.Contains(stdout.String(), `"Current": true`) {
		t.Fatalf("stdout = %s, want the current key marked", stdout)
	}
}

func TestCDNVCDNReadsWithoutAKeyExitThreeWithTheHint(t *testing.T) {
	for _, args := range [][]string{
		{"cdn", "list-certificates"},
		{"cdn", "get-certificate", "--certificate-id", "cert-1"},
		{"cdn", "list-api-keys"},
	} {
		t.Run(args[1], func(t *testing.T) {
			s := newVCDNServer(t)
			_, _, run := vcdnRoot(t, s, "")
			err := run(args...)
			if err == nil {
				t.Fatal("expected an error without a key")
			}
			if exitCode(err) != 3 {
				t.Fatalf("exitCode = %d, want 3", exitCode(err))
			}
			env := classify(err)
			if env.Code != "NoCredentials" {
				t.Fatalf("Code = %q, want NoCredentials", env.Code)
			}
			for _, want := range []string{"configure set vcdn_api_key -", "VNGCLOUD_VCDN_API_KEY"} {
				if !strings.Contains(env.Message, want) {
					t.Errorf("message %q does not name %q", env.Message, want)
				}
			}
			if n := s.requestCount(); n != 0 {
				t.Fatalf("requestCount = %d, want 0", n)
			}
		})
	}
}

func TestCDNGetCertificateRequiresCertificateID(t *testing.T) {
	s := newVCDNServer(t)
	_, _, run := vcdnRoot(t, s, vcdnKeyPlaceholder)
	err := run("cdn", "get-certificate")
	if err == nil || exitCode(err) != 2 {
		t.Fatalf("err = %v, exit %d, want exit 2", err, exitCode(err))
	}
	if n := s.requestCount(); n != 0 {
		t.Fatalf("requestCount = %d, want 0", n)
	}
}

func TestCDNVCDNRejectedKeyExitsThreeWithoutTheKey(t *testing.T) {
	s := &vcdnServer{svcFixture: newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v1/apikey/list": func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusUnauthorized) },
	})}
	_, stderr, run := vcdnRoot(t, s, vcdnKeyPlaceholder)
	err := run("cdn", "list-api-keys")
	if err == nil {
		t.Fatal("expected an error for a 401")
	}
	if exitCode(err) != 3 {
		t.Fatalf("exitCode = %d, want 3", exitCode(err))
	}
	if strings.Contains(err.Error(), vcdnKeyPlaceholder) || strings.Contains(stderr.String(), vcdnKeyPlaceholder) {
		t.Fatalf("the key reached the error output: %v %s", err, stderr)
	}
	if n := s.requestCount(); n != 1 {
		t.Fatalf("requestCount = %d, want 1 (a 401 is not retried)", n)
	}
}

func TestCDNVCDNDebugOutputHoldsNoKey(t *testing.T) {
	for _, args := range [][]string{
		{"cdn", "list-certificates"},
		{"cdn", "get-certificate", "--certificate-id", "cert-1"},
		{"cdn", "list-api-keys"},
	} {
		t.Run(args[1], func(t *testing.T) {
			s := newVCDNServer(t)
			stdout, stderr, run := vcdnRoot(t, s, vcdnKeyPlaceholder)
			if err := run(append([]string{"--debug"}, args...)...); err != nil {
				t.Fatalf("execute: %v (stderr=%s)", err, stderr)
			}
			if !strings.Contains(stderr.String(), "msg=request") {
				t.Fatalf("stderr = %q, want a request log line", stderr)
			}
			for _, out := range []string{stdout.String(), stderr.String()} {
				for _, banned := range []string{"secret", "private-key", "Bearer", "Authorization"} {
					if strings.Contains(out, banned) {
						t.Errorf("output holds %q:\n%s", banned, out)
					}
				}
			}
		})
	}
}
