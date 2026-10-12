package storage_test

import (
	"context"
	"errors"
	"io"
	"os"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/storage"
)

func ExampleObjectClient_PutObject() {
	client, err := storage.NewObjectClient(storage.ObjectConfig{Region: "HCM04", Credentials: storage.ObjectCredentials{AccessKeyID: vngcloud.Secret("access-id"), SecretAccessKey: vngcloud.Secret("secret-key")}})
	if err != nil {
		return
	}
	file, err := os.Open("bundle.bin")
	if err != nil {
		return
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return
	}
	_, err = client.PutObject(context.Background(), &storage.PutObjectInput{Bucket: "app-files", Key: "bundle.bin", Body: file, ContentLength: info.Size()})
	if err != nil {
		return
	}
}

func ExampleObjectClient_GetObject() {
	client, err := storage.NewObjectClient(storage.ObjectConfig{Region: "HCM04", Credentials: storage.ObjectCredentials{AccessKeyID: vngcloud.Secret("access-id"), SecretAccessKey: vngcloud.Secret("secret-key")}})
	if err != nil {
		return
	}
	out, err := client.GetObject(context.Background(), &storage.GetObjectInput{Bucket: "app-files", Key: "bundle.bin", MaxBytes: 1024 * 1024})
	if err != nil {
		return
	}
	defer func() { _ = out.Body.Close() }()
	// Copy reads through clean EOF. A read error leaves only partial bytes.
	_, err = io.Copy(io.Discard, out.Body)
	if err != nil {
		return
	}
}

func ExampleObjectClient_CopyObject() {
	client, err := storage.NewObjectClient(storage.ObjectConfig{
		Region: "HCM04",
		Credentials: storage.ObjectCredentials{
			AccessKeyID:     vngcloud.Secret("access-id"),
			SecretAccessKey: vngcloud.Secret("secret-key"),
		},
	})
	if err != nil {
		return
	}
	// Both buckets belong to one project in the configured region. The service
	// uses current names, so concurrent changes can affect either object.
	// Provider metadata defaults apply; this is not a lossless metadata clone.
	// Version fields are diagnostic and never authorize deleting the source.
	// HCM04 refuses encrypted sources and may leave the destination unencrypted
	// despite bucket encryption. HAN02 encryption behavior is unverified.
	_, err = client.CopyObject(context.Background(), &storage.CopyObjectInput{
		SourceBucket: "app-files", SourceKey: "archives/bundle.bin",
		Bucket: "app-backups", Key: "archives/bundle.bin", Replace: true,
	})
	if errors.Is(err, storage.ErrObjectCopyUnconfirmed) {
		// Inspect the destination before deciding whether to repeat the copy.
		return
	}
	if err != nil {
		return
	}
}
