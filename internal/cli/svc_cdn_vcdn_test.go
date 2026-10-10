package cli

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/spf13/cobra"

	"danny.vn/vngcloud/cdn"
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
		"/v1/certificate/list":           serve("certificate-list.json"),
		"/v1/certificate/detail/cert-1":  serve("certificate-detail.json"),
		"/v1/apikey/list":                serve("apikey-list.json"),
		"/v1/cdn/list":                   serve("webaccelerator-list.json"),
		"/v1/cdn/detail/cdn-1":           serve("webaccelerator-detail.json"),
		"/v1/analytic/traffic-consuming": serve("analytics-traffic.json"),
		"/v1/analytic/cdn-requestsps":    serve("analytics-request-rate.json"),
		"/v1/analytic/cache-status":      serve("analytics-cache-status.json"),
		"/v1/analytic/cdn-http-codes":    serve("analytics-http-codes.json"),
		"/v1/analytic/traffic-report":    serve("analytics-traffic-report.json"),
		"/v1/cdn/update":                 serve("write-update.json"),
		"/v1/cdn/status/change/cdn-1":    jsonHandler(http.StatusBadRequest, `{"success":false,"code":400,"message":"refused","data":null}`),
		"/v1/cdn/delete/cdn-1":           jsonHandler(http.StatusBadRequest, `{"success":false,"code":400,"message":"refused","data":null}`),
	})
	return s
}

func TestCDNWebAcceleratorReadsAndAnalytics(t *testing.T) {
	tests := []struct {
		name string
		args []string
		path string
	}{
		{"list", []string{"cdn", "list-web-accelerators"}, "/v1/cdn/list"},
		{"get", []string{"cdn", "get-web-accelerator", "--cdn-id", "cdn-1"}, "/v1/cdn/detail/cdn-1"},
		{"traffic", []string{"cdn", "get-traffic", "--cli-input-json", `{"CDNDomains":["cdn.example.test"]}`, "--period", "24h"}, "/v1/analytic/traffic-consuming"},
		{"request-rate", []string{"cdn", "get-request-rate", "--cli-input-json", `{"CDNDomains":["cdn.example.test"]}`, "--period", "24h"}, "/v1/analytic/cdn-requestsps"},
		{"cache-status", []string{"cdn", "get-cache-status", "--cli-input-json", `{"CDNDomains":["cdn.example.test"]}`, "--period", "24h"}, "/v1/analytic/cache-status"},
		{"http-codes", []string{"cdn", "get-http-codes", "--cli-input-json", `{"CDNDomains":["cdn.example.test"]}`, "--period", "24h"}, "/v1/analytic/cdn-http-codes"},
		{"traffic-report", []string{"cdn", "get-traffic-report", "--cli-input-json", `{"CDNDomains":["cdn.example.test"]}`, "--from", "2026-10-08", "--to", "2026-10-09"}, "/v1/analytic/traffic-report"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newVCDNServer(t)
			stdout, stderr, run := vcdnRoot(t, s, vcdnKeyPlaceholder)
			if err := run(append([]string{"--output", "json"}, tt.args...)...); err != nil {
				t.Fatalf("execute: %v (stderr=%s)", err, stderr)
			}
			if method, ok := s.methodFor(tt.path); !ok || (method != http.MethodGet && method != http.MethodPost) {
				t.Fatalf("request for %s = %q, present=%v", tt.path, method, ok)
			}
			if !json.Valid([]byte(stdout.String())) {
				t.Fatalf("stdout is not JSON: %s", stdout)
			}
		})
	}
}

func TestGoldenCDNWebAccelerator(t *testing.T) {
	v := &cdn.GetWebAcceleratorOutput{WebAccelerator: cdn.WebAccelerator{
		CDNID: "cdn-1", Type: "webacc", DomainName: "app.example.test",
		CDNDomain: "cdn.example.test", Status: cdn.StatusActive, StatusName: "ACTIVE",
		CertificateID: "default", LBType: "rr", Upstreams: []cdn.Upstream{{ID: "upstream-1", IPAddress: "192.0.2.1"}},
	}}
	checkGolden(t, "cdn-get-web-accelerator.json.golden", "json", "", v)
	checkGolden(t, "cdn-get-web-accelerator.table.golden", "table", "WebAccelerator", v)
	checkGolden(t, "cdn-get-web-accelerator.text.golden", "text", "WebAccelerator", v)
}

