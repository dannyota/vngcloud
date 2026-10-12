package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/spf13/cobra"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/transport"
	"danny.vn/vngcloud/storage"
)

type objectOperationsClient interface {
	ListObjects(context.Context, *storage.ListObjectsInput) (*storage.ListObjectsOutput, error)
	HeadObject(context.Context, *storage.HeadObjectInput) (*storage.HeadObjectOutput, error)
}

type storageObjectOperationFlags struct {
	region            string
	credentialsFile   string
	bucket            string
	key               string
	timeout           time.Duration
	prefix            string
	delimiter         string
	continuationToken string
	maxKeys           int
}

func registerStorageObjectOperationFlags(cmd *cobra.Command, f *storageObjectOperationFlags, operation string) {
	cmd.Flags().StringVar(&f.region, "s3-region", "", "S3 region: HCM04 or HAN02 (required)")
	cmd.Flags().StringVar(&f.credentialsFile, "s3-credentials-file", "", "private AWS-format S3 credentials file (required)")
	cmd.Flags().StringVar(&f.bucket, "bucket", "", "bucket name (required)")
	cmd.Flags().DurationVar(&f.timeout, "transfer-timeout", 5*time.Minute, "whole operation deadline, 1s through 1h")
	if operation == "list-objects" {
		cmd.Flags().StringVar(&f.prefix, "prefix", "", "exact UTF-8 key prefix, at most 1024 bytes")
		cmd.Flags().StringVar(&f.delimiter, "delimiter", "", "grouping delimiter: empty or /")
		cmd.Flags().StringVar(&f.continuationToken, "continuation-token", "", "opaque next-page token, at most 8 KiB without controls")
		cmd.Flags().IntVar(&f.maxKeys, "max-keys", 1000, "combined object and common-prefix page bound, 1 through 1000")
	} else {
		cmd.Flags().StringVar(&f.key, "key", "", "exact UTF-8 object key, at most 1024 bytes (required)")
	}
}

func newStorageListObjectsCmd(e *env) *cobra.Command {
	return newStorageListObjectsCmdWithClient(e, func(cfg storage.ObjectConfig) (objectOperationsClient, error) { return storage.NewObjectClient(cfg) })
}
func newStorageHeadObjectCmd(e *env) *cobra.Command {
	return newStorageHeadObjectCmdWithClient(e, func(cfg storage.ObjectConfig) (objectOperationsClient, error) { return storage.NewObjectClient(cfg) })
}
func newStorageListObjectsCmdWithClient(e *env, factory func(storage.ObjectConfig) (objectOperationsClient, error)) *cobra.Command {
	return newStorageObjectOperationCmd(e, "list-objects", factory)
}
func newStorageHeadObjectCmdWithClient(e *env, factory func(storage.ObjectConfig) (objectOperationsClient, error)) *cobra.Command {
	return newStorageObjectOperationCmd(e, "head-object", factory)
}
func newStorageObjectOperationCmd(e *env, operation string, factory func(storage.ObjectConfig) (objectOperationsClient, error)) *cobra.Command {
	short := "List one bounded page of object metadata"
	if operation == "head-object" {
		short = "Inspect one object's metadata without downloading content"
	}
	cmd := &cobra.Command{Use: operation, Short: short, Long: storageObjectOperationNotes(operation), Args: noArgs}
	f := new(storageObjectOperationFlags)
	registerStorageObjectOperationFlags(cmd, f, operation)
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		return runStorageObjectOperation(cmd, e, f, operation, factory)
	}
	return cmd
}

