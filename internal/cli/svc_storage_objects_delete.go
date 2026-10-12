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

type objectDeleteClient interface {
	DeleteObject(context.Context, *storage.DeleteObjectInput) (*storage.DeleteObjectOutput, error)
}

type storageObjectDeleteMetadata struct {
	Bucket       string
	Key          string
	DeleteMarker *bool
	VersionID    string
}

func newStorageDeleteObjectCmd(e *env) *cobra.Command {
	return newStorageDeleteObjectCmdWithClient(e, func(cfg storage.ObjectConfig) (objectDeleteClient, error) {
		return storage.NewObjectClient(cfg)
	})
}

func newStorageDeleteObjectCmdWithClient(e *env, factory func(storage.ObjectConfig) (objectDeleteClient, error)) *cobra.Command {
	cmd := &cobra.Command{Use: "delete-object", Short: "Delete one current object name unconditionally; requires --yes", Long: storageObjectOperationNotes("delete-object"), Args: noArgs}
	f := new(storageObjectOperationFlags)
	registerStorageObjectOperationFlags(cmd, f, "delete-object")
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		return runStorageDeleteObject(cmd, e, f, factory)
	}
	return cmd
}

func validateStorageDeleteObject(cmd *cobra.Command, e *env, f *storageObjectOperationFlags) error {
	if cmd == nil || e == nil || e.flags == nil || f == nil {
		return newUsageError("object operation flags are required")
	}
	if !e.flags.yes {
		return newUsageError("delete-object requires --yes")
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

func runStorageDeleteObject(cmd *cobra.Command, e *env, f *storageObjectOperationFlags, factory func(storage.ObjectConfig) (objectDeleteClient, error)) (retErr error) {
	if err := validateStorageDeleteObject(cmd, e, f); err != nil {
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
	started, status := time.Now(), 0
	defer func() {
		if e.flags.debug {
			logStorageObject(e, "storage.DeleteObject", started, 0, status, retErr)
		}
	}()
	out, err := client.DeleteObject(ctx, &storage.DeleteObjectInput{Bucket: f.bucket, Key: f.key})
	if err != nil {
		return err
	}
	status = 204
	values := []string{credentials.AccessKeyID.Reveal(), credentials.SecretAccessKey.Reveal()}
	metadata := storageObjectDeleteMetadata{
		Bucket:       transport.RedactErrorText(f.bucket, values...),
		Key:          transport.RedactErrorText(f.key, values...),
		DeleteMarker: out.DeleteMarker,
		VersionID:    out.VersionID,
	}
	data, err := json.Marshal(metadata)
	if err == nil {
		err = renderOutput(e.stdout, format, e.flags.query, json.RawMessage(data), false)
	}
	if err != nil {
		return objectCompletedError("delete", "metadata output failed; do not retry the operation", err)
	}
	return nil
}
