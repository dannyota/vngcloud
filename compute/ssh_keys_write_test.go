package compute

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/testutil"
	"danny.vn/vngcloud/internal/transport"
)

// TestSSHKeyHasNoPrivateKeyField checks that the read model can never hold
// a private key, whatever a future response might carry.
func TestSSHKeyHasNoPrivateKeyField(t *testing.T) {
	if _, ok := reflect.TypeOf(SSHKey{}).FieldByName("PrivateKey"); ok {
		t.Fatal("SSHKey must not have a PrivateKey field")
	}
}

func TestComputeGetSSHKey(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/project-1/sshKeys/key-1" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		testutil.WriteFixture(t, w, "../testdata/compute/get_ssh_key.json")
	}))

	out, err := c.GetSSHKey(context.Background(), &GetSSHKeyInput{SSHKeyID: "key-1"})
	if err != nil {
		t.Fatalf("GetSSHKey() error = %v", err)
	}
	if out.SSHKey.ID != "key-1" || out.SSHKey.Status != "ACTIVE" {
		t.Fatalf("unexpected ssh key: %+v", out.SSHKey)
	}
}

func TestComputeGetSSHKeyNotFound(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))

	_, err := c.GetSSHKey(context.Background(), &GetSSHKeyInput{SSHKeyID: "key-1"})
	if !vngcloud.IsNotFound(err) {
		t.Fatalf("GetSSHKey() err = %v, want NotFound", err)
	}
}

func TestComputeSSHKeyPathIDRejection(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("no request expected")
	}))
	for _, id := range []string{"..", ".", "/", "?", ""} {
		if _, err := c.GetSSHKey(context.Background(), &GetSSHKeyInput{SSHKeyID: id}); !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Fatalf("GetSSHKey(%q) err = %v, want ErrInvalidInput", id, err)
		}
		if _, err := c.DeleteSSHKey(context.Background(), &DeleteSSHKeyInput{SSHKeyID: id}); !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Fatalf("DeleteSSHKey(%q) err = %v, want ErrInvalidInput", id, err)
		}
	}
}

func TestComputeImportSSHKeyRequestBody(t *testing.T) {
	const publicKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIFAKEKEYMATERIALFORTESTS app"
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/v2/project-1/sshKeys/import" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		var body struct {
			Name   string `json:"name"`
			PubKey string `json:"pubKey"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.Name != "app" || body.PubKey != publicKey {
			t.Fatalf("unexpected body: %+v", body)
		}
		w.WriteHeader(http.StatusCreated)
		testutil.WriteFixture(t, w, "../testdata/compute/import_ssh_key.json")
	}))

	out, err := c.ImportSSHKey(context.Background(), &ImportSSHKeyInput{Name: "app", PublicKey: publicKey})
	if err != nil {
		t.Fatalf("ImportSSHKey() error = %v", err)
	}
	if out.SSHKey.ID != "key-1" || out.SSHKey.PublicKey != "<public-key>" {
		t.Fatalf("unexpected ssh key: %+v", out.SSHKey)
	}
}

func TestComputeImportSSHKeyTrimsPublicKey(t *testing.T) {
	const publicKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIFAKEKEYMATERIALFORTESTS app"
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			PubKey string `json:"pubKey"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.PubKey != publicKey {
			t.Fatalf("pubKey = %q, want the trimmed value %q", body.PubKey, publicKey)
		}
		w.WriteHeader(http.StatusCreated)
		testutil.WriteFixture(t, w, "../testdata/compute/import_ssh_key.json")
	}))

	if _, err := c.ImportSSHKey(context.Background(), &ImportSSHKeyInput{Name: "app", PublicKey: "  " + publicKey + "\n"}); err != nil {
		t.Fatalf("ImportSSHKey() error = %v", err)
	}
}

func TestComputeImportSSHKeyShapeRefusals(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("no request expected")
	}))

	multiLine := "ssh-ed25519 AAAA1...\nssh-ed25519 BBBB2..."
	if _, err := c.ImportSSHKey(context.Background(), &ImportSSHKeyInput{Name: "app", PublicKey: multiLine}); !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("multi-line PublicKey err = %v, want ErrInvalidInput", err)
	}

	privateKeyText := "-----BEGIN OPENSSH PRIVATE KEY-----fake-body-not-a-real-key-----END OPENSSH PRIVATE KEY-----"
	_, err := c.ImportSSHKey(context.Background(), &ImportSSHKeyInput{Name: "app", PublicKey: privateKeyText})
	if !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("PRIVATE KEY PublicKey err = %v, want ErrInvalidInput", err)
	}
	if strings.Contains(err.Error(), privateKeyText) {
		t.Fatalf("error echoes the rejected value: %v", err)
	}
}

