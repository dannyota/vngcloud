package cli

import (
	"context"
	"fmt"
	"regexp"

	"github.com/spf13/cobra"

	"danny.vn/vngcloud/storage"
)

const serviceAccountIDFlagName = "service-account-id"

// serviceAccountIDPattern is the ID shape the SDK accepts for a path segment.
// create-s3-key checks it before the create, since a refused attach would
// otherwise cost a key.
var serviceAccountIDPattern = regexp.MustCompile("^[A-Za-z0-9-]+$")

func registerCreateS3KeyFlags(cmd *cobra.Command) {
	registerSecretFileFlag(cmd)
	cmd.Flags().String(serviceAccountIDFlagName, "",
		"IAM service account ID to restrict the key to; the key is attached before its secret is written")
}

func guardCreateS3Key(cmd *cobra.Command, in any) error {
	if err := guardSecretFilePath(cmd, in); err != nil {
		return err
	}
	if !cmd.Flags().Changed(serviceAccountIDFlagName) {
		return nil
	}
	id, _ := cmd.Flags().GetString(serviceAccountIDFlagName)
	if !serviceAccountIDPattern.MatchString(id) {
		return newUsageError("--%s must be a service account ID of letters, digits, and hyphens, got %q",
			serviceAccountIDFlagName, id)
	}
	return nil
}

// attachFailedError reports an attach that failed inside create-s3-key. It
// wraps the attach's own error, so the envelope keeps the attach's code, and
// it always exits 1.
type attachFailedError struct {
	msg string
	err error
}

func (e attachFailedError) Error() string { return e.msg }

func (e attachFailedError) Unwrap() error { return e.err }

// attachNewS3Key restricts a key this command just created. On any failure,
// an ambiguous 5xx included, it deletes the key, so a secret for an
// unrestricted key never exists. If that delete fails, the error names the
// key so a person can delete it.
func attachNewS3Key(client *storage.Client, ctx context.Context, in *storage.CreateS3KeyInput, keyID, serviceAccountID string) error {
	_, err := client.AttachS3Key(ctx, &storage.AttachS3KeyInput{
		Region: in.Region, ProjectID: in.ProjectID, UserKeyID: keyID, ServiceAccountID: serviceAccountID,
	})
	if err == nil {
		return nil
	}
	if delErr := deleteUnusableS3Key(client, ctx, in, keyID); delErr != nil {
		return attachFailedError{err: err, msg: fmt.Sprintf(
			"s3 key %s exists, unrestricted, and could not be deleted; delete it manually; the attach failed: %s",
			keyID, err)}
	}
	return attachFailedError{err: err, msg: fmt.Sprintf(
		"s3 key %s was created but could not be attached: %s; the key was deleted and no file was written", keyID, err)}
}
