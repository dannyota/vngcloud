# Storage Bucket Policy

Bucket policy calls for `danny.vn/vngcloud/storage`: `GetBucketPolicy`,
`PutBucketPolicy`, and `DeleteBucketPolicy`. A bucket policy gives a service
account's key rights on one bucket. See [Storage](Storage.md) for buckets, S3
keys, and attaching a key to a service account.

This page assumes `cfg`, `ctx`, and `client := storage.New(cfg)` from
[Storage's Setup](Storage.md#setup).

## Calls

All three calls take `ProjectID` and `Bucket`, and `Region` works as in
[Storage](Storage.md#regions). `PutBucketPolicy` also takes `Policy`, the
document as JSON text.

```go
principal, err := client.EnsureServiceAccountPrincipal(ctx,
	&storage.EnsureServiceAccountPrincipalInput{
		ProjectID:        projectID,
		ServiceAccountID: serviceAccountID,
	})
if err != nil {
	log.Fatal(err)
}
policy := buildPolicy(principal.PrincipalARN, bucket) // the template below

_, err = client.PutBucketPolicy(ctx, &storage.PutBucketPolicyInput{
	ProjectID: projectID,
	Bucket:    bucket,
	Policy:    policy,
})

got, err := client.GetBucketPolicy(ctx, &storage.GetBucketPolicyInput{
	ProjectID: projectID,
	Bucket:    bucket,
})
if err != nil {
	log.Fatal(err)
}
log.Println(got.Policy == "") // true when the bucket has no policy

_, err = client.DeleteBucketPolicy(ctx, &storage.DeleteBucketPolicyInput{
	ProjectID: projectID,
	Bucket:    bucket,
})
```

- `GetBucketPolicy` returns `Policy` as the server sends it. A bucket with no
  policy gives `Policy ""` and no error. The server may change whitespace and
  key order, so compare decoded documents, not strings.
- `PutBucketPolicy` replaces the whole policy. It sends `Policy` unchanged as
  a JSON string. A `Policy` that is not a JSON object with a non-empty
  `Statement` array returns `vngcloud.ErrInvalidInput`, and nothing is sent.
  So does a statement that is not a JSON object or lacks a non-empty `Effect`,
  `Principal`, `Action`, or `Resource`. The error names the statement index
  and the field.
  To remove every statement, call `DeleteBucketPolicy`.
- `DeleteBucketPolicy` succeeds when the bucket has no policy, so a repeat
  delete returns the same result.
- Put and delete give the same result when repeated, so both keep the
  transport's retries.
- A put, a delete, and an attach reach the data plane within about a second.

## Template

This policy gives one principal object work in one bucket and nothing on the
bucket's settings. Fill in the principal from `PrincipalARN` and the bucket
name:

```json
{"Version": "2012-10-17", "Statement": [
  {"Sid": "Bucket", "Effect": "Allow",
   "Principal": {"AWS": ["<PrincipalARN>"]},
   "Action": ["s3:ListBucket", "s3:GetBucketLocation",
              "s3:ListBucketMultipartUploads"],
   "Resource": ["arn:aws:s3:::<bucket>"]},
  {"Sid": "Objects", "Effect": "Allow",
   "Principal": {"AWS": ["<PrincipalARN>"]},
   "Action": ["s3:GetObject", "s3:PutObject", "s3:DeleteObject",
              "s3:AbortMultipartUpload", "s3:ListMultipartUploadParts"],
   "Resource": ["arn:aws:s3:::<bucket>/*"]}]}
```

The template names object actions and does not use `s3:*`. In S3, `s3:*` also
grants the bucket delete and policy changes on that bucket, so a leaked key
could delete the bucket or rewrite its policy. Whether this server lets a
non-owner do so is not checked, and the template avoids the question.

## Rules

- An attached key lists every bucket of the project and can create a bucket,
  but cannot put or delete objects in it, or delete it.
- A `GET` of a missing object answers 404 `NoSuchKey` even in a bucket the
  key cannot read, so a key learns which object names exist anywhere in the
  project. Do not put secrets in object names.
- An attach makes the service account's sub-user. Call
  `EnsureServiceAccountPrincipal` only to get `PrincipalARN` for a policy.
- The server does not check principals. A policy that names no real sub-user,
  such as a mistyped ARN or a deleted service account, is accepted and grants
  nothing. Copy `PrincipalARN` from `EnsureServiceAccountPrincipal`, and check
  access with the attached key.
- `PutBucketPolicy` refuses a statement with no `Principal`, including `{}`,
  because the server accepts one and then answers the console API's
  `GetBucketPolicy`, `DeleteBucketPolicy`, and `DeleteBucket` for that
  bucket with an empty body (`EmptyResponse`). A policy put by another tool
  can still cause this. The S3 `DeleteBucketPolicy` call, made with an S3 key
  that is not attached, removes the policy and restores the console calls.
- Remove a service account from its bucket policies before you delete it: a
  new service account with the same name gets the same principal.

## Errors

| Case | Result |
|---|---|
| Missing field, bad project ID or bucket name, unmapped region | `vngcloud.ErrInvalidInput`, no call sent |
| `Policy` is not a JSON object with a non-empty `Statement` array, or a statement lacks a non-empty `Effect`, `Principal`, `Action`, or `Resource` | `vngcloud.ErrInvalidInput`, no call sent |
| The server cannot parse the policy: envelope code `400`, such as an unknown `Version` or `Effect` | `*vngcloud.APIError` with the parser's message, no sentinel |
| Envelope code `114` | `*vngcloud.APIError` with the server's message, no sentinel |
| `GetBucketPolicy` on a bucket that does not exist | `*vngcloud.APIError`, code `EmptyResponse`: the server answers HTTP 200 with no body |
| `GetBucketPolicy` data that is not a string | `*vngcloud.APIError`, code `InvalidResponse` |
| HTTP 403 | `vngcloud.ErrPermission` |

See [Errors](Errors.md) for `APIError` itself.
