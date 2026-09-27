package loadbalancer

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/testutil"
	"danny.vn/vngcloud/internal/transport"
)

// fixtureCertPEM and fixtureKeyPEM are a throwaway self-signed certificate
// and its RSA private key, generated once for this test binary rather than
// committed to testdata: a committed key, even a fake one, risks tripping a
// secret scanner and needs no allowlist this way. Nothing here is a real
// credential; the key never leaves this process.
var fixtureCertPEM, fixtureKeyPEM = generateFixtureCertAndKey()

func generateFixtureCertAndKey() (certPEM, keyPEM string) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "vngcloud-test.invalid"},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		panic(err)
	}
	cert := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	pkey := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	return string(cert), string(pkey)
}

func TestLoadBalancerImportCertificateRequestBodyTLS(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/v2/project-1/cas" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["name"] != "example-com" || body["type"] != CertificateTypeTLS {
			t.Fatalf("unexpected body: %+v", body)
		}
		if body["certificate"] != fixtureCertPEM {
			t.Fatalf("body certificate does not hold the fixture certificate")
		}
		if body["privateKey"] != fixtureKeyPEM {
			t.Fatalf("body privateKey does not hold the fixture key in the clear")
		}
		if body["privateKey"] == "[redacted]" {
			t.Fatal("body privateKey was redacted; the server would receive that literal text as the key")
		}
		if _, ok := body["certificateChain"]; ok {
			t.Fatalf("body holds an empty certificateChain: %+v", body)
		}
		if _, ok := body["passphrase"]; ok {
			t.Fatalf("body holds an empty passphrase: %+v", body)
		}
		w.WriteHeader(http.StatusCreated)
		testutil.WriteFixture(t, w, "../testdata/loadbalancer/import_certificate.json")
	}))

	out, err := c.ImportCertificate(context.Background(), &ImportCertificateInput{
		Name:        "example-com",
		Type:        CertificateTypeTLS,
		Certificate: fixtureCertPEM,
		PrivateKey:  vngcloud.Secret(fixtureKeyPEM),
	})
	if err != nil {
		t.Fatalf("ImportCertificate() error = %v", err)
	}
	if out.Certificate.UUID != "cert-1" || len(out.Certificate.SubjectAlternativeNames) != 2 {
		t.Fatalf("unexpected certificate: %+v", out.Certificate)
	}
}

func TestLoadBalancerImportCertificateRequestBodyChainAndPassphrase(t *testing.T) {
	const passphrase = "correct-horse-battery-staple"
	chain := fixtureCertPEM + fixtureCertPEM

	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["certificateChain"] != chain {
			t.Fatalf("unexpected certificateChain: %v", body["certificateChain"])
		}
		if body["passphrase"] != passphrase {
			t.Fatalf("body passphrase does not hold the fixture passphrase in the clear")
		}
		w.WriteHeader(http.StatusCreated)
		testutil.WriteFixture(t, w, "../testdata/loadbalancer/import_certificate.json")
	}))

	_, err := c.ImportCertificate(context.Background(), &ImportCertificateInput{
		Name:             "example-com",
		Type:             CertificateTypeTLS,
		Certificate:      fixtureCertPEM,
		CertificateChain: chain,
		PrivateKey:       vngcloud.Secret(fixtureKeyPEM),
		Passphrase:       vngcloud.Secret(passphrase),
	})
	if err != nil {
		t.Fatalf("ImportCertificate() error = %v", err)
	}
}

func TestLoadBalancerImportCertificateRequestBodyCA(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["type"] != CertificateTypeCA {
			t.Fatalf("unexpected type: %v", body["type"])
		}
		for _, field := range []string{"privateKey", "passphrase", "certificateChain"} {
			if _, ok := body[field]; ok {
				t.Fatalf("body holds %s for a CA certificate: %+v", field, body)
			}
		}
		w.WriteHeader(http.StatusCreated)
		testutil.WriteFixture(t, w, "../testdata/loadbalancer/import_certificate.json")
	}))

	_, err := c.ImportCertificate(context.Background(), &ImportCertificateInput{
		Name:        "example-ca",
		Type:        CertificateTypeCA,
		Certificate: fixtureCertPEM,
	})
	if err != nil {
		t.Fatalf("ImportCertificate() error = %v", err)
	}
}

