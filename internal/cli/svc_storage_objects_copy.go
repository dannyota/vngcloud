package cli

import (
	"context"
	"encoding/json"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/spf13/cobra"

	"danny.vn/vngcloud/internal/transport"
	"danny.vn/vngcloud/storage"
)

type objectCopyClient interface {
	CopyObject(context.Context, *storage.CopyObjectInput) (*storage.CopyObjectOutput, error)
}

type storageCopyObjectFlags struct {
	storageObjectOperationFlags
	sourceBucket string
	sourceKey    string
	replace      bool
}

func registerStorageCopyObjectFlags(cmd *cobra.Command, f *storageCopyObjectFlags) {
	registerStorageObjectOperationFlags(cmd, &f.storageObjectOperationFlags, "copy-object")
	cmd.Flags().StringVar(&f.sourceBucket, "source-bucket", "", "source bucket in the same project and region (required)")
	cmd.Flags().StringVar(&f.sourceKey, "source-key", "", "exact UTF-8 source key, at most 1024 bytes (required)")
	cmd.Flags().BoolVar(&f.replace, "replace", false, "permit replacing any current destination; requires --yes")
}

func storageCopyObjectNotes() string {
	return "Copy one current object with native server-side CopyObject, retaining the source. " +
		"Require explicit --source-bucket, --source-key, --bucket, --key, --s3-region, --s3-credentials-file and --replace --yes. " +
		"Both buckets must belong to the same project and region. Read-only blocks copy. " +
		"Concurrent writers can change the selected source or destination; diagnostic version IDs do not authorize cleanup. " +
		"Provider metadata defaults apply without a lossless clone or encryption guarantee. " +
		"Recorded HCM04 encrypted sources return 501; a copy can remain unencrypted despite destination bucket encryption. HAN02 encryption is unverified. " +
		"A complete bounded result is required: HTTP 200 Error is not success. Inspect the destination before repeating an unconfirmed copy. " +
		"GreenNode's exact size limit is unverified; AWS's 5 GB reference is not a measured GreenNode limit. " +
		"No multipart, fallback, version-history clone, create-only copy, or move is provided. " +
		"Output and queries expose accepted metadata only. Linux and macOS credential files are supported; no IAM login or ambient credentials are used."
}

func storageCopyObjectExample() string {
	return "vngcloud storage copy-object --s3-region HCM04 --s3-credentials-file ./storage.credentials --source-bucket app-files --source-key archives/bundle.bin --bucket app-backups --key archives/bundle.bin --replace --yes"
}

func newStorageCopyObjectCmd(e *env) *cobra.Command {
	return newStorageCopyObjectCmdWithClient(e, func(cfg storage.ObjectConfig) (objectCopyClient, error) {
		return storage.NewObjectClient(cfg)
	})
}

func newStorageCopyObjectCmdWithClient(e *env, factory func(storage.ObjectConfig) (objectCopyClient, error)) *cobra.Command {
	cmd := &cobra.Command{Use: "copy-object", Short: "Copy one object server-side, retaining its source; requires --replace --yes", Long: storageCopyObjectNotes(), Example: storageCopyObjectExample(), Args: noArgs}
	f := new(storageCopyObjectFlags)
	registerStorageCopyObjectFlags(cmd, f)
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		return runStorageCopyObject(cmd, e, f, factory)
	}
	return cmd
}