func TestComputeImportSSHKeyDuplicateNameNotWrapped(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"message":"name of ssh key already exist"}`))
	}))

	_, err := c.ImportSSHKey(context.Background(), &ImportSSHKeyInput{Name: "app", PublicKey: "ssh-ed25519 AAAAfakeKeyMaterial app"})
	var apiErr *vngcloud.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v, want *vngcloud.APIError", err)
	}
	if strings.Contains(err.Error(), "list-ssh-keys") {
		t.Fatalf("a 4xx error should not get the ambiguous-create hint: %v", err)
	}
}

func TestComputeImportSSHKeyMissingID(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"data":{"name":"app","pubKey":"<public-key>","status":"ACTIVE"}}`))
	}))

	if _, err := c.ImportSSHKey(context.Background(), &ImportSSHKeyInput{Name: "app", PublicKey: "ssh-ed25519 AAAAfakeKeyMaterial app"}); err == nil {
		t.Fatal("ImportSSHKey() error = nil, want an error for a response with no id")
	}
}

func TestComputeImportSSHKeyNoRetryOn502(t *testing.T) {
	var calls int32
	c := New(testutil.NewRetryConfig(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusBadGateway)
	})))

	_, err := c.ImportSSHKey(context.Background(), &ImportSSHKeyInput{Name: "app", PublicKey: "ssh-ed25519 AAAAfakeKeyMaterial app"})
	if err == nil {
		t.Fatal("ImportSSHKey() error = nil, want an error")
	}
	if calls != 1 {
		t.Fatalf("server received %d request(s), want 1 (no retry after 502)", calls)
	}
	if !strings.Contains(err.Error(), "list-ssh-keys") {
		t.Fatalf("error does not name list-ssh-keys: %v", err)
	}
}

func TestComputeCreateSSHKeyRequestBodyAndResponse(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/v2/project-1/sshKeys" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		var body struct {
			Name string `json:"name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.Name != "app" {
			t.Fatalf("unexpected body: %+v", body)
		}
		w.WriteHeader(http.StatusCreated)
		testutil.WriteFixture(t, w, "../testdata/compute/create_ssh_key.json")
	}))

	out, err := c.CreateSSHKey(context.Background(), &CreateSSHKeyInput{Name: "app"})
	if err != nil {
		t.Fatalf("CreateSSHKey() error = %v", err)
	}
	if out.SSHKey.ID != "key-1" || out.SSHKey.PublicKey != "<public-key>" {
		t.Fatalf("unexpected ssh key: %+v", out.SSHKey)
	}
	if out.PrivateKey.Reveal() != "<secret>" {
		t.Fatalf("Reveal() = %q, want the fixture value", out.PrivateKey.Reveal())
	}
}

func TestComputeCreateSSHKeyMissingIDOrPrivateKey(t *testing.T) {
	bodies := []string{
		`{"data":{"name":"app","pubKey":"<public-key>","privateKey":"<secret>","status":"ACTIVE"}}`,
		`{"data":{"id":"key-1","name":"app","pubKey":"<public-key>","status":"ACTIVE"}}`,
	}
	for _, body := range bodies {
		c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(body))
		}))
		if _, err := c.CreateSSHKey(context.Background(), &CreateSSHKeyInput{Name: "app"}); err == nil {
			t.Fatalf("CreateSSHKey() error = nil for body %s, want an error", body)
		}
	}
}

func TestComputeCreateSSHKeyNoRetryOn502(t *testing.T) {
	var calls int32
	c := New(testutil.NewRetryConfig(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusBadGateway)
	})))

	_, err := c.CreateSSHKey(context.Background(), &CreateSSHKeyInput{Name: "app"})
	if err == nil {
		t.Fatal("CreateSSHKey() error = nil, want an error")
	}
	if calls != 1 {
		t.Fatalf("server received %d request(s), want 1 (no retry after 502)", calls)
	}
	if !strings.Contains(err.Error(), "lost its private key") {
		t.Fatalf("error does not mention the lost-private-key hint: %v", err)
	}
}

// TestComputeCreateSSHKeyNeverCaptured checks that CreateSSHKey's Sensitive
// request never reaches the configured response-capture hook, while an
// ordinary read on the same Client still does.
func TestComputeCreateSSHKeyNeverCaptured(t *testing.T) {
	var captured []transport.Capture
	cfg := testutil.NewConfigWithCapture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusCreated)
			testutil.WriteFixture(t, w, "../testdata/compute/create_ssh_key.json")
			return
		}
		testutil.WriteFixture(t, w, "../testdata/compute/get_ssh_key.json")
	}), func(c transport.Capture) {
		captured = append(captured, c)
	})
	c := New(cfg)

	if _, err := c.GetSSHKey(context.Background(), &GetSSHKeyInput{SSHKeyID: "key-1"}); err != nil {
		t.Fatalf("GetSSHKey() error = %v", err)
	}
	if len(captured) != 1 {
		t.Fatalf("captured = %d after GetSSHKey, want 1", len(captured))
	}

	if _, err := c.CreateSSHKey(context.Background(), &CreateSSHKeyInput{Name: "app"}); err != nil {
		t.Fatalf("CreateSSHKey() error = %v", err)
	}
	if len(captured) != 1 {
		t.Fatalf("captured = %d after CreateSSHKey, want still 1 (never captured)", len(captured))
	}
}

// TestComputeCreateSSHKeySecretRedaction checks that CreateSSHKeyOutput's
// PrivateKey never leaks the fixture secret through fmt verbs, slog, or
// json.Marshal, and that "[redacted]" appears in its place.
func TestComputeCreateSSHKeySecretRedaction(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		testutil.WriteFixture(t, w, "../testdata/compute/create_ssh_key.json")
	}))

	out, err := c.CreateSSHKey(context.Background(), &CreateSSHKeyInput{Name: "app"})
	if err != nil {
		t.Fatalf("CreateSSHKey() error = %v", err)
	}

	forms := map[string]string{
		"%v on Output":      fmt.Sprintf("%v", out),
		"%+v on Output":     fmt.Sprintf("%+v", out),
		"%#v on Output":     fmt.Sprintf("%#v", out),
		"%s on PrivateKey":  fmt.Sprintf("%s", out.PrivateKey),
		"%v on PrivateKey":  fmt.Sprintf("%v", out.PrivateKey),
		"%#v on PrivateKey": fmt.Sprintf("%#v", out.PrivateKey),
	}
	for name, got := range forms {
		if strings.Contains(got, "<secret>") {
			t.Fatalf("%s = %q, holds the fixture secret", name, got)
		}
		if !strings.Contains(got, "[redacted]") {
			t.Fatalf("%s = %q, want it to contain [redacted]", name, got)
		}
	}

	data, err := json.Marshal(out) //nolint:gosec // G117: PrivateKey is a vngcloud.Secret; its MarshalJSON redacts it, which this test itself verifies below
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	if strings.Contains(string(data), "<secret>") {
		t.Fatalf("json.Marshal() = %s, holds the fixture secret", data)
	}
	if !strings.Contains(string(data), "[redacted]") {
		t.Fatalf("json.Marshal() = %s, want it to contain [redacted]", data)
	}

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	logger.Info("created ssh key", "private_key", out.PrivateKey)
	if strings.Contains(buf.String(), "<secret>") {
		t.Fatalf("slog output = %q, holds the fixture secret", buf.String())
	}
	if !strings.Contains(buf.String(), "[redacted]") {
		t.Fatalf("slog output = %q, want it to contain [redacted]", buf.String())
	}
}

func TestComputeDeleteSSHKey(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete || r.URL.Path != "/v2/project-1/sshKeys/key-1" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusNoContent)
	}))

	if _, err := c.DeleteSSHKey(context.Background(), &DeleteSSHKeyInput{SSHKeyID: "key-1"}); err != nil {
		t.Fatalf("DeleteSSHKey() error = %v", err)
	}
}

func TestComputeDeleteSSHKeyNotFound(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))

	_, err := c.DeleteSSHKey(context.Background(), &DeleteSSHKeyInput{SSHKeyID: "key-1"})
	if !vngcloud.IsNotFound(err) {
		t.Fatalf("DeleteSSHKey() err = %v, want NotFound", err)
	}
}

// TestComputeDeleteSSHKeyGoneReturnsNotFound checks that a second delete of
// the same key, which the server reports as a 400 rather than a 404, maps
// to the SDK's ordinary not-found sentinel, the same as a 404 would.
func TestComputeDeleteSSHKeyGoneReturnsNotFound(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"message":"Cannot get ssh key with id key-1"}`))
	}))

	_, err := c.DeleteSSHKey(context.Background(), &DeleteSSHKeyInput{SSHKeyID: "key-1"})
	if !vngcloud.IsNotFound(err) {
		t.Fatalf("DeleteSSHKey() err = %v, want NotFound", err)
	}
	var apiErr *vngcloud.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v, want *vngcloud.APIError in its chain", err)
	}
}

