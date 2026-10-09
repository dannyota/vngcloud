package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"

	"danny.vn/vngcloud/internal/testutil"
	"danny.vn/vngcloud/internal/transport"
)

const fixtureSecret = "<secret>"

func createdKey(t *testing.T) *CreateS3KeyOutput {
	t.Helper()
	s := &keyServer{status: 200, body: fixture(t, "create_s3_key.json")}
	out, err := newTestClient(t, s.handler(t)).CreateS3Key(context.Background(), &CreateS3KeyInput{ProjectID: "proj-1"})
	if err != nil {
		t.Fatal(err)
	}
	if out.SecretKey.Reveal() != fixtureSecret {
		t.Fatal("the fixture secret did not reach the Output")
	}
	return out
}

func TestCreateS3KeyOutputPrintsRedacted(t *testing.T) {
	out := createdKey(t)
	for _, verb := range []string{"%s", "%v", "%+v", "%#v"} {
		for _, v := range []any{out, *out, out.SecretKey} {
			got := fmt.Sprintf(verb, v)
			if strings.Contains(got, fixtureSecret) {
				t.Fatalf("%s of %T leaked the secret: %s", verb, v, got)
			}
		}
	}
	if got := fmt.Sprintf("%+v", out); !strings.Contains(got, "[redacted]") {
		t.Fatalf("%%+v = %s, want [redacted]", got)
	}
}

func TestCreateS3KeyOutputEncodesRedacted(t *testing.T) {
	out := createdKey(t)
	b, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), fixtureSecret) || !strings.Contains(string(b), `"SecretKey":"[redacted]"`) {
		t.Fatalf("json = %s", b)
	}

	for name, newHandler := range map[string]func(*bytes.Buffer) slog.Handler{
		"text": func(b *bytes.Buffer) slog.Handler { return slog.NewTextHandler(b, nil) },
		"json": func(b *bytes.Buffer) slog.Handler { return slog.NewJSONHandler(b, nil) },
	} {
		var buf bytes.Buffer
		slog.New(newHandler(&buf)).Info("created", "out", out, "secret", out.SecretKey, "key", *out)
		if strings.Contains(buf.String(), fixtureSecret) {
			t.Fatalf("slog %s leaked the secret: %s", name, buf.String())
		}
		if !strings.Contains(buf.String(), "[redacted]") {
			t.Fatalf("slog %s = %s, want [redacted]", name, buf.String())
		}
	}
}

// captureLog records every response the transport hands to the capture hook.
type captureLog struct {
	mu  sync.Mutex
	ops []string
	all strings.Builder
}

func (c *captureLog) hook(r transport.Capture) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ops = append(c.ops, r.Operation)
	c.all.Write(r.Body)
}

func TestKeyCallsNeverReachTheCaptureHook(t *testing.T) {
	var log captureLog
	h := serve(t, func(w http.ResponseWriter, r *http.Request) {
		file := "create_s3_key.json"
		if r.Method == http.MethodGet {
			file = "list_s3_keys.json"
		}
		testutil.WriteFixture(t, w, fixtures+file)
	})
	c := New(testutil.NewConfigWithCapture(t, h, log.hook))
	ctx := context.Background()
	if _, err := c.CreateS3Key(ctx, &CreateS3KeyInput{ProjectID: "proj-1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ListS3Keys(ctx, &ListS3KeysInput{ProjectID: "proj-1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.DeleteS3Key(ctx, &DeleteS3KeyInput{ProjectID: "proj-1", UserKeyID: "key-1"}); err != nil {
		t.Fatal(err)
	}
	for _, op := range log.ops {
		if op == "storage.CreateS3Key" || op == "storage.ListS3Keys" {
			t.Fatalf("the hook saw %s", op)
		}
	}
	if strings.Contains(log.all.String(), fixtureSecret) {
		t.Fatal("the hook saw the secret")
	}
	if len(log.ops) == 0 {
		t.Fatal("the hook saw nothing, so this test proves nothing")
	}
}

func TestCreateS3KeyDecodeErrorsNeverQuoteTheBody(t *testing.T) {
	bodies := []string{
		"not json " + fixtureSecret,
		`{"code":200,"success":true,"data":{"userKeyId":7,"accessKey":"a","secretKey":"` + fixtureSecret + `"}}`,
		`{"code":200,"success":true,"data":{"userKeyId":"k","accessKey":"a","secretKey":["` + fixtureSecret + `"]}}`,
		`{"code":200,"success":true,"data":{"userKeyId":"k","accessKey":"a","secretKey":"` + fixtureSecret + `"`,
		`{"code":200,"success":false,"errorMsg":"x","data":{"secretKey":"` + fixtureSecret + `"}}`,
	}
	for _, body := range bodies {
		s := &keyServer{status: 200, body: body}
		out, err := newTestClient(t, s.handler(t)).CreateS3Key(context.Background(), &CreateS3KeyInput{ProjectID: "proj-1"})
		if out != nil && out.SecretKey.Reveal() != "" {
			continue
		}
		if err == nil {
			t.Fatalf("body %s: want an error", body)
		}
		if strings.Contains(err.Error(), fixtureSecret) || strings.Contains(fmt.Sprintf("%+v %#v", err, err), fixtureSecret) {
			t.Fatalf("body %s: the error quotes the body: %v", body, err)
		}
	}
}

func TestKeyCallsLogNoSecretOrBody(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	h := serve(t, func(w http.ResponseWriter, r *http.Request) {
		testutil.WriteFixture(t, w, fixtures+"create_s3_key.json")
	})
	c := New(testutil.NewConfigWithLogger(t, h, logger))
	if _, err := c.CreateS3Key(context.Background(), &CreateS3KeyInput{ProjectID: "proj-1"}); err != nil {
		t.Fatal(err)
	}
	if buf.Len() == 0 {
		t.Fatal("the logger saw nothing, so this test proves nothing")
	}
	for _, leak := range []string{fixtureSecret, "<access-key-1>", "<user-key-id-1>", "Bearer"} {
		if strings.Contains(buf.String(), leak) {
			t.Fatalf("the debug log holds %q: %s", leak, buf.String())
		}
	}
}
