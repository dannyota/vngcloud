package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/spf13/cobra"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/transport"
	"danny.vn/vngcloud/storage"
)

type objectTransferClient interface {
	PutObject(context.Context, *storage.PutObjectInput) (*storage.PutObjectOutput, error)
	GetObject(context.Context, *storage.GetObjectInput) (*storage.GetObjectOutput, error)
}

type storageObjectMetadata struct {
	Bucket        string
	Key           string
	ContentLength int64
	SHA256        string
	ETag          string
	Destination   string `json:",omitempty"`
}

type storageObjectFlags struct {
	region          string
	credentialsFile string
	bucket          string
	key             string
	body            string
	destination     string
	replace         bool
	timeout         time.Duration
	maxDownload     int64
}

// One registration supplies both real flags and reference fields.
func registerStorageObjectFlags(cmd *cobra.Command, f *storageObjectFlags, put bool) {
	cmd.Flags().StringVar(&f.region, "s3-region", "", "S3 region: HCM04 or HAN02 (required)")
	cmd.Flags().StringVar(&f.credentialsFile, "s3-credentials-file", "", "private AWS-format S3 credentials file (required)")
	cmd.Flags().StringVar(&f.bucket, "bucket", "", "bucket name (required)")
	cmd.Flags().StringVar(&f.key, "key", "", "exact UTF-8 object key, at most 1024 bytes (required)")
	cmd.Flags().DurationVar(&f.timeout, "transfer-timeout", 5*time.Minute, "whole transfer deadline, 1s through 1h")
	cmd.Flags().BoolVar(&f.replace, "replace", false, "replace the object or local destination; requires --yes")
	if put {
		cmd.Flags().StringVar(&f.body, "body", "", "completed regular source file, at most 5000000000 bytes (required)")
	} else {
		cmd.Flags().StringVar(&f.destination, "destination", "", "local file in a trusted existing directory (required)")
		cmd.Flags().Int64Var(&f.maxDownload, "max-download-bytes", storage.DefaultMaxDownloadBytes, "positive download bound, at most 9223372036854775806")
	}
}

func newStoragePutObjectCmd(e *env) *cobra.Command {
	return newStoragePutObjectCmdWithClient(e, func(cfg storage.ObjectConfig) (objectTransferClient, error) { return storage.NewObjectClient(cfg) })
}
func newStorageGetObjectCmd(e *env) *cobra.Command {
	return newStorageGetObjectCmdWithClient(e, func(cfg storage.ObjectConfig) (objectTransferClient, error) { return storage.NewObjectClient(cfg) })
}
func newStoragePutObjectCmdWithClient(e *env, newObjectClient func(storage.ObjectConfig) (objectTransferClient, error)) *cobra.Command {
	return newStorageObjectCmd(e, true, newObjectClient)
}
func newStorageGetObjectCmdWithClient(e *env, newObjectClient func(storage.ObjectConfig) (objectTransferClient, error)) *cobra.Command {
	return newStorageObjectCmd(e, false, newObjectClient)
}
func newStorageObjectCmd(e *env, put bool, newObjectClient func(storage.ObjectConfig) (objectTransferClient, error)) *cobra.Command {
	name, short := "get-object", "Download one object to a local file"
	if put {
		name, short = "put-object", "Upload one completed file as an object"
	}
	cmd := &cobra.Command{Use: name, Short: short, Long: storageObjectNotes(put), Args: noArgs}
	f := new(storageObjectFlags)
	registerStorageObjectFlags(cmd, f, put)
	cmd.RunE = func(cmd *cobra.Command, _ []string) error { return runStorageObject(cmd, e, f, put, newObjectClient) }
	return cmd
}

var objectBucketPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,254}$`)

func validateStorageObject(cmd *cobra.Command, e *env, f *storageObjectFlags, put bool) error {
	for _, name := range []string{"region", "project-id"} {
		if cmd.Flags().Changed(name) || cmd.InheritedFlags().Changed(name) {
			return newUsageError("--%s does not select an S3 resource; use --s3-region, --bucket, and --key", name)
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
	if f.timeout < time.Second || f.timeout > time.Hour {
		return newUsageError("--transfer-timeout must be 1s through 1h")
	}
	if !put && (f.maxDownload <= 0 || f.maxDownload > 9223372036854775806) {
		return newUsageError("--max-download-bytes must be positive and at most 9223372036854775806")
	}
	path := f.destination
	name := "destination"
	if put {
		path, name = f.body, "body"
	}
	if path == "" || path == "-" || strings.HasPrefix(path, "file://") {
		return newUsageError("--%s must name a regular file", name)
	}
	if f.replace && !e.flags.yes {
		return newUsageError("--replace requires --yes")
	}
	if put && f.region == "HAN02" && !f.replace {
		return newUsageError("HAN02 create-only PUT is unavailable; explicit replacement requires --replace --yes")
	}
	if _, err := compileQuery(e.flags.query); err != nil {
		return err
	}
	if e.flags.output != "" && !validOutputFormats[e.flags.output] {
		return newUsageError("--output must be json, table, or text")
	}
	return nil
}

func runStorageObject(cmd *cobra.Command, e *env, f *storageObjectFlags, put bool, newObjectClient func(storage.ObjectConfig) (objectTransferClient, error)) (retErr error) {
	on, source, err := readOnlyPreConfig(e.flags)
	if err != nil {
		return err
	}
	if put && on {
		return readOnlyError{source: source}
	}
	if err := validateStorageObject(cmd, e, f, put); err != nil {
		return err
	}
	if !objectFilesSupported() {
		return newUsageError("object file transfers require Linux or macOS")
	}
	profile, err := loadObjectProfile(e.flags)
	if err != nil {
		return err
	}
	on, source, err = readOnlyProfileValue(profile.ReadOnly, resolvedProfileName(e.flags))
	if err != nil {
		return err
	}
	if put && on {
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
	if put {
		if err := checkObjectSource(f.body); err != nil {
			return err
		}
	} else if err := checkObjectDestination(f.destination, f.replace); err != nil {
		return newUsageError("%s", err)
	}
	credentials, err := readS3CredentialsFile(f.credentialsFile)
	if err != nil {
		return err
	}
	client, err := newObjectClient(storage.ObjectConfig{Region: f.region, Credentials: credentials, Timeout: f.timeout})
	if err != nil {
		return err
	}
	started := time.Now()
	op := "storage.GetObject"
	if put {
		op = "storage.PutObject"
	}
	var count int64
	responseStatus := 0
	defer func() {
		if e.flags.debug {
			logStorageObject(e, op, started, count, responseStatus, retErr)
		}
	}()
	metadata := storageObjectMetadata{Bucket: f.bucket, Key: f.key}
	if put {
		file, cleanup, err := snapshotObjectFile(ctx, f.body)
		if err != nil {
			return err
		}
		completed := false
		defer func() {
			if err := cleanup(); err != nil {
				if completed {
					err = objectCompletedError("upload", "private snapshot cleanup failed; do not retry the transfer", err)
				}
				retErr = errors.Join(retErr, err)
			}
		}()
		info, err := file.Stat()
		if err != nil {
			return safeObjectFileError("could not stat object snapshot", err)
		}
		out, err := client.PutObject(ctx, &storage.PutObjectInput{Bucket: f.bucket, Key: f.key, Body: file, ContentLength: info.Size(), Replace: f.replace})
		if err != nil {
			return err
		}
		completed = true
		responseStatus = 200
		metadata.ContentLength, metadata.SHA256, metadata.ETag = out.ContentLength, out.SHA256, out.ETag
	} else {
		out, err := client.GetObject(ctx, &storage.GetObjectInput{Bucket: f.bucket, Key: f.key, MaxBytes: f.maxDownload})
		if err != nil {
			return err
		}
		responseStatus = 200
		// The helper owns Close. Retain safe SDK errors that its file boundary strips.
		body := &objectErrorBody{ReadCloser: out.Body}
		result, err := downloadObjectFile(ctx, body, f.destination, f.replace)
		count = result.ContentLength
		if err != nil {
			return objectDownloadError(result, errors.Join(err, body.sdkError))
		}
		metadata.ContentLength, metadata.SHA256, metadata.ETag, metadata.Destination = result.ContentLength, result.SHA256, out.ETag, f.destination
	}
	count = metadata.ContentLength
	values := []string{credentials.AccessKeyID.Reveal(), credentials.SecretAccessKey.Reveal()}
	metadata.Bucket = transport.RedactErrorText(metadata.Bucket, values...)
	metadata.Key = transport.RedactErrorText(metadata.Key, values...)
	metadata.Destination = transport.RedactErrorText(metadata.Destination, values...)
	data, err := json.Marshal(metadata)
	if err == nil {
		err = renderOutput(e.stdout, format, e.flags.query, json.RawMessage(data), put)
	}
	if err != nil {
		operation := "download"
		if put {
			operation = "upload"
		}
		return objectCompletedError(operation, "metadata output failed; do not retry the transfer", err)
	}
	return nil
}

func checkObjectSource(path string) error {
	f, err := openRegularFileNonBlocking(path)
	if err != nil {
		return newUsageError("--body must name a readable regular file")
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return newUsageError("--body must name a readable regular file")
	}
	if info.Size() > storage.MaxPutObjectSize {
		return newUsageError("--body exceeds 5000000000 bytes")
	}
	return nil
}

// Do not retain a raw writer error, which can quote credentials or content.
func objectCompletedError(operation, message string, err error) error {
	var queryErr *queryFailedError
	if errors.As(err, &queryErr) {
		return fmt.Errorf("%s completed; %s: %w", operation, message, &queryFailedError{withheldMessage: "metadata query failed"})
	}
	return safeObjectFileError(operation+" completed; "+message, err)
}
func objectDownloadError(result objectFileResult, err error) error {
	if result.Published {
		return fmt.Errorf("download completed and was published; keep the completed file and do not retry: %w", err)
	}
	return err
}

type objectErrorBody struct {
	io.ReadCloser
	sdkError error
}

func (b *objectErrorBody) retain(err error) {
	var api *vngcloud.APIError
	if errors.As(err, &api) {
		b.sdkError = errors.Join(b.sdkError, err)
	}
}
func (b *objectErrorBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	b.retain(err)
	return n, err
}
func (b *objectErrorBody) Close() error { err := b.ReadCloser.Close(); b.retain(err); return err }

func logStorageObject(e *env, operation string, started time.Time, count int64, status int, err error) {
	code := ""
	if err != nil {
		code = "RequestFailed"
		var api *vngcloud.APIError
		if errors.As(err, &api) {
			status = api.StatusCode
		}
		if errors.Is(err, storage.ErrObjectWriteUnconfirmed) {
			code = "ObjectWriteUnconfirmed"
		}
		if errors.Is(err, context.Canceled) {
			code = "Canceled"
		}
		if errors.Is(err, context.DeadlineExceeded) {
			code = "DeadlineExceeded"
		}
	}
	_, _ = fmt.Fprintf(e.stderr, "operation=%s elapsed=%s status=%d bytes=%d code=%s\n", operation, time.Since(started), status, count, code)
}
