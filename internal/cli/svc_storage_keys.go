package cli

import (
	"context"
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/storage"
)

// createS3KeyOutput is create-s3-key's JSON shape: the SDK output, whose
// SecretKey prints "[redacted]", plus the path the credentials went to.
// ServiceAccountID names the service account the key was attached to, and
// is empty without --service-account-id; the
// embedded SubUserID stays empty because it comes from the create response.
type createS3KeyOutput struct {
	storage.CreateS3KeyOutput
	SecretFile       string
	ServiceAccountID string
}

// createS3KeyOp is built by hand because --secret-file has no Input field and
// the Output gains SecretFile.
func createS3KeyOp() Op[storage.Client] {
	return Op[storage.Client]{
		name:          "create-s3-key",
		methodName:    "CreateS3Key",
		kind:          kindWrite,
		guard:         guardCreateS3Key,
		noFlag:        map[string]bool{"Region": true, "ProjectID": true},
		globalProject: "ProjectID",
		extraFlags:    registerCreateS3KeyFlags,
		newInput:      func() any { return new(storage.CreateS3KeyInput) },
		newOutput:     func() any { return new(createS3KeyOutput) },
		call:          callCreateS3Key,
	}
}

// awsCredentialsFile is the AWS shared credentials format that rclone and the
// AWS CLI read through AWS_SHARED_CREDENTIALS_FILE.
func awsCredentialsFile(accessKey, secretKey string) []byte {
	return []byte("[default]\naws_access_key_id = " + accessKey + "\naws_secret_access_key = " + secretKey + "\n")
}

// callCreateS3Key creates the key, attaches it when --service-account-id is
// given, then writes the credentials to --secret-file, which guardCreateS3Key
// already checked. A key is unrestricted until its attach, so the file is
// written only after the attach succeeds. The secret exists only in the
// create response, so a key whose attach failed, whose file was not written,
// or whose response held no secret, is deleted.
func callCreateS3Key(cmd *cobra.Command, client *storage.Client, ctx context.Context, in any) (any, error) {
	createIn := in.(*storage.CreateS3KeyInput)
	out, err := client.CreateS3Key(ctx, createIn)
	if errors.Is(err, storage.ErrNoSecret) && out != nil {
		if delErr := deleteUnusableS3Key(client, ctx, createIn, out.UserKeyID); delErr != nil {
			return nil, delErr
		}
		return nil, secretFileFailedError{msg: fmt.Sprintf(
			"s3 key %s was created but the response held no secret; no file was written; the key was deleted",
			out.UserKeyID)}
	}
	if err != nil {
		return nil, err
	}
	serviceAccountID, _ := cmd.Flags().GetString(serviceAccountIDFlagName)
	if serviceAccountID != "" {
		if attachErr := attachNewS3Key(client, ctx, createIn, out.UserKeyID, serviceAccountID); attachErr != nil {
			return nil, attachErr
		}
	}
	path, _ := cmd.Flags().GetString(secretFileFlagName)
	if writeErr := writeSecretFile(path, awsCredentialsFile(out.AccessKey, out.SecretKey.Reveal())); writeErr != nil {
		if delErr := deleteUnusableS3Key(client, ctx, createIn, out.UserKeyID); delErr != nil {
			return nil, delErr
		}
		return nil, newSecretFileWriteFailed("s3 key", out.UserKeyID, writeErr)
	}
	return &createS3KeyOutput{CreateS3KeyOutput: *out, SecretFile: path, ServiceAccountID: serviceAccountID}, nil
}

// deleteUnusableS3Key deletes a key nobody can use. It runs on a context
// detached from ctx with its own timeout, so a canceled command still cleans
// up, and a delete that succeeds or answers code 114 counts as done. The error it returns names the key only
// by its ID.
func deleteUnusableS3Key(client *storage.Client, ctx context.Context, in *storage.CreateS3KeyInput, keyID string) error {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), secretFileCleanupDeleteTimeout)
	defer cancel()
	_, err := client.DeleteS3Key(cleanupCtx, &storage.DeleteS3KeyInput{
		Region: in.Region, ProjectID: in.ProjectID, UserKeyID: keyID,
	})
	if err != nil && !vngcloud.IsNotFound(err) && !isKeyAlreadyDeleted(err) {
		return newSecretFileCleanupFailed("s3 key", keyID)
	}
	return nil
}

// isKeyAlreadyDeleted reports the server's answer for a key that is already
// gone: envelope code 114, not NotFound.
func isKeyAlreadyDeleted(err error) bool {
	var apiErr *vngcloud.APIError
	return errors.As(err, &apiErr) && apiErr.Code == "114"
}