func TestLoadBalancerImportCertificateShapeRefusals(t *testing.T) {
	notAPEM := "just some ordinary text, not PEM at all"
	keyText := fixtureKeyPEM

	tests := map[string]*ImportCertificateInput{
		"no PEM block": {
			Name: "n", Type: CertificateTypeTLS, Certificate: notAPEM, PrivateKey: vngcloud.Secret(fixtureKeyPEM),
		},
		"key text in Certificate": {
			Name: "n", Type: CertificateTypeTLS, Certificate: keyText, PrivateKey: vngcloud.Secret(fixtureKeyPEM),
		},
		"key text in CertificateChain": {
			Name: "n", Type: CertificateTypeTLS, Certificate: fixtureCertPEM, CertificateChain: keyText, PrivateKey: vngcloud.Secret(fixtureKeyPEM),
		},
		"a CERTIFICATE block as the key": {
			Name: "n", Type: CertificateTypeTLS, Certificate: fixtureCertPEM, PrivateKey: vngcloud.Secret(fixtureCertPEM),
		},
		"TLS/SSL without a key": {
			Name: "n", Type: CertificateTypeTLS, Certificate: fixtureCertPEM,
		},
		"CA with a key": {
			Name: "n", Type: CertificateTypeCA, Certificate: fixtureCertPEM, PrivateKey: vngcloud.Secret(fixtureKeyPEM),
		},
		"CA with a chain": {
			Name: "n", Type: CertificateTypeCA, Certificate: fixtureCertPEM, CertificateChain: fixtureCertPEM,
		},
		"CA with a passphrase": {
			Name: "n", Type: CertificateTypeCA, Certificate: fixtureCertPEM, Passphrase: vngcloud.Secret("hunter2"),
		},
		"passphrase without a key": {
			Name: "n", Type: CertificateTypeTLS, Certificate: fixtureCertPEM, Passphrase: vngcloud.Secret("hunter2"),
		},
	}

	for name, in := range tests {
		t.Run(name, func(t *testing.T) {
			c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				t.Fatal("no request expected")
			}))
			_, err := c.ImportCertificate(context.Background(), in)
			if !errors.Is(err, vngcloud.ErrInvalidInput) {
				t.Fatalf("err = %v, want ErrInvalidInput", err)
			}
			if strings.Contains(err.Error(), fixtureKeyPEM) || strings.Contains(err.Error(), fixtureCertPEM) {
				t.Fatalf("error echoes fixture material: %v", err)
			}
		})
	}
}

func TestLoadBalancerImportCertificateMissingUUID(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"data":{"name":"example-com"}}`))
	}))

	_, err := c.ImportCertificate(context.Background(), &ImportCertificateInput{
		Name: "example-com", Type: CertificateTypeCA, Certificate: fixtureCertPEM,
	})
	if err == nil {
		t.Fatal("ImportCertificate() error = nil, want an error for a response with no id")
	}
	if !strings.Contains(err.Error(), "list-certificates") {
		t.Fatalf("error does not name list-certificates: %v", err)
	}
}

func TestLoadBalancerImportCertificateFourXXNotWrapped(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"message":"name already exists"}`))
	}))

	_, err := c.ImportCertificate(context.Background(), &ImportCertificateInput{
		Name: "example-com", Type: CertificateTypeCA, Certificate: fixtureCertPEM,
	})
	var apiErr *vngcloud.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v, want *vngcloud.APIError", err)
	}
	if strings.Contains(err.Error(), "list-certificates") {
		t.Fatalf("a 4xx error should not get the ambiguous-import hint: %v", err)
	}
}

func TestLoadBalancerImportCertificateNoRetryOn502(t *testing.T) {
	var calls int32
	c := New(testutil.NewRetryConfig(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusBadGateway)
	})))

	_, err := c.ImportCertificate(context.Background(), &ImportCertificateInput{
		Name: "example-com", Type: CertificateTypeCA, Certificate: fixtureCertPEM,
	})
	if err == nil {
		t.Fatal("ImportCertificate() error = nil, want an error")
	}
	if calls != 1 {
		t.Fatalf("server received %d request(s), want 1 (no retry after 502)", calls)
	}
	if !strings.Contains(err.Error(), "list-certificates") {
		t.Fatalf("error does not name list-certificates: %v", err)
	}
}