func TestCDNListFieldsUseCLIInputJSON(t *testing.T) {
	cmd := newCDNCmd(&env{})
	for _, tt := range []struct {
		command string
		fields  []string
	}{
		{"get-traffic", []string{"cdn-domains"}},
		{"get-request-rate", []string{"cdn-domains"}},
		{"get-cache-status", []string{"cdn-domains"}},
		{"get-http-codes", []string{"cdn-domains"}},
		{"get-traffic-report", []string{"cdn-domains"}},
		{"update-web-accelerator", []string{"remove-rule-actions", "fail-over-error-codes", "c-names"}},
	} {
		t.Run(tt.command, func(t *testing.T) {
			sub, _, err := cmd.Find([]string{tt.command})
			if err != nil {
				t.Fatalf("Find: %v", err)
			}
			for _, field := range tt.fields {
				if flag := sub.Flags().Lookup(field); flag != nil {
					t.Errorf("--%s is registered", field)
				}
			}
		})
	}
}

func TestCDNWebAcceleratorWritesGuardsAndNoWait(t *testing.T) {
	for _, tt := range []struct {
		name    string
		args    []string
		want    int
		wantErr bool
	}{
		{"update", []string{"cdn", "update-web-accelerator", "--cdn-id", "cdn-1", "--cli-input-json", `{"CNames":["next.example.test"]}`, "--no-wait"}, 3, false},
		{"enable", []string{"cdn", "enable-web-accelerator", "--cdn-id", "cdn-1", "--no-wait"}, 1, false},
		{"disable-needs-yes", []string{"cdn", "disable-web-accelerator", "--cdn-id", "cdn-1"}, 0, true},
		{"delete-needs-yes", []string{"cdn", "delete-web-accelerator", "--cdn-id", "cdn-1"}, 0, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := newVCDNServer(t)
			_, _, run := vcdnRoot(t, s, vcdnKeyPlaceholder)
			err := run(tt.args...)
			if tt.wantErr {
				if err == nil || exitCode(err) != 2 {
					t.Fatalf("err = %v, exit = %d, want exit 2", err, exitCode(err))
				}
			} else if err != nil {
				t.Fatalf("execute: %v", err)
			}
			if got := s.requestCount(); got != tt.want {
				t.Fatalf("request count = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestCDNDisableAndDeleteAcceptYes(t *testing.T) {
	for _, args := range [][]string{
		{"cdn", "disable-web-accelerator", "--cdn-id", "cdn-1", "--yes"},
		{"cdn", "delete-web-accelerator", "--cdn-id", "cdn-1", "--yes"},
	} {
		t.Run(args[1], func(t *testing.T) {
			s := newVCDNServer(t)
			_, _, run := vcdnRoot(t, s, vcdnKeyPlaceholder)
			err := run(args...)
			if err == nil {
				t.Fatal("want a request error after --yes")
			}
			if got := s.requestCount(); got != 2 {
				t.Fatalf("request count = %d, want 2", got)
			}
		})
	}
}

func TestCDNDisableNoWait(t *testing.T) {
	detailCalls := 0
	body, err := os.ReadFile("../../testdata/cdn/webaccelerator-detail.json")
	if err != nil {
		t.Fatal(err)
	}
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v1/cdn/detail/cdn-1": func(w http.ResponseWriter, _ *http.Request) {
			detailCalls++
			out := string(body)
			if detailCalls == 2 {
				out = strings.Replace(out, `"status": 1`, `"status": 5`, 1)
			}
			jsonHandler(http.StatusOK, out)(w, nil)
		},
		"/v1/cdn/status/change/cdn-1": jsonHandler(http.StatusOK, `{"success":true,"code":200,"message":"ok","data":{}}`),
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	t.Setenv("VNGCLOUD_VCDN_API_KEY", vcdnKeyPlaceholder)
	root.SetArgs([]string{"--region", "hcm-3", "--output", "json", "cdn", "disable-web-accelerator", "--cdn-id", "cdn-1", "--no-wait", "--yes"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("execute: %v (stderr=%s)", err, stderr)
	}
	if fixture.requestCount() != 3 || !strings.Contains(stdout.String(), `"Changed": true`) {
		t.Fatalf("requests = %d, stdout = %s", fixture.requestCount(), stdout)
	}
}

func TestCDNWriteReadOnlyRefusesButAnalyticsRuns(t *testing.T) {
	s := newVCDNServer(t)
	_, _, run := vcdnRoot(t, s, vcdnKeyPlaceholder)
	err := run("--read-only", "cdn", "enable-web-accelerator", "--cdn-id", "cdn-1")
	if err == nil || classify(err).Code != "ReadOnly" || s.requestCount() != 0 {
		t.Fatalf("err = %v, code = %q, requests = %d", err, classify(err).Code, s.requestCount())
	}
	if err := run("--read-only", "cdn", "get-traffic", "--cli-input-json", `{"CDNDomains":["cdn.example.test"]}`, "--period", "24h"); err != nil {
		t.Fatalf("analytics read: %v", err)
	}
	if method, ok := s.methodFor("/v1/analytic/traffic-consuming"); !ok || method != http.MethodPost {
		t.Fatalf("analytics method = %q, present=%v", method, ok)
	}
}

func TestCDNErrorSentinelsClassifyAndNotSettledWinsCancellation(t *testing.T) {
	for _, tt := range []struct {
		err  error
		code string
	}{
		{cdn.ErrBusy, "ResourceBusy"},
		{cdn.ErrUnexpectedStatus, "UnexpectedStatus"},
		{cdn.ErrStatusUnconfirmed, "StatusUnconfirmed"},
		{errors.Join(cdn.ErrNotSettled, context.Canceled), "NotSettled"},
	} {
		if got := classify(tt.err).Code; got != tt.code {
			t.Errorf("classify(%v) = %q, want %q", tt.err, got, tt.code)
		}
		if exitCode(tt.err) != 1 {
			t.Errorf("exitCode(%v) = %d, want 1", tt.err, exitCode(tt.err))
		}
	}
}

func TestCDNCancelledSettlePrintsPartialOutput(t *testing.T) {
	h := newFakeHarness(t)
	var op Op[cdn.Client]
	for _, candidate := range cdnOps {
		if candidate.name == "update-web-accelerator" {
			op = candidate
		}
	}
	op.call = func(_ *cobra.Command, _ *cdn.Client, _ context.Context, _ any) (any, error) {
		return &cdn.UpdateWebAcceleratorOutput{WebAccelerator: cdn.WebAccelerator{
			CDNID: "cdn-1", Status: cdn.StatusDeploying, StatusName: "DEPLOYING",
		}}, errors.Join(cdn.ErrNotSettled, context.Canceled)
	}
	root := newTestRoot(h.e)
	root.AddCommand(Service(h.e, "cdn", "test", cdn.New, op))
	err := execCmd(t, root, []string{"--output", "json", "cdn", "update-web-accelerator", "--cdn-id", "cdn-1", "--cli-input-json", `{"CNames":["next.example.test"]}`})
	if err == nil || classify(err).Code != "NotSettled" || exitCode(err) != 1 {
		t.Fatalf("err = %v, code = %q, exit = %d", err, classify(err).Code, exitCode(err))
	}
	if !strings.Contains(h.stdout.String(), `"Status": 3`) {
		t.Fatalf("stdout = %s, want the last CDN read", h.stdout)
	}
	if strings.Contains(h.stdout.String(), vcdnKeyPlaceholder) || strings.Contains(h.stderr.String(), vcdnKeyPlaceholder) {
		t.Fatalf("output holds API key: stdout=%s stderr=%s", h.stdout, h.stderr)
	}
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
