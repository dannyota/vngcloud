//go:build livewrite

package livetest_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/iam"
	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/jsonresponse"
	"danny.vn/vngcloud/internal/routes"
	"danny.vn/vngcloud/internal/testutil"
	"danny.vn/vngcloud/internal/transport"
)

func TestIAMUserHarnessGoogleGate(t *testing.T) {
	for _, tc := range []struct {
		write, user, google string
		want                bool
	}{
		{"1", "1", "1", true}, {"", "1", "1", false}, {"1", "", "1", false},
		{"1", "1", "true", false}, {"1", "1", " 1", false}, {"1", "1", "", false},
	} {
		if iamUserGoogleGates(tc.write, tc.user, tc.google) != tc.want {
			t.Fatal("Google gate mismatch")
		}
	}
}

func TestIAMUserHarnessGoogleLifecycle(t *testing.T) {
	for _, fail := range []string{"", "setup", "verify", "mutation", "identity", "seed-echo"} {
		t.Run(fail, func(t *testing.T) {
			verified, enabled, setup := false, false, false
			mutations, setups, verifies, captures := 0, 0, 0, 0
			cfg := testutil.NewConfigWithCapture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				path := r.URL.Path
				switch {
				case strings.HasSuffix(path, "/set-up-google"):
					setups++
					setup = true
					if fail == "setup" {
						w.WriteHeader(500)
						return
					}
					_, _ = w.Write([]byte(`{"secret":"JBSWY3DPEHPK3PXP","qrCodeUrl":"https://qr.invalid/canary","unknown":"secret-canary"}`))
				case strings.HasSuffix(path, "/verify-google"):
					verifies++
					var body map[string]string
					if json.NewDecoder(r.Body).Decode(&body) != nil || body["token"] != "123456" || len(body) != 1 {
						t.Error("verification body mismatch")
					}
					if fail == "verify" {
						w.WriteHeader(500)
						return
					}
					verified = true
					enabled = true
					w.WriteHeader(204)
				case strings.HasSuffix(path, "/activate-google"), strings.HasSuffix(path, "/deactivate-google"):
					mutations++
					if fail == "mutation" {
						w.WriteHeader(403)
						return
					}
					enabled = strings.HasSuffix(path, "/activate-google")
					w.WriteHeader(204)
				case strings.HasSuffix(path, "/auth/userinfo"):
					_, _ = w.Write([]byte(`{"userId":"caller","userType":"iam-user"}`))
				case strings.HasSuffix(path, "/actions"):
					_, _ = w.Write([]byte(`[{"action":"iam:CreatePolicy"}]`))
				case strings.HasSuffix(path, "/policies"):
					_, _ = w.Write([]byte(`{"data":[],"pageNumber":0,"pageSize":1000,"totalItems":0,"totalPages":0}`))
				case strings.HasSuffix(path, "/groups"):
					_, _ = w.Write([]byte(`[]`))
				case strings.HasSuffix(path, "/iam-users/owned"):
					name := "created-name"
					if fail == "identity" {
						name = "other"
					}
					if setup && fail == "seed-echo" {
						name = "prefix-jbsw y3dp-ehpk3pxp=-suffix"
					}
					_, _ = fmt.Fprintf(w, `{"id":"owned","username":%q,"from":"iam","twoFactorAuth":{"googleAuthenticator":{"verified":%t,"enabled":%t}},"unknown":"token-qr-canary"}`, name, verified, enabled)
				default:
					t.Error("unexpected request or QR fetch")
					w.WriteHeader(400)
				}
			}), func(transport.Capture) { captures++ })
			h := newIAMUserGoogle(cfg)
			h.code = func(context.Context, string) (string, error) { return "123456", nil }
			ledger := &iamUserLedger{ID: "owned", Name: "created-name"}
			ok := h.run(context.Background(), iam.New(cfg), ledger)
			if ok != (fail == "") {
				t.Fatal("lifecycle outcome mismatch")
			}
			if fail == "" && (setups != 1 || verifies != 1 || mutations != 3 || enabled || !verified) {
				t.Fatal("lifecycle call or state mismatch")
			}
			if fail == "identity" && setups != 0 || fail == "setup" && verifies != 0 || fail == "verify" && mutations != 0 || fail == "mutation" && mutations != 1 {
				t.Fatal("failed step continued")
			}
			if captures != 0 {
				t.Fatal("sensitive response captured")
			}
			if fail != "identity" && !h.private {
				t.Fatal("privacy not latched before setup")
			}
		})
	}
}