// TestLoadBalancerImportCertificateNeverCaptured checks that
// ImportCertificate's Sensitive request never reaches the configured
// response-capture hook, while an ordinary read on the same Client still
// does.
func TestLoadBalancerImportCertificateNeverCaptured(t *testing.T) {
	var captured []transport.Capture
	cfg := testutil.NewConfigWithCapture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusCreated)
			testutil.WriteFixture(t, w, "../testdata/loadbalancer/import_certificate.json")
			return
		}
		testutil.WriteFixture(t, w, "../testdata/loadbalancer/get_certificate.json")
	}), func(c transport.Capture) {
		captured = append(captured, c)
	})
	c := New(cfg)

	if _, err := c.GetCertificate(context.Background(), &GetCertificateInput{CertificateID: "cert-1"}); err != nil {
		t.Fatalf("GetCertificate() error = %v", err)
	}
	if len(captured) != 1 {
		t.Fatalf("captured = %d after GetCertificate, want 1", len(captured))
	}

	_, err := c.ImportCertificate(context.Background(), &ImportCertificateInput{
		Name: "example-com", Type: CertificateTypeCA, Certificate: fixtureCertPEM,
	})
	if err != nil {
		t.Fatalf("ImportCertificate() error = %v", err)
	}
	if len(captured) != 1 {
		t.Fatalf("captured = %d after ImportCertificate, want still 1 (never captured)", len(captured))
	}
}

// TestLoadBalancerImportCertificateRedaction checks that a 400 whose message
// quotes the whole key, one line of it, its JSON-escaped form, or the
// passphrase never leaks any of them into the returned error, --debug
// output, or a fmt/slog/json rendering of the Input.
func TestLoadBalancerImportCertificateRedaction(t *testing.T) {
	const passphrase = "correct-horse-battery-staple"
	keyLine := strings.Split(fixtureKeyPEM, "\n")[1]
	escapedKey, err := json.Marshal(fixtureKeyPEM)
	if err != nil {
		t.Fatal(err)
	}
	// Strip the surrounding quotes json.Marshal adds, so the message embeds
	// only the escaped content, as a server dumping a raw JSON field value
	// into text might.
	escapedKeyText := string(escapedKey[1 : len(escapedKey)-1])

	rejections := []string{
		fmt.Sprintf(`{"message":"invalid privateKey: %s"}`, mustJSONString(fixtureKeyPEM)),
		fmt.Sprintf(`{"message":"invalid privateKey line: %s"}`, mustJSONString(keyLine)),
		fmt.Sprintf(`{"message":"invalid privateKey: %s"}`, mustJSONString(escapedKeyText)),
		fmt.Sprintf(`{"message":"invalid passphrase: %s"}`, mustJSONString(passphrase)),
	}

	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, nil))

	for i, body := range rejections {
		t.Run(fmt.Sprintf("case-%d", i), func(t *testing.T) {
			logBuf.Reset()
			cfg := testutil.NewConfigWithLogger(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(body))
			}), logger)
			c := New(cfg)

			in := &ImportCertificateInput{
				Name:        "example-com",
				Type:        CertificateTypeTLS,
				Certificate: fixtureCertPEM,
				PrivateKey:  vngcloud.Secret(fixtureKeyPEM),
				Passphrase:  vngcloud.Secret(passphrase),
			}
			_, callErr := c.ImportCertificate(context.Background(), in)
			if callErr == nil {
				t.Fatal("ImportCertificate() error = nil, want the server's rejection")
			}
			for _, secret := range []string{fixtureKeyPEM, keyLine, escapedKeyText, passphrase} {
				if strings.Contains(callErr.Error(), secret) {
					t.Fatalf("error leaks secret material: %v", callErr)
				}
			}
			if strings.Contains(logBuf.String(), fixtureKeyPEM) || strings.Contains(logBuf.String(), passphrase) {
				t.Fatalf("debug log leaks secret material: %s", logBuf.String())
			}

			forms := []string{
				fmt.Sprintf("%v", in),
				fmt.Sprintf("%+v", in),
				fmt.Sprintf("%#v", in),
			}
			data, jsonErr := json.Marshal(in) //nolint:gosec // G117: PrivateKey and Passphrase are vngcloud.Secret; MarshalJSON redacts them, verified below
			if jsonErr != nil {
				t.Fatalf("json.Marshal(in) error = %v", jsonErr)
			}
			forms = append(forms, string(data))
			for _, form := range forms {
				if strings.Contains(form, fixtureKeyPEM) || strings.Contains(form, passphrase) {
					t.Fatalf("Input rendering leaks secret material: %s", form)
				}
			}
		})
	}
}

