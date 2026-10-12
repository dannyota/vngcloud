//go:build !linux && !darwin

package cli

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/storage"
)

type objectUnsupportedBody struct {
	touched bool
	closed  bool
}

func (b *objectUnsupportedBody) Read([]byte) (int, error) { b.touched = true; return 0, io.EOF }
func (b *objectUnsupportedBody) Close() error             { b.closed = true; return nil }

func TestObjectFilesUnsupported(t *testing.T) {
	if _, err := readS3CredentialsFile("unused"); !errors.Is(err, vngcloud.ErrCredentialsFile) {
		t.Fatal(err)
	}
	if _, _, err := snapshotObjectFile(context.Background(), "unused"); err == nil {
		t.Fatal("snapshot supported")
	}
	if err := checkObjectDestination("unused", false); err == nil {
		t.Fatal("destination supported")
	}
	body := new(objectUnsupportedBody)
	if result, err := downloadObjectFile(context.Background(), body, "unused", false); err == nil || result.Published || body.touched || !body.closed {
		t.Fatal("unsupported download consumed or left body open")
	}
	if _, err := storage.NewObjectClient(storage.ObjectConfig{Region: "HCM04", Credentials: storage.ObjectCredentials{AccessKeyID: vngcloud.Secret("synthetic"), SecretAccessKey: vngcloud.Secret(strings.Repeat("s", 8))}}); err != nil {
		t.Fatal(err)
	}
}