func TestIAMUserHarnessGoogleVerify(t *testing.T) {
	for _, status := range []int{204, 400, 401, 403, 404, 409, 429, 500, 307} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			calls, waits := 0, 0
			cfg := testutil.NewConfig(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.Header().Set("Location", "/forbidden")
				w.WriteHeader(status)
			}))
			h := newIAMUserGoogle(cfg)
			h.now = func() time.Time { return time.Unix(31, 0) }
			h.code = func(context.Context, string) (string, error) { return "123456", nil }
			h.wait = func(_ context.Context, d time.Duration) bool {
				waits++
				if d != 29*time.Second {
					t.Error("wrong wait")
				}
				return true
			}
			if h.verify(context.Background(), "owned", "synthetic") != (status == 204) {
				t.Fatal("verify result mismatch")
			}
			want := 1
			if status == 400 {
				want = 2
			}
			if calls != want || waits != want-1 {
				t.Fatal("verify retry bound mismatch")
			}
		})
	}
	t.Run("cancel", func(t *testing.T) {
		calls := 0
		cfg := testutil.NewConfig(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls++; w.WriteHeader(400) }))
		h := newIAMUserGoogle(cfg)
		h.code = func(context.Context, string) (string, error) { return "123456", nil }
		ctx, cancel := context.WithCancel(context.Background())
		h.wait = func(ctx context.Context, _ time.Duration) bool { cancel(); return iamUserGoogleWait(ctx, time.Second) }
		if h.verify(ctx, "owned", "synthetic") || calls != 1 {
			t.Fatal("cancellation continued")
		}
	})
}

func TestIAMUserHarnessGoogleQuota(t *testing.T) {
	for _, body := range []string{`{"usage":3,"limit":20}`, `{"usage":3.0,"limit":20}`, `{}`, `{"usage":"3","limit":20}`, `{"usage":null,"limit":20}`, `{"usage":3,"limit":false}`, `{"usage":3,"Usage":3,"limit":20}`, `{"usage":3,"limit":20} {}`, strings.Repeat("x", (256<<10)+1)} {
		t.Run(fmt.Sprint(len(body), body[:min(len(body), 20)]), func(t *testing.T) {
			cfg := testutil.NewConfigWithCapture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !strings.HasSuffix(r.URL.Path, "/quotas/iam-user") {
					t.Error("wrong quota path")
				}
				_, _ = w.Write([]byte(body))
			}), func(transport.Capture) { t.Error("quota captured") })
			h := newIAMUserGoogle(cfg)
			got, ok := h.quota(context.Background())
			want := body == `{"usage":3,"limit":20}` || body == `{"usage":3.0,"limit":20}`
			if ok != want || ok && !iamUserQuotaEqual(got, "3") || ok && iamUserQuotaEqual(got, "4") {
				t.Fatal("quota validation mismatch")
			}
		})
	}
}

// Test-only helpers keep enrolment out of the public SDK.
type iamUserGoogle struct {
	c       *core.Client
	private bool
	secrets []string
	seed    string
	now     func() time.Time
	wait    func(context.Context, time.Duration) bool
	code    func(context.Context, string) (string, error)
}

func iamUserGoogleGates(write, user, google string) bool {
	return iamUserGates(write, user) && google == "1"
}

func newIAMUserGoogle(cfg vngcloud.Config) *iamUserGoogle {
	return &iamUserGoogle{c: core.ClientOf(cfg), now: time.Now, wait: iamUserGoogleWait,
		code: func(ctx context.Context, seed string) (string, error) {
			return (&vngcloud.SecretTOTP{Secret: seed}).GetCode(ctx)
		}}
}