func validateStorageObjectOperation(cmd *cobra.Command, e *env, f *storageObjectOperationFlags, operation string) error {
	if cmd == nil || e == nil || e.flags == nil || f == nil {
		return newUsageError("object operation flags are required")
	}
	if operation != "list-objects" && operation != "head-object" {
		return newUsageError("unsupported object operation")
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
	if f.timeout < time.Second || f.timeout > time.Hour {
		return newUsageError("--transfer-timeout must be 1s through 1h")
	}
	if operation == "head-object" {
		if f.key == "" || !utf8.ValidString(f.key) || len(f.key) > 1024 {
			return newUsageError("--key must contain 1 through 1024 UTF-8 bytes")
		}
	} else {
		if f.maxKeys < 1 || f.maxKeys > 1000 {
			return newUsageError("--max-keys must be 1 through 1000")
		}
		if !utf8.ValidString(f.prefix) || len(f.prefix) > 1024 {
			return newUsageError("--prefix must contain at most 1024 UTF-8 bytes")
		}
		if f.delimiter != "" && f.delimiter != "/" {
			return newUsageError("--delimiter must be empty or /")
		}
		if !utf8.ValidString(f.continuationToken) || len(f.continuationToken) > 8192 || strings.ContainsFunc(f.continuationToken, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
			return newUsageError("--continuation-token must be UTF-8, at most 8 KiB, without control bytes")
		}
	}
	if _, err := compileQuery(e.flags.query); err != nil {
		return err
	}
	if e.flags.output != "" && !validOutputFormats[e.flags.output] {
		return newUsageError("--output must be json, table, or text")
	}
	return nil
}

func runStorageObjectOperation(cmd *cobra.Command, e *env, f *storageObjectOperationFlags, operation string, factory func(storage.ObjectConfig) (objectOperationsClient, error)) (retErr error) {
	if err := validateStorageObjectOperation(cmd, e, f, operation); err != nil {
		return err
	}
	if factory == nil {
		return newUsageError("object client factory is required")
	}
	if _, _, err := readOnlyPreConfig(e.flags); err != nil {
		return err
	}
	if !objectFilesSupported() {
		return newUsageError("object credential files require Linux or macOS")
	}
	profile, err := loadObjectProfile(e.flags)
	if err != nil {
		return err
	}
	if _, _, err := readOnlyProfileValue(profile.ReadOnly, resolvedProfileName(e.flags)); err != nil {
		return err
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
	started := time.Now()
	count, status := 0, 0
	op := "storage.ListObjects"
	if operation == "head-object" {
		op = "storage.HeadObject"
	}
	defer func() {
		if e.flags.debug {
			logStorageObjectOperation(e, op, started, count, status, retErr)
		}
	}()
	var result any
	if operation == "list-objects" {
		page, err := client.ListObjects(ctx, &storage.ListObjectsInput{Bucket: f.bucket, Prefix: f.prefix, Delimiter: f.delimiter, ContinuationToken: f.continuationToken, MaxKeys: f.maxKeys})
		if err != nil {
			return err
		}
		result = page
		count = len(page.Items) + len(page.CommonPrefixes)
	} else {
		metadata, err := client.HeadObject(ctx, &storage.HeadObjectInput{Bucket: f.bucket, Key: f.key})
		if err != nil {
			return err
		}
		values := []string{credentials.AccessKeyID.Reveal(), credentials.SecretAccessKey.Reveal()}
		result = struct {
			Bucket string
			Key    string
			*storage.HeadObjectOutput
		}{
			Bucket:           transport.RedactErrorText(f.bucket, values...),
			Key:              transport.RedactErrorText(f.key, values...),
			HeadObjectOutput: metadata,
		}
		count = 1
	}
	status = 200
	// Marshal accepted SDK metadata before the shared renderer's map policy.
	data, err := json.Marshal(result)
	if err == nil {
		err = renderOutput(e.stdout, format, e.flags.query, json.RawMessage(data), false)
	}
	if err != nil {
		return objectCompletedError(operation, "metadata output failed; do not retry the operation", err)
	}
	return nil
}

func logStorageObjectOperation(e *env, operation string, started time.Time, count, status int, err error) {
	code := ""
	if err != nil {
		code = "RequestFailed"
		var api *vngcloud.APIError
		if errors.As(err, &api) {
			status = api.StatusCode
		}
		if errors.Is(err, context.Canceled) {
			code = "Canceled"
		}
		if errors.Is(err, context.DeadlineExceeded) {
			code = "DeadlineExceeded"
		}
		if errors.Is(err, storage.ErrObjectCopyUnconfirmed) {
			code = "ObjectCopyUnconfirmed"
		}
	}
	_, _ = fmt.Fprintf(e.stderr, "operation=%s elapsed=%s status=%d count=%d code=%s\n", operation, time.Since(started), status, count, code)
}
