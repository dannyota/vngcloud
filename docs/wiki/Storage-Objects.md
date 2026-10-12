# Storage Objects

Use `storage.NewObjectClient` for native S3 object transfers. The existing
`storage.New(cfg)` client manages vStorage projects, buckets, keys, policies,
and settings through the console API. The object client uses a separate S3
data plane and never loads IAM credentials or a management client.

This feature transfers one completed regular file as one object. It supports
binary content without text conversion, compression, or format inspection.
The SDK accepts portable in-memory credentials. The CLI file transfer commands
support Linux and macOS only.

## Credentials and regions

Use the existing [storage management setup](Storage.md#setup) and
[S3 key setup](Storage.md#s3-keys) to create a key for the selected project.
The `create-s3-key --secret-file <credentials-file>` command writes a
mode-0600 AWS shared credentials file containing `[default]`,
`aws_access_key_id`, and `aws_secret_access_key`. Keep the file private and
its parent directory trusted. The object CLI requires this explicit file and
checks that it is a regular file owned by the current user with mode 0600. Do
not pass secret values in command arguments.

The CLI and SDK require the exact S3 region `HCM04` or `HAN02`. They select
`https://hcm04.vstorage.vngcloud.vn` or
`https://han02.vstorage.vngcloud.vn`. The commands reject explicit global
`--region` and `--project-id`; environment or profile defaults for those
management settings do not select the S3 region. The S3 key scopes the project.

The intended actions need only `s3:PutObject` for upload and `s3:GetObject`
for download. No IAM login or ambient credential chain is used. The CLI
requires the explicit credential file; SDK callers pass both credentials in
memory:

```go
client, err := storage.NewObjectClient(storage.ObjectConfig{
	Region: "HCM04",
	Credentials: storage.ObjectCredentials{
		AccessKeyID:     vngcloud.Secret("<access-key-id>"),
		SecretAccessKey: vngcloud.Secret("<secret-access-key>"),
	},
})
```

The placeholders above are examples only. Never paste real credentials into
documentation, source control, or shell history.

## CLI file transfers

Upload one file and download one exact key:

```sh
vngcloud storage put-object --s3-region HCM04 \
  --s3-credentials-file ./storage.credentials \
  --bucket app-files --key archives/bundle.tar.gz \
  --body ./bundle.tar.gz

vngcloud storage get-object --s3-region HCM04 \
  --s3-credentials-file ./storage.credentials \
  --bucket app-files --key archives/bundle.tar.gz \
  --destination ./downloaded.tar.gz
```

Both commands require regular file paths. They do not stream object bytes
through stdin or stdout and do not accept `-` or `file://` paths. Downloads
write only to the selected destination. `--cli-input-json` is unsupported.
Normal `--output` and `--query` handle metadata only: bucket, key, byte count,
local SHA256, optional ETag, and download destination.

The CLI PUT limit is decimal 5,000,000,000 bytes. GET defaults to the same
bound. `--max-download-bytes` selects a positive finite limit up to
9,223,372,036,854,775,806 bytes. A five-minute deadline covers the full
transfer by default. `--transfer-timeout` accepts one second through one hour.

HCM04 supports default create-only uploads. HAN02 create-only uploads are
unavailable; HAN02 downloads and explicit replacement remain available under
the published compatibility contract. This is not a new live verification.

## Replacement and local files

PUT creates only by default with a conditional request. If the key already
exists, the upload fails and preserves it. `--replace --yes` sends an
unconditional whole-object PUT. `--replace` without `--yes` fails before the
request. `--yes` by itself does not enable replacement. A CLI read-only policy
blocks PUT before the credentials file or source file opens. Read-only mode
still permits GET, which writes the chosen local file.

Finish writing the source before upload. The CLI copies it into a private
0600 snapshot inside a new 0700 `vngcloud-object-*` temporary directory, then
hashes and uploads that snapshot. The snapshot may use up to one upload's worth
of disk space. Changes to the source during copying can produce a mixed
snapshot. A process crash can leave `vngcloud-object-*` files for manual local
cleanup; the CLI does not sweep them automatically.

For GET, the destination parent must already exist and be trusted. By default,
the CLI refuses every occupied destination, including a dangling symlink.
`--replace --yes` permits replacing an existing regular local file; it does
not replace the remote object. The CLI writes a same-directory private
temporary file and publishes only after clean EOF, sync, and close. Default
publication uses a no-clobber link operation. Replacement uses an atomic
rename on supported Linux and macOS filesystems. A pre-publication failure
leaves the old destination intact. These operations do not promise power-loss
durability on every filesystem. Use trusted source, credential, and destination
parent directories.

If publication succeeds but temporary cleanup or directory sync fails, the
error says the download was published. Keep the complete file, do not repeat
GET, and remove any named local temporary file or sync the directory as
appropriate.

## SDK transfers

The SDK accepts in-memory credentials and does not read files. Set
`ObjectConfig.Region` to exactly `HCM04` or `HAN02`. A zero
`ObjectConfig.Timeout` selects five minutes; positive timeouts can be at most
one hour, and negative values are invalid. The requester's earlier deadline
wins.

PUT accepts an `io.ReadSeeker`. The SDK seeks the body to offset zero before
hashing, then rewinds it and sends one bounded request. `ContentLength` may be
zero through 5,000,000,000 bytes. The caller keeps the body immutable during
the call and owns it; `PutObject` does not close it. GET returns a streaming
`io.ReadCloser`. A zero `GetObjectInput.MaxBytes` selects the 5,000,000,000
byte default. A positive value sets a finite bound up to
9,223,372,036,854,775,806; negative values and `math.MaxInt64` are invalid.
Read the body through clean EOF to establish completion, then close it on every
path, including read errors:

```go
body, err := os.Open("bundle.tar.gz")
if err != nil {
	return err
}
defer body.Close()
info, err := body.Stat()
if err != nil {
	return err
}
put, err := client.PutObject(ctx, &storage.PutObjectInput{
	Bucket: "app-files", Key: "archives/bundle.tar.gz",
	Body: body, ContentLength: info.Size(),
})
if err != nil {
	return err
}
_ = put

got, err := client.GetObject(ctx, &storage.GetObjectInput{
	Bucket: "app-files", Key: "archives/bundle.tar.gz",
})
if err != nil {
	return err
}
defer got.Body.Close()
if _, err := io.Copy(destination, got.Body); err != nil {
	return err
}
```

The GET body must be consumed to clean EOF before treating the transfer as
complete. `ContentLength` is -1 when the server omits the length. A declared
length is checked against the bound before the body is returned. Unknown-length
bodies use one probe byte to enforce the limit. The request deadline stays
active until clean EOF or `Close`.

`PutObjectOutput.SHA256` is the locally computed upload digest. The CLI
computes and reports the SHA256 of bytes received during a download. `GetObject`
does not return a digest; SDK callers that need one must hash `Body` while
consuming it through clean EOF. A download digest describes received bytes and
does not verify them against a remote checksum. ETag is optional opaque
metadata, not a checksum contract.

## Unconfirmed uploads and other tools

A PUT may have reached the server even when the client cannot confirm its
response. The client does not retry, roll back, or send a cleanup DELETE. Fetch
the exact key to a fresh path, then compare its length and SHA256 with the
intended file before deciding whether to upload again. An HTTP 412 alone does
not prove that the existing bytes match your upload.

The native feature adds only one-object PUT and GET. It does not list, HEAD,
delete, append, sync, resume, multipart-transfer, or create presigned URLs.
Use an external S3 tool such as rclone when you need sync or multipart
transfers. The native SDK and CLI remain separate from those tools.