func (h *iamUserGoogle) request(ctx context.Context, method string, parts []string, body, out any, maxBody int64, ok int) (int, bool) {
	if ctx.Err() != nil {
		return 0, false
	}
	var sent []string
	defer clear(sent)
	status, _, raw, err := h.c.DoRaw(ctx, transport.Request{
		Operation: "iam.LiveUserGoogle", Method: method,
		URL:  h.c.RouteURL(routes.Route{Product: routes.ProductDashboard, Version: "accounts-api/v1", Parts: parts}),
		Body: body, OK: []int{ok}, Once: true, NoRedirect: true, Sensitive: true,
		StrictJSON: out != nil, MaxBody: maxBody, KeepObservedStatus: true,
		WithholdMessage: "live response withheld", ClassifyError: func(int, string) string { return "RequestFailed" },
		Redact: h.secrets, SentCredentials: &sent,
	})
	defer clear(raw)
	h.secrets = append(h.secrets, sent...)
	if err != nil {
		return 0, false
	}
	if status != ok {
		return status, false
	}
	if out != nil && (jsonresponse.ValidateStrict(raw, out) != nil || json.Unmarshal(raw, out) != nil) {
		return status, false
	}
	return status, true
}

func (h *iamUserGoogle) run(ctx context.Context, client *iam.Client, ledger *iamUserLedger) bool {
	if ledger.ID == "" || ledger.StopCleanup || ledger.DeleteAttempted || ledger.Gone || !h.read(ctx, client, ledger, nil, nil) {
		return false
	}
	// Latch before sending: even a lost setup response can carry remote enrolment.
	h.private = true
	var setup struct {
		Secret string `json:"secret"`
		QR     string `json:"qrCodeUrl"`
	}
	_, ok := h.request(ctx, http.MethodPost, []string{"iam-users", ledger.ID, "set-up-google"}, nil, &setup, 64<<10, 200)
	h.seed = iamUserGoogleNormalize(setup.Secret)
	h.secrets = append(h.secrets, setup.Secret, setup.QR)
	seed := setup.Secret
	setup.Secret = ""
	setup.QR = ""
	if !ok || seed == "" || h.secrets[len(h.secrets)-1] == "" || !h.verify(ctx, ledger.ID, seed) || !h.read(ctx, client, ledger, vngcloud.Ptr(true), vngcloud.Ptr(true)) {
		return false
	}
	for _, step := range []struct{ enabled, changed bool }{
		{true, false}, {false, true}, {false, false}, {true, true}, {true, false}, {false, true},
	} {
		if ctx.Err() != nil {
			return false
		}
		var method string
		var changed bool
		var enabled *bool
		if step.enabled {
			out, err := client.EnableUserMFA(ctx, &iam.EnableUserMFAInput{UserID: ledger.ID, Method: "google"})
			if err != nil || out == nil {
				return false
			}
			method, changed, enabled = out.Method, out.Changed, out.Enabled
		} else {
			out, err := client.DisableUserMFA(ctx, &iam.DisableUserMFAInput{UserID: ledger.ID, Method: "google"})
			if err != nil || out == nil {
				return false
			}
			method, changed, enabled = out.Method, out.Changed, out.Enabled
		}
		if method != "google" || changed != step.changed || enabled == nil || *enabled != step.enabled || !h.read(ctx, client, ledger, vngcloud.Ptr(true), &step.enabled) {
			return false
		}
	}
	return true
}

func (h *iamUserGoogle) read(ctx context.Context, client *iam.Client, l *iamUserLedger, verified, enabled *bool) bool {
	out, err := client.GetUser(ctx, &iam.GetUserInput{UserID: l.ID})
	if err != nil || out == nil || !strings.EqualFold(out.User.ID, l.ID) || out.User.Username != l.Name || out.User.From != "iam" || h.containsSecret(out.User.ID) || h.containsSecret(out.User.Username) || out.User.MFA == nil {
		return false
	}
	factor := out.User.MFA.GoogleAuthenticator
	return factor != nil && factor.Verified != nil && factor.Enabled != nil &&
		(verified == nil || *factor.Verified == *verified) && (enabled == nil || *factor.Enabled == *enabled)
}

func (h *iamUserGoogle) verify(ctx context.Context, id, seed string) bool {
	for attempt := range 2 {
		if ctx.Err() != nil {
			return false
		}
		token, err := h.code(ctx, seed)
		if err != nil || len(token) != 6 || strings.IndexFunc(token, func(r rune) bool { return r < '0' || r > '9' }) != -1 {
			return false
		}
		h.secrets = append(h.secrets, token)
		status, ok := h.request(ctx, http.MethodPost, []string{"iam-users", id, "verify-google"}, map[string]string{"token": token}, nil, 64<<10, 204)
		if ok && status == 204 {
			return true
		}
		if attempt != 0 || status != 400 {
			return false
		}
		current := h.now()
		if !h.wait(ctx, current.Truncate(30*time.Second).Add(30*time.Second).Sub(current)) {
			return false
		}
	}
	return false
}