func validateStorageCopyObject(cmd *cobra.Command, e *env, f *storageCopyObjectFlags) error {
	if cmd == nil || e == nil || e.flags == nil || f == nil {
		return newUsageError("object operation flags are required")
	}
	if !f.replace || !e.flags.yes {
		return newUsageError("native create-only copy is unavailable; copy-object requires --replace --yes")
	}
	for _, name := range []string{"region", "project-id"} {
		if cmd.Flags().Changed(name) || cmd.InheritedFlags().Changed(name) {
			return newUsageError("--%s does not select an S3 resource; use --s3-region and --bucket", name)
		}
	}
	if f.region != "HCM04" && f.region != "HAN02" {
		return newUsageError("--s3-region must be HCM04 or HAN02")
	}
	if f.credentialsFile == "" || f.credentialsFile == "-" || strings.HasPrefix(f.credentialsFile, "file://") {
		return newUsageError("--s3-credentials-file must name a private file")
	}
	if !objectBucketPattern.MatchString(f.bucket) || f.bucket == "." || f.bucket == ".." {
		return newUsageError("--bucket must be a valid storage bucket name")
	}
	if f.key == "" || !utf8.ValidString(f.key) || len(f.key) > 1024 {
		return newUsageError("--key must contain 1 through 1024 UTF-8 bytes")
	}
	if !objectBucketPattern.MatchString(f.sourceBucket) || f.sourceBucket == "." || f.sourceBucket == ".." {
		return newUsageError("--source-bucket must be a valid storage bucket name")
	}
	if f.sourceKey == "" || !utf8.ValidString(f.sourceKey) || len(f.sourceKey) > 1024 {
		return newUsageError("--source-key must contain 1 through 1024 UTF-8 bytes")
	}
	if f.sourceBucket == f.bucket && f.sourceKey == f.key {
		return newUsageError("source and destination must be distinct")
	}
	if f.timeout < time.Second || f.timeout > time.Hour {
		return newUsageError("--transfer-timeout must be 1s through 1h")
	}
	if _, err := compileQuery(e.flags.query); err != nil {
		return err
	}
	if e.flags.output != "" && !validOutputFormats[e.flags.output] {
		return newUsageError("--output must be json, table, or text")
	}
	return nil
}

func runStorageCopyObject(cmd *cobra.Command, e *env, f *storageCopyObjectFlags, factory func(storage.ObjectConfig) (objectCopyClient, error)) (retErr error) {
	if err := validateStorageCopyObject(cmd, e, f); err != nil {
		return err
	}
	if factory == nil {
		return newUsageError("object client factory is required")
	}
	on, source, err := readOnlyPreConfig(e.flags)
	if err != nil {
		return err
	}
	if on {
		return readOnlyError{source: source}
	}
	if !objectFilesSupported() {
		return newUsageError("object credential files require Linux or macOS")
	}
	profile, err := loadObjectProfile(e.flags)
	if err != nil {
		return err
	}
	on, source, err = readOnlyProfileValue(profile.ReadOnly, resolvedProfileName(e.flags))
	if err != nil {
		return err
	}
	if on {
		return readOnlyError{source: source}
	}
	format := e.flags.output
	if format == "" {
		format = profile.Output
	}
	if format == "" {
		format = outputJSON
	}
	if !validOutputFormats[format] {
		return newUsageError("--output must be json, table, or text")
	}
	ctx, cancel := context.WithTimeout(cmd.Context(), f.timeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return err
	}
	credentials, err := readS3CredentialsFile(f.credentialsFile)
	if err != nil {
		return err
	}
	client, err := factory(storage.ObjectConfig{Region: f.region, Credentials: credentials, Timeout: f.timeout})
	if err != nil {
		return err
	}
	started, status, count := time.Now(), 0, 0
	defer func() {
		if e.flags.debug {
			logStorageObjectOperation(e, "storage.CopyObject", started, count, status, retErr)
		}
	}()
	out, err := client.CopyObject(ctx, &storage.CopyObjectInput{SourceBucket: f.sourceBucket, SourceKey: f.sourceKey, Bucket: f.bucket, Key: f.key, Replace: f.replace})
	if err != nil {
		return err
	}
	status, count = 200, 1
	values := []string{credentials.AccessKeyID.Reveal(), credentials.SecretAccessKey.Reveal()}
	metadata := struct {
		SourceBucket string
		SourceKey    string
		Bucket       string
		Key          string
		*storage.CopyObjectOutput
	}{
		SourceBucket:     transport.RedactErrorText(f.sourceBucket, values...),
		SourceKey:        transport.RedactErrorText(f.sourceKey, values...),
		Bucket:           transport.RedactErrorText(f.bucket, values...),
		Key:              transport.RedactErrorText(f.key, values...),
		CopyObjectOutput: out,
	}
	data, err := json.Marshal(metadata)
	if err == nil {
		err = renderOutput(e.stdout, format, e.flags.query, json.RawMessage(data), false)
	}
	if err != nil {
		return objectCompletedError("copy", "metadata output failed; do not retry the operation", err)
	}
	return nil
}