func mustJSONString(s string) string {
	data, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return string(data)
}

// TestLoadBalancerImportCertificateInputSecretRedaction checks that
// ImportCertificateInput's PrivateKey and Passphrase fields never leak
// through fmt verbs, slog, or json.Marshal of the whole Input, independent
// of any server response.
func TestLoadBalancerImportCertificateInputSecretRedaction(t *testing.T) {
	const passphrase = "correct-horse-battery-staple"
	in := &ImportCertificateInput{
		Name:        "example-com",
		Type:        CertificateTypeTLS,
		Certificate: fixtureCertPEM,
		PrivateKey:  vngcloud.Secret(fixtureKeyPEM),
		Passphrase:  vngcloud.Secret(passphrase),
	}

	forms := map[string]string{
		"%v":  fmt.Sprintf("%v", in),
		"%+v": fmt.Sprintf("%+v", in),
		"%#v": fmt.Sprintf("%#v", in),
	}
	for name, got := range forms {
		if strings.Contains(got, fixtureKeyPEM) || strings.Contains(got, passphrase) {
			t.Fatalf("%s = %q, holds secret material", name, got)
		}
		if !strings.Contains(got, "[redacted]") {
			t.Fatalf("%s = %q, want it to contain [redacted]", name, got)
		}
	}

	data, err := json.Marshal(in) //nolint:gosec // G117: PrivateKey and Passphrase are vngcloud.Secret; MarshalJSON redacts them, which this test itself verifies below
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	if strings.Contains(string(data), fixtureKeyPEM) || strings.Contains(string(data), passphrase) {
		t.Fatalf("json.Marshal() = %s, holds secret material", data)
	}

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	logger.Info("importing certificate", "private_key", in.PrivateKey, "passphrase", in.Passphrase)
	if strings.Contains(buf.String(), fixtureKeyPEM) || strings.Contains(buf.String(), passphrase) {
		t.Fatalf("slog output = %q, holds secret material", buf.String())
	}
}

func TestLoadBalancerCertificatePathIDRejection(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("no request expected")
	}))
	for _, id := range []string{"..", ".", "/", "?", ""} {
		if _, err := c.GetCertificate(context.Background(), &GetCertificateInput{CertificateID: id}); !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Fatalf("GetCertificate(%q) err = %v, want ErrInvalidInput", id, err)
		}
		if _, err := c.DeleteCertificate(context.Background(), &DeleteCertificateInput{CertificateID: id}); !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Fatalf("DeleteCertificate(%q) err = %v, want ErrInvalidInput", id, err)
		}
	}
}

func TestLoadBalancerDeleteCertificateInUseSendsNoDelete(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			t.Fatal("no DELETE expected when the pre-read shows InUse")
		}
		_, _ = w.Write([]byte(`{"uuid":"cert-1","inUse":true}`))
	}))

	_, err := c.DeleteCertificate(context.Background(), &DeleteCertificateInput{CertificateID: "cert-1"})
	if !errors.Is(err, ErrCertificateInUse) {
		t.Fatalf("DeleteCertificate() err = %v, want ErrCertificateInUse", err)
	}
}

