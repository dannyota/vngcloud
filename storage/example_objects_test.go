package storage_test

import (
	"context"
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