func iamUserGoogleWait(ctx context.Context, delay time.Duration) bool {
	if ctx.Err() != nil {
		return false
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return ctx.Err() == nil
	}
}

func iamUserGoogleNormalize(value string) string {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(strings.ToLower(value), "otpauth://") {
		if parsed, err := url.Parse(value); err == nil && parsed.Query().Get("secret") != "" {
			value = parsed.Query().Get("secret")
		}
	}
	return strings.ToUpper(strings.TrimRight(strings.NewReplacer(" ", "", "-", "").Replace(value), "="))
}

func (h *iamUserGoogle) containsSecret(value string) bool {
	for _, secret := range h.secrets {
		if secret != "" && strings.Contains(strings.ToUpper(value), strings.ToUpper(secret)) {
			return true
		}
	}
	normalized := strings.NewReplacer(" ", "", "-", "", "=", "").Replace(iamUserGoogleNormalize(value))
	return h.seed != "" && strings.Contains(normalized, h.seed)
}

func (h *iamUserGoogle) quota(ctx context.Context) (json.Number, bool) {
	var out struct {
		Usage json.RawMessage `json:"usage"`
		Limit json.RawMessage `json:"limit"`
	}
	_, ok := h.request(ctx, http.MethodGet, []string{"quotas", "iam-user"}, nil, &out, 256<<10, 200)
	defer clear(out.Usage)
	defer clear(out.Limit)
	if !ok {
		return "", false
	}
	usage, ok := iamUserQuotaNumber(out.Usage)
	_, limitOK := iamUserQuotaNumber(out.Limit)
	return usage, ok && limitOK
}

func iamUserQuotaNumber(raw []byte) (json.Number, bool) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if decoder.Decode(&value) != nil {
		return "", false
	}
	number, ok := value.(json.Number)
	return number, ok
}

func iamUserQuotaEqual(a, b json.Number) bool {
	first, exponent, ok := iamUserQuotaDecimal(a)
	second, otherExponent, otherOK := iamUserQuotaDecimal(b)
	return ok && otherOK && first == second && exponent.Cmp(otherExponent) == 0
}

// Keep the coefficient and decimal exponent separate. Expanding 10^exponent
// makes short peer-controlled numbers expensive despite the response byte cap.
func iamUserQuotaDecimal(number json.Number) (string, *big.Int, bool) {
	text := string(number)
	if len(text) == 0 || len(text) > 256<<10 || strings.TrimSpace(text) != text ||
		(text[0] != '-' && (text[0] < '0' || text[0] > '9')) || !json.Valid([]byte(text)) {
		return "", nil, false
	}
	mantissa := text
	exponent := new(big.Int)
	if pos := strings.IndexAny(text, "eE"); pos != -1 {
		mantissa = text[:pos]
		if _, ok := exponent.SetString(text[pos+1:], 10); !ok {
			return "", nil, false
		}
	}
	negative := strings.HasPrefix(mantissa, "-")
	mantissa = strings.TrimPrefix(mantissa, "-")
	fraction := 0
	if pos := strings.IndexByte(mantissa, '.'); pos != -1 {
		fraction = len(mantissa) - pos - 1
		mantissa = mantissa[:pos] + mantissa[pos+1:]
	}
	coefficient := strings.TrimLeft(mantissa, "0")
	if coefficient == "" {
		return "0", new(big.Int), true
	}
	trimmed := strings.TrimRight(coefficient, "0")
	exponent.Add(exponent, big.NewInt(int64(len(coefficient)-len(trimmed)-fraction)))
	if negative {
		trimmed = "-" + trimmed
	}
	return trimmed, exponent, true
}

func (h *iamUserGoogle) clear() { clear(h.secrets); h.secrets = nil; h.seed = "" }