// TestComputeDeleteSSHKeyOtherBadRequestUnchanged checks that a 400 for a
// different reason, or naming a different key, is not misreported as
// not-found.
func TestComputeDeleteSSHKeyOtherBadRequestUnchanged(t *testing.T) {
	bodies := []string{
		`{"message":"something else went wrong"}`,
		`{"message":"Cannot get ssh key with id key-2"}`,
	}
	for _, body := range bodies {
		c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(body))
		}))

		_, err := c.DeleteSSHKey(context.Background(), &DeleteSSHKeyInput{SSHKeyID: "key-1"})
		if vngcloud.IsNotFound(err) {
			t.Fatalf("body %s: DeleteSSHKey() err = %v, want not NotFound", body, err)
		}
		var apiErr *vngcloud.APIError
		if !errors.As(err, &apiErr) {
			t.Fatalf("body %s: err = %v, want *vngcloud.APIError", body, err)
		}
	}
}

// TestComputeGetSSHKeyEmptyReturnsNotFound checks that a 200 response with
// an empty object, which the server sends for an unknown id instead of a
// 404, maps to the SDK's ordinary not-found sentinel rather than an empty
// SSHKey with no error.
func TestComputeGetSSHKeyEmptyReturnsNotFound(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"data":{}}`))
	}))

	out, err := c.GetSSHKey(context.Background(), &GetSSHKeyInput{SSHKeyID: "key-1"})
	if !vngcloud.IsNotFound(err) {
		t.Fatalf("GetSSHKey() err = %v, want NotFound", err)
	}
	if out != nil {
		t.Fatalf("GetSSHKey() out = %+v, want nil", out)
	}
}
