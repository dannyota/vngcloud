package cli

import "github.com/spf13/cobra"

func storageObjectOperationDocOps() []docOp {
	var ops []docOp
	for _, operation := range []string{"list-objects", "head-object", "delete-object", "copy-object"} {
		fields := extraDocFields(func(cmd *cobra.Command) {
			if operation == "copy-object" {
				registerStorageCopyObjectFlags(cmd, new(storageCopyObjectFlags))
			} else {
				registerStorageObjectOperationFlags(cmd, new(storageObjectOperationFlags), operation)
			}
		})
		kind := "Read"
		if operation == "delete-object" || operation == "copy-object" {
			kind = "Write, destructive"
		}
		if operation == "copy-object" {
			for i := range fields {
				if fields[i].name == "replace" {
					fields[i].required = true
				}
			}
		}

		ops = append(ops, docOp{name: operation, kind: kind, fields: fields})
	}
	return ops
}

func storageObjectOperationNotes(operation string) string {
	if operation == "copy-object" {
		return storageCopyObjectNotes()
	}
	if operation == "delete-object" {
		return "Require --yes and explicit --s3-region HCM04 or HAN02, --s3-credentials-file with a private [default] AWS-format access-key pair, --bucket and --key. Delete the current name unconditionally with one bodyless DELETE and no prior read, retry or confirmation request. Read-only from --read-only, VNGCLOUD_READ_ONLY or profile read_only blocks deletion before credentials. Ignore management region, project, auth and ambient S3 credentials; explicit --region and --project-id are refused. Linux and macOS only. No --cli-input-json, body, destination, --replace, version selector, precondition, MFA or retention bypass. --transfer-timeout defaults to 5m and accepts 1s through 1h. --output and --query select metadata only: Bucket, Key, DeleteMarker and VersionID. Echoed identifiers are redacted; absent or filtered DeleteMarker is JSON null and VersionID is empty. HTTP 204 confirms acceptance, not prior existence, permanent erasure, reclaimed quota or an empty bucket. No protection against concurrent writers: deletion can remove a new current object. Enabled versioning can add a delete marker and retain older versions; suspended versioning can remove the null version and add a marker while retaining numbered versions. Repetition can delete newly written data or add another marker. ObjectDeleteUnconfirmed exits 1 with fixed recovery: inspect the current key and version history before deciding whether to delete again. A post-success query or stdout failure says delete completed and do not retry, exits 1 and sends no second request. Requires s3:DeleteObject, without requiring a read or console grant in the client; recorded broader grants do not prove a minimal permission combination. HCM04 uses recorded deletion behavior; HAN02 relies on published S3 compatibility, not fresh live verification. Native create-only copy and move remain unavailable."
	}
	common := "Require explicit --s3-region HCM04 or HAN02 and --s3-credentials-file with a private [default] AWS-format access-key pair. Ignore management region, project, auth and ambient S3 credentials. Explicit --region and --project-id are refused. Linux and macOS only. Read-only permits both reads. No --cli-input-json, object body, local destination, version selector, subprocess, or automatic retry. --output and --query select accepted metadata only, never object content. --transfer-timeout defaults to 5m and accepts 1s through 1h. A post-success query or stdout failure says the operation completed and exits 1 without another request. Both regions rely on published S3 compatibility; HCM04 has additional recorded listing evidence. Detailed optional fields and HAN02 V2 pagination remain standards-based assumptions."
	if operation == "list-objects" {
		return common + " Return one ListObjectsV2 page (JSON keeps the envelope; table/text shows item rows, with explicit queries for page fields): Items, CommonPrefixes, MaxKeys, IsTruncated, NextContinuationToken. Require --bucket; --prefix matches exact UTF-8 bytes up to 1024 bytes, --delimiter is empty or '/', and --continuation-token is opaque UTF-8 up to 8 KiB without control bytes. --max-keys defaults to 1000 and accepts 1 through 1000; it bounds Items plus CommonPrefixes, not a total. Object rows contain Key, Size, ETag, LastModified and StorageClass. Common prefixes are groups, not objects. Optional metadata can be omitted; absent LastModified is null. Use IsTruncated and the returned token with unchanged bucket, prefix and delimiter for the next explicit invocation; even a short page can continue. No --all, automatic pagination, total count, or cross-page snapshot: concurrent changes can cause omissions or repeats. Requires s3:ListBucket on the bucket."
	}
	return common + " Require --bucket and --key. Send HEAD only, with no GET fallback. Output contains Bucket, Key, ContentLength, ETag, ContentType, LastModified, Metadata, VersionID and ServerSideEncryption. ContentLength -1 means absent; zero means empty. Optional unsafe, unsupported or oversize metadata is omitted; absent LastModified is null and Metadata is a non-nil map with lowercase names and no x-amz-meta- prefix. VersionID and encryption fields are observations, not locks or attestations; version selection is unavailable. Requires s3:GetObject. Preserve returned 403 as failure and 404 as not found; never infer absence from 403."
}

func storageObjectOperationExample(operation string) string {
	if operation == "copy-object" {
		return storageCopyObjectExample()
	}
	base := "vngcloud storage " + operation + " --s3-region HCM04 --s3-credentials-file ./storage.credentials --bucket app-files"
	if operation == "list-objects" {
		return base + " --prefix archives/ --delimiter / --max-keys 100"
	}
	if operation == "delete-object" {
		return base + " --key archives/bundle.bin --yes"
	}
	return base + " --key archives/bundle.bin"
}