func TestIAMUserHarnessGoogleSetupBounds(t *testing.T) {
	for _, tc := range []struct {
		label, body string
		status      int
	}{
		{"unknown-fields", `{"secret":"JBSWY3DPEHPK3PXP","qrCodeUrl":"https://qr.invalid/secret","TOKEN":"token-canary","123456":"code-canary","seed":{"unknown":"seed-canary"}}`, 200},
		{"missing-qr", `{"secret":"JBSWY3DPEHPK3PXP"}`, 200},
		{"alias", `{"secret":"JBSWY3DPEHPK3PXP","Secret":"unknown-canary","qrCodeUrl":"secret-canary"}`, 200},
		{"malformed", `{"secret":"secret-canary","qrCodeUrl":`, 200},
		{"oversize", strings.Repeat("x", (64<<10)+1), 200},
		{"null", `null`, 200}, {"redirect", `secret-canary`, 307},
		{"rejected", `{"secret":"secret-canary"}`, 400},
	} {
		t.Run(tc.label, func(t *testing.T) {
			setups, verifies, captures := 0, 0, 0
			var h *iamUserGoogle
			cfg := testutil.NewConfigWithCapture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case strings.HasSuffix(r.URL.Path, "/set-up-google"):
					setups++
					if !h.private {
						t.Error("setup sent before privacy")
					}
					if r.Body != nil {
						var body [1]byte
						if n, _ := r.Body.Read(body[:]); n != 0 {
							t.Error("setup body sent")
						}
					}
					w.Header().Set("Location", "/forbidden")
					w.WriteHeader(tc.status)
					_, _ = w.Write([]byte(tc.body))
				case strings.HasSuffix(r.URL.Path, "/verify-google"):
					verifies++
					w.WriteHeader(500)
				case strings.HasSuffix(r.URL.Path, "/iam-users/owned"):
					_, _ = w.Write([]byte(`{"id":"owned","username":"created-name","from":"iam","twoFactorAuth":{"googleAuthenticator":{"verified":false,"enabled":false}}}`))
				default:
					t.Error("unexpected request or QR fetch")
					w.WriteHeader(400)
				}
			}), func(transport.Capture) { captures++ })
			h = newIAMUserGoogle(cfg)
			h.code = func(context.Context, string) (string, error) { return "123456", nil }
			if h.run(context.Background(), iam.New(cfg), &iamUserLedger{ID: "owned", Name: "created-name"}) || setups != 1 || captures != 0 || !h.private {
				t.Fatal("unsafe setup outcome")
			}
			wantVerify := 0
			if tc.label == "unknown-fields" {
				wantVerify = 1
			}
			if verifies != wantVerify {
				t.Fatal("malformed setup continued")
			}
		})
	}
}

func TestIAMUserHarnessGoogleUncertainVerify(t *testing.T) {
	for _, status := range []int{400, 204} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			calls := 0
			cfg := testutil.NewConfig(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls++
				w.Header().Set("Content-Length", "99")
				w.WriteHeader(status)
				if status == 400 {
					_, _ = w.Write([]byte("short"))
				} else {
					_, _ = w.Write([]byte(strings.Repeat("x", (64<<10)+1)))
				}
			}))
			h := newIAMUserGoogle(cfg)
			h.code = func(context.Context, string) (string, error) { return "123456", nil }
			h.wait = func(context.Context, time.Duration) bool { t.Error("uncertain verify waited"); return false }
			// A 204 response cannot have an HTTP body. The 400 truncated body is uncertain.
			ok := h.verify(context.Background(), "owned", "synthetic")
			if status == 400 && ok || calls != 1 {
				t.Fatal("uncertain verification retried")
			}
		})
	}
}

func TestIAMUserHarnessGoogleAttribution(t *testing.T) {
	cfg := testutil.NewConfig(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { t.Error("unattributed request"); w.WriteHeader(400) }))
	h := newIAMUserGoogle(cfg)
	for _, ledger := range []*iamUserLedger{
		{Name: "created-name", CandidateID: "candidate"},
		{ID: "owned", StopCleanup: true}, {ID: "owned", DeleteAttempted: true}, {ID: "owned", Gone: true},
	} {
		if h.run(context.Background(), iam.New(cfg), ledger) {
			t.Fatal("unattributed enrolment")
		}
	}
}

