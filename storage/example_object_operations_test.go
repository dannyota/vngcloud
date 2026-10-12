package storage_test

import (
	"context"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/storage"
)

func ExampleObjectClient_ListObjects() {
	client, err := storage.NewObjectClient(storage.ObjectConfig{
		Region: "HCM04",
		Credentials: storage.ObjectCredentials{
			AccessKeyID:     vngcloud.Secret("<access-key-id>"),
			SecretAccessKey: vngcloud.Secret("<secret-access-key>"),
		},
	})
	if err != nil {
		return
	}
	input := &storage.ListObjectsInput{
		Bucket: "app-files", Prefix: "archives/", Delimiter: "/", MaxKeys: 100,
	}
	page, err := client.ListObjects(context.Background(), input)
	if err != nil {
		return
	}
	// Continue explicitly with the same bucket, prefix, and delimiter.
	if page.IsTruncated {
		input.ContinuationToken = page.NextContinuationToken
		page, err = client.ListObjects(context.Background(), input)
		if err != nil {
			return
		}
	}
	_ = page.Items
	_ = page.CommonPrefixes
}

func ExampleObjectClient_HeadObject() {
	client, err := storage.NewObjectClient(storage.ObjectConfig{
		Region: "HAN02",
		Credentials: storage.ObjectCredentials{
			AccessKeyID:     vngcloud.Secret("<access-key-id>"),
			SecretAccessKey: vngcloud.Secret("<secret-access-key>"),
		},
	})
	if err != nil {
		return
	}
	metadata, err := client.HeadObject(context.Background(), &storage.HeadObjectInput{
		Bucket: "app-files", Key: "archives/bundle.bin",
	})
	if err != nil {
		return
	}
	// HEAD reports accepted metadata without downloading object content.
	_ = metadata.ContentLength
	_ = metadata.Metadata
	_ = metadata.VersionID
}