func TestLoadBalancerDeleteCertificateSuccess(t *testing.T) {
	var getCalls, delCalls int32
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			atomic.AddInt32(&getCalls, 1)
			_, _ = w.Write([]byte(`{"uuid":"cert-1","inUse":false}`))
		case http.MethodDelete:
			atomic.AddInt32(&delCalls, 1)
			if r.URL.Path != "/v2/project-1/cas/cert-1" {
				t.Fatalf("unexpected path: %s", r.URL.Path)
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected method: %s", r.Method)
		}
	}))

	if _, err := c.DeleteCertificate(context.Background(), &DeleteCertificateInput{CertificateID: "cert-1"}); err != nil {
		t.Fatalf("DeleteCertificate() error = %v", err)
	}
	if getCalls != 1 || delCalls != 1 {
		t.Fatalf("getCalls=%d delCalls=%d, want 1 and 1", getCalls, delCalls)
	}
}

// TestLoadBalancerDeleteCertificateServerInUseRefusal checks that a 400 or a
// 409 from the DELETE itself, whose message matches the design's assumed
// in-use wording, also wraps ErrCertificateInUse.
func TestLoadBalancerDeleteCertificateServerInUseRefusal(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusConflict} {
		t.Run(fmt.Sprintf("status-%d", status), func(t *testing.T) {
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet {
					_, _ = w.Write([]byte(`{"uuid":"cert-1","inUse":false}`))
					return
				}
				w.WriteHeader(status)
				_, _ = w.Write([]byte(`{"message":"certificate is used by a listener"}`))
			}))

			_, err := c.DeleteCertificate(context.Background(), &DeleteCertificateInput{CertificateID: "cert-1"})
			if !errors.Is(err, ErrCertificateInUse) {
				t.Fatalf("DeleteCertificate() err = %v, want ErrCertificateInUse", err)
			}
		})
	}
}

func TestLoadBalancerDeleteCertificateNotFound(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))

	_, err := c.DeleteCertificate(context.Background(), &DeleteCertificateInput{CertificateID: "cert-1"})
	if !vngcloud.IsNotFound(err) {
		t.Fatalf("DeleteCertificate() err = %v, want NotFound", err)
	}
}

// TestLoadBalancerDeleteCertificateGoneMapsToNotFound checks that a 400 from
// the pre-read GetCertificate, for a certificate no longer listed, maps to
// the SDK's ordinary not-found sentinel: how a missing certificate actually
// reads (404, 400, or 500) is undecided pending a live check, so this proves
// the confirm-by-list mechanism the design specifies for whichever status
// that turns out to be.
func TestLoadBalancerDeleteCertificateGoneMapsToNotFound(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/v2/project-1/cas/cert-1" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"message":"cannot get certificate"}`))
			return
		}
		// The confirm-by-list call: cert-1 is no longer present.
		_, _ = w.Write([]byte(`{"listData":[],"page":1,"pageSize":10000,"totalPage":1,"totalItem":0}`))
	}))

	_, err := c.DeleteCertificate(context.Background(), &DeleteCertificateInput{CertificateID: "cert-1"})
	if !vngcloud.IsNotFound(err) {
		t.Fatalf("DeleteCertificate() err = %v, want NotFound", err)
	}
}

// TestLoadBalancerDeleteCertificateAmbiguousStillListedUnchanged checks that
// a non-404 error from the pre-read is returned as is, not remapped to
// NotFound, when the confirm-by-list call still shows the certificate
// present: the original failure means something other than "already gone".
func TestLoadBalancerDeleteCertificateAmbiguousStillListedUnchanged(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/v2/project-1/cas/cert-1" {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"message":"internal error"}`))
			return
		}
		_, _ = w.Write([]byte(`{"listData":[{"uuid":"cert-1"}],"page":1,"pageSize":10000,"totalPage":1,"totalItem":1}`))
	}))

	_, err := c.DeleteCertificate(context.Background(), &DeleteCertificateInput{CertificateID: "cert-1"})
	if vngcloud.IsNotFound(err) {
		t.Fatalf("DeleteCertificate() err = %v, want not NotFound (still listed)", err)
	}
	var apiErr *vngcloud.APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusInternalServerError {
		t.Fatalf("err = %v, want the original 500 *vngcloud.APIError", err)
	}
}

func TestLoadBalancerDeleteCertificateRequiredInput(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("no request expected")
	}))
	if _, err := c.DeleteCertificate(context.Background(), nil); !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("nil input err = %v", err)
	}
}