func TestIAMUserHarnessGoogleCleanupPrivacy(t *testing.T) {
	for _, echo := range []string{"JBSWY3DPEHPK3PXP", "jbswy3dpehpk3pxp", "jbsw y3dp-ehpk3pxp=", "123456", "https://qr.invalid/canary", "token-canary"} {
		t.Run(strconv.Itoa(len(echo)), func(t *testing.T) {
			gone := false
			captures, deletes := 0, 0
			cfg := iamUserPrivateTestConfig(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case strings.HasSuffix(r.URL.Path, "/quotas/iam-user"):
					_, _ = fmt.Fprintf(w, `{"usage":3.0,"limit":20,"unknown":%q}`, echo)
				case r.Method == http.MethodDelete && strings.HasSuffix(r.URL.Path, "/iam-users/owned"):
					deletes++
					gone = true
					w.WriteHeader(204)
				case strings.HasSuffix(r.URL.Path, "/auth/userinfo"):
					_, _ = fmt.Fprintf(w, `{"userId":"caller","userType":"iam-user","unknown":%q}`, echo)
				case strings.HasSuffix(r.URL.Path, "/iam-users/owned"):
					if gone {
						w.WriteHeader(404)
						return
					}
					_, _ = fmt.Fprintf(w, `{"id":"owned","username":"created-name","from":"iam","unknown":%q}`, echo)
				default:
					t.Error("unexpected cleanup request")
					w.WriteHeader(400)
				}
			}), vngcloud.WithResponseCapture(func(vngcloud.ResponseCapture) { captures++ }))
			h := newIAMUserGoogle(cfg)
			h.private = true
			h.seed = "JBSWY3DPEHPK3PXP"
			h.secrets = []string{"123456", "https://qr.invalid/canary", "token-canary"}
			if !h.containsSecret("prefix-" + echo + "-suffix") {
				t.Fatal("secret variant not recognized")
			}
			ledger := &iamUserLedger{ID: "owned", Name: "created-name"}
			if !iamUserDeleteOwned(context.Background(), iam.New(cfg), ledger) || deletes != 1 {
				t.Fatal("attributable cleanup failed")
			}
			usage, ok := h.quota(context.Background())
			if !ok || !iamUserQuotaEqual(usage, "3") || iamUserQuotaEqual(usage, "4") {
				t.Fatal("cleanup quota equality failed")
			}
			if captures != 0 {
				t.Fatalf("cleanup response captures=%d", captures)
			}
		})
	}
}

func TestIAMUserHarnessGoogleQuotaUnavailable(t *testing.T) {
	for _, status := range []int{307, 400, 401, 403, 404, 429, 500} {
		cfg := testutil.NewConfigWithCapture(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Location", "/forbidden")
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"usage":3,"limit":20,"unknown":"secret-canary"}`))
		}), func(transport.Capture) { t.Error("unavailable quota captured") })
		if _, ok := newIAMUserGoogle(cfg).quota(context.Background()); ok {
			t.Fatal("unavailable quota accepted")
		}
	}
}

func TestIAMUserHarnessQuotaExactDecimal(t *testing.T) {
	for _, tc := range []struct {
		label string
		a, b  json.Number
		equal bool
	}{
		{"decimal", "3.000", "3e0", true},
		{"negative", "-0.03", "-3e-2", true},
		{"signed-zero", "-0e999999999999999999999", "0", true},
		{"large-positive-exponent", "1e999999999999999999999", "10e999999999999999999998", true},
		{"large-negative-exponent", "1e-999999999999999999999", "0.1e-999999999999999999998", true},
		{"different", "1e999999999999999999999", "2e999999999999999999999", false},
		{"fraction-is-not-json", "1/2", "0.5", false},
		{"hex-is-not-json", "0x10", "16", false},
		{"trailing-json", "3 4", "3", false},
	} {
		t.Run(tc.label, func(t *testing.T) {
			if iamUserQuotaEqual(tc.a, tc.b) != tc.equal {
				t.Fatal("exact decimal equality mismatch")
			}
		})
	}
}

// Use the same final options as the live configuration, with loopback endpoints
// and a synthetic token so no profile, environment file, or login is needed.
func iamUserPrivateTestConfig(t *testing.T, handler http.Handler, options ...vngcloud.LoadOption) vngcloud.Config {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	options = append(options, vngcloud.WithRegion("hcm-3"), vngcloud.WithStaticToken("token-canary"),
		vngcloud.WithRetry(0, 0), vngcloud.WithHTTPClient(server.Client()),
		vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{Dashboard: server.URL, IAM: server.URL}))
	cfg, err := vngcloud.NewConfig(iamUserPrivateOptions(options...)...)
	if err != nil {
		t.Fatal("offline private configuration failed")
	}
	return cfg
}

func TestIAMUserHarnessGooglePrivateWiring(t *testing.T) {
	for _, fail := range []string{"", "setup-malformed", "setup-lost", "guard", "mutation", "delete", "quota-unavailable", "quota-malformed", "quota-unequal"} {
		t.Run(fail, func(t *testing.T) {
			const echo = "unknown-seed-code-qr-token-canary"
			dir := t.TempDir()
			var logs bytes.Buffer
			gone, verified, enabled := false, false, false
			setups, mutations, deletes, quotaReads, captures := 0, 0, 0, 0, 0
			edges := make(map[string]bool)
			cfg := iamUserPrivateTestConfig(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				path := r.URL.Path
				edges[path] = true
				write := func(body map[string]any) {
					body[echo] = echo
					_ = json.NewEncoder(w).Encode(body)
				}
				switch {
				case strings.HasSuffix(path, "/quotas/iam-user"):
					quotaReads++
					if gone && fail == "quota-unavailable" {
						w.WriteHeader(503)
					}
					switch {
					case gone && fail == "quota-malformed":
						write(map[string]any{"usage": "3", "limit": 20})
					case gone && fail == "quota-unequal":
						write(map[string]any{"usage": 4, "limit": 20})
					default:
						write(map[string]any{"usage": 3, "limit": 20})
					}
				case strings.HasSuffix(path, "/set-up-google"):
					setups++
					if fail == "setup-malformed" || fail == "setup-lost" {
						if fail == "setup-lost" {
							w.Header().Set("Content-Length", "999")
						}
						_, _ = w.Write([]byte(`{"secret":"seed-canary",`))
						return
					}
					write(map[string]any{"secret": "JBSWY3DPEHPK3PXP", "qrCodeUrl": "https://qr.invalid/canary"})
				case strings.HasSuffix(path, "/verify-google"):
					verified, enabled = true, true
					w.WriteHeader(204)
				case strings.HasSuffix(path, "/activate-google"), strings.HasSuffix(path, "/deactivate-google"):
					mutations++
					if fail == "mutation" {
						w.WriteHeader(409)
						write(map[string]any{"message": echo, "code": echo})
						return
					}
					enabled = strings.HasSuffix(path, "/activate-google")
					w.WriteHeader(204)
				case r.Method == http.MethodDelete && strings.HasSuffix(path, "/iam-users/owned"):
					deletes++
					if fail == "delete" {
						w.WriteHeader(409)
						write(map[string]any{"message": echo, "code": echo})
						return
					}
					gone = true
					w.WriteHeader(204)
				case strings.HasSuffix(path, "/auth/userinfo"):
					write(map[string]any{"userId": "caller", "userType": "iam-user"})
				case strings.HasSuffix(path, "/actions"):
					if fail == "guard" {
						w.WriteHeader(403)
						write(map[string]any{"message": echo, "code": echo})
						return
					}
					_ = json.NewEncoder(w).Encode([]map[string]any{{"action": "iam:CreatePolicy", echo: echo}})
				case strings.HasSuffix(path, "/policies/direct"), strings.HasSuffix(path, "/policies/group-policy"):
					write(map[string]any{"id": path[strings.LastIndex(path, "/")+1:], "statements": []any{map[string]any{"effect": "deny", "actions": []string{"iam:CreatePolicy"}}}})
				case strings.HasSuffix(path, "/policies"):
					write(map[string]any{"data": []any{map[string]any{"id": "direct", echo: echo}}, "pageNumber": 0, "pageSize": 1000, "totalItems": 1, "totalPages": 1})
				case strings.HasSuffix(path, "/groups"):
					_ = json.NewEncoder(w).Encode([]map[string]any{{"id": "group", "policies": []string{"group-policy"}, echo: echo}})
				case strings.HasSuffix(path, "/iam-users/owned"):
					if gone {
						w.WriteHeader(404)
						write(map[string]any{"message": echo, "code": echo})
						return
					}
					write(map[string]any{"id": "owned", "username": "created-name", "from": "iam", "twoFactorAuth": map[string]any{"googleAuthenticator": map[string]bool{"verified": verified, "enabled": enabled}}})
				default:
					t.Error("unexpected request or QR fetch")
					w.WriteHeader(400)
				}
			}), vngcloud.WithResponseCapture(func(c vngcloud.ResponseCapture) {
				captures++
				if os.WriteFile(filepath.Join(dir, "capture"), c.Body, 0o600) != nil {
					t.Error("capture sentinel failed")
				}
			}), vngcloud.WithLogger(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))))
			h := newIAMUserGoogle(cfg)
			t.Cleanup(h.clear)
			h.code = func(context.Context, string) (string, error) { return "123456", nil }
			before, ok := h.quota(context.Background())
			if !ok {
				t.Fatal("synthetic quota baseline unavailable")
			}
			ledger := &iamUserLedger{ID: "owned", Name: "created-name", Attempted: true}
			ran := h.run(context.Background(), iam.New(cfg), ledger)
			wantRun := fail == "" || strings.HasPrefix(fail, "quota-") || fail == "delete"
			if ran != wantRun || setups != 1 || !h.private {
				t.Fatal("private lifecycle or setup boundary failed")
			}
			wantMutations := 3
			if strings.HasPrefix(fail, "setup-") || fail == "guard" {
				wantMutations = 0
			} else if fail == "mutation" {
				wantMutations = 1
			}
			if mutations != wantMutations {
				t.Fatal("failed private sequence continued")
			}
			// A fresh SDK client forces cleanup's caller read even after warm guards.
			cleaned := iamUserDeleteOwned(context.Background(), iam.New(cfg), ledger)
			if cleaned != (fail != "delete") || deletes != 1 || ledger.Gone != cleaned {
				t.Fatal("private cleanup outcome failed")
			}
			if cleaned {
				after, available := h.quota(context.Background())
				restored := available && iamUserQuotaEqual(before, after)
				if restored != !strings.HasPrefix(fail, "quota-") || quotaReads != 2 {
					t.Fatal("private quota restoration failed")
				}
			} else if quotaReads != 1 {
				t.Fatal("unresolved cleanup checked quota")
			}
			if fail == "" && (!edges["/policies-api/v1/policies/direct"] || !edges["/policies-api/v1/policies/group-policy"]) {
				t.Fatal("full SDK guard graph not exercised")
			}
			if iamUserSaveLedger(filepath.Join(dir, "ledger"), ledger) != nil {
				t.Fatal("synthetic ledger write failed")
			}
			entries, err := os.ReadDir(dir)
			if err != nil || len(entries) != 1 || entries[0].Name() != "ledger" || captures != 0 || logs.Len() != 0 {
				t.Fatal("private wiring emitted output or a response file")
			}
			raw, err := os.ReadFile(filepath.Join(dir, "ledger"))
			if err != nil || bytes.Contains(raw, []byte(echo)) || bytes.Contains(raw, []byte("JBSWY3DPEHPK3PXP")) || bytes.Contains(raw, []byte("123456")) || bytes.Contains(raw, []byte("https://qr.invalid/canary")) {
				t.Fatal("ledger retained enrolment material")
			}
		})
	}
}

func TestIAMUserHarnessGoogleFreshWindow(t *testing.T) {
	calls, generated, waits := 0, 0, 0
	cfg := testutil.NewConfig(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var body map[string]string
		if json.NewDecoder(r.Body).Decode(&body) != nil {
			t.Error("verification body invalid")
		}
		if calls == 1 {
			if body["token"] != "123456" {
				t.Error("first verification code mismatch")
			}
			w.WriteHeader(400)
			return
		}
		if body["token"] != "654321" || waits != 1 {
			t.Error("verification reused the old window")
		}
		w.WriteHeader(204)
	}))
	h := newIAMUserGoogle(cfg)
	h.now = func() time.Time { return time.Unix(31, 0) }
	h.wait = func(context.Context, time.Duration) bool { waits++; return true }
	h.code = func(context.Context, string) (string, error) {
		generated++
		if generated == 1 {
			return "123456", nil
		}
		return "654321", nil
	}
	if !h.verify(context.Background(), "owned", "synthetic") || calls != 2 || generated != 2 || waits != 1 {
		t.Fatal("fresh-window verification failed")
	}
}

func TestIAMUserHarnessQuotaExponentBound(t *testing.T) {
	// Exponent text stays bounded by the response, independent of its value.
	exponent := strings.Repeat("9", 4096)
	value := json.Number("1e" + exponent)
	if !iamUserQuotaEqual(value, value) || iamUserQuotaEqual(value, json.Number("2e"+exponent)) {
		t.Fatal("large exponent comparison failed")
	}
	if iamUserQuotaEqual(json.Number(strings.Repeat("1", (256<<10)+1)), "1") {
		t.Fatal("quota number byte bound ignored")
	}
}
